import hashlib
import importlib.util
import os
import plistlib
import subprocess
import tarfile
import tempfile
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
            self.assertIn(f'Exec="{installed}"', entry)
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
            binary.write_bytes(b"native-payload")
            output = PACKAGE.package("macos", "arm64", binary, "0.3.0", directory / "out", "unused")
            with zipfile.ZipFile(output[0]) as archive:
                path = "Garbage Truck.app/Contents/MacOS/garbage-truck-desktop"
                self.assertEqual(archive.read(path), b"native-payload")
                self.assertTrue((archive.getinfo(path).external_attr >> 16) & 0o111)
                info = plistlib.loads(archive.read("Garbage Truck.app/Contents/Info.plist"))
                self.assertEqual(info["CFBundleExecutable"], "garbage-truck-desktop")
                self.assertEqual(info["CFBundleShortVersionString"], "0.3.0")

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
