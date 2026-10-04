import hashlib
import importlib.util
import os
import plistlib
import shutil
import struct
import subprocess
import tarfile
import tempfile
import time
import unittest
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location(
    "desktop_package", ROOT / "scripts/package-desktop.py"
)
assert SPEC is not None and SPEC.loader is not None
PACKAGE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PACKAGE)


def macho(arch: str = "arm64", major: int = 13) -> bytes:
    cpu = 0x0100000C if arch == "arm64" else 0x01000007
    return struct.pack("<IIIIIIII", 0xFEEDFACF, cpu, 0, 2, 1, 24, 0, 0) + struct.pack(
        "<IIIIII", 0x32, 24, 1, major << 16, 26 << 16, 0
    )


class PackageTests(unittest.TestCase):
    def test_linux_install_upgrade_uninstall_keeps_workspace(self) -> None:
        with tempfile.TemporaryDirectory(prefix="Garbage package ") as temp:
            directory = Path(temp)
            binary = directory / "binary"
            binary.write_bytes(b"payload-one")
            binary.chmod(0o755)
            output = PACKAGE.package("linux", "amd64", binary, "0.3.0", directory / "out", "unused")
            archive = output[0]
            self.assertEqual(
                archive.with_name(archive.name + ".sha256").read_text().split()[0],
                hashlib.sha256(archive.read_bytes()).hexdigest(),
            )
            extracted = directory / "extracted"
            with tarfile.open(archive) as file:
                file.extractall(extracted, filter="data")
            data = directory / "user data with spaces"
            workspace = data / "GarbageTruck"
            workspace.mkdir(parents=True)
            marker = workspace / "autocoder.db"
            marker.write_bytes(b"keep saved records")
            environment = dict(os.environ, XDG_DATA_HOME=str(data))
            installer = extracted / "Garbage-Truck/install.sh"
            for _ in range(2):
                subprocess.run(["sh", str(installer), "--no-launch"], env=environment, check=True)
                self.assertEqual(marker.read_bytes(), b"keep saved records")
            installed = data / "garbage-truck-app/garbage-truck-desktop"
            self.assertEqual(installed.read_bytes(), b"payload-one")
            self.assertTrue(os.access(installed, os.X_OK))
            entry = (data / "applications/garbage-truck.desktop").read_text()
            self.assertIn(f'Exec=/bin/sh "{data / "garbage-truck-app/launch.sh"}"', entry)
            self.assertIn("Terminal=false", entry)
            subprocess.run(
                ["sh", str(data / "garbage-truck-app/uninstall.sh")], env=environment, check=True
            )
            self.assertEqual(marker.read_bytes(), b"keep saved records")
            self.assertFalse(installed.exists())

    def test_macos_bundle_preserves_executable_and_metadata(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            binary = directory / "binary"
            binary.write_bytes(macho())
            output = PACKAGE.package("macos", "arm64", binary, "0.3.0", directory / "out", "unused")
            with zipfile.ZipFile(output[0]) as archive:
                path = "Garbage Truck.app/Contents/MacOS/garbage-truck-desktop"
                self.assertEqual(archive.read(path), macho())
                self.assertTrue((archive.getinfo(path).external_attr >> 16) & 0o111)
                info = plistlib.loads(archive.read("Garbage Truck.app/Contents/Info.plist"))
                self.assertEqual(info["CFBundleExecutable"], "garbage-truck-desktop")
                self.assertEqual(info["CFBundleShortVersionString"], "0.3.0")
                self.assertEqual(info["LSMinimumSystemVersion"], "13.0")

    def test_macos_rejects_incompatible_or_malformed_binary(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            binary = directory / "binary"
            for data in [
                macho(major=26),
                macho(major=12),
                macho(arch="amd64"),
                b"not-a-binary",
                macho()[:40],
            ]:
                binary.write_bytes(data)
                with self.assertRaises(ValueError):
                    PACKAGE.package("macos", "arm64", binary, "0.4.0", directory / "out", "unused")

    @unittest.skipUnless(shutil.which("gio"), "GIO is required for real desktop launch checks")
    def test_gio_launches_special_character_install_paths(self) -> None:
        with tempfile.TemporaryDirectory(prefix="Garbage GIO ") as temp:
            directory = Path(temp)
            for special in ["cash$", 'quote"', "tick`", "percent%", "slash\\", 'all $"`%\\']:
                with self.subTest(path=special):
                    root = directory / special
                    marker = directory / "launched"
                    marker.unlink(missing_ok=True)
                    package = directory / "package"
                    package.mkdir(exist_ok=True)
                    (package / "garbage-truck-desktop").write_text(
                        '#!/bin/sh\nprintf launched > "$GT_TEST_MARKER"\n', encoding="utf-8"
                    )
                    shutil.copy2(ROOT / "packaging/linux/install.sh", package / "install.sh")
                    shutil.copy2(ROOT / "packaging/linux/uninstall.sh", package / "uninstall.sh")
                    shutil.copy2(ROOT / "internal/web/static/icon.svg", package / "icon.svg")
                    environment = dict(
                        os.environ, XDG_DATA_HOME=str(root), GT_TEST_MARKER=str(marker)
                    )
                    subprocess.run(
                        ["sh", str(package / "install.sh"), "--no-launch"],
                        check=True,
                        env=environment,
                        capture_output=True,
                    )
                    entry = root / "applications/garbage-truck.desktop"
                    subprocess.run(
                        ["gio", "launch", str(entry)],
                        check=True,
                        env=environment,
                        capture_output=True,
                    )
                    deadline = time.monotonic() + 3
                    while not marker.exists() and time.monotonic() < deadline:
                        time.sleep(0.02)
                    self.assertTrue(marker.exists(), f"GIO did not launch {special!r}")
                    self.assertEqual(marker.read_text(), "launched")

    def test_invalid_target_or_version_cannot_create_a_package(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            binary = directory / "binary"
            binary.write_bytes(b"payload")
            for platform, arch, version in [
                ("linux", "amd64", "../../bad"),
                ("unknown", "amd64", "0.3.0"),
                ("windows", "arm64", "0.3.0"),
            ]:
                with self.assertRaises(ValueError):
                    PACKAGE.package(platform, arch, binary, version, directory / "out", "unused")


if __name__ == "__main__":
    unittest.main()
