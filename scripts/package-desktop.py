"""Package prebuilt native desktop hosts without bundling development dependencies."""

import argparse
import hashlib
import json
import os
import plistlib
import re
import shutil
import struct
import subprocess
import tarfile
import tempfile
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MACOS_MINIMUM = "13.0"


def macho_minimums(binary: Path) -> dict[str, str]:
    """Read load commands from the binary, including every universal slice."""
    data = binary.read_bytes()

    def read_slice(payload: bytes) -> dict[str, str]:
        if len(payload) < 8:
            raise ValueError("Not a complete Mach-O binary.")
        magic = payload[:4]
        if magic in (b"\xca\xfe\xba\xbe", b"\xca\xfe\xba\xbf"):
            count = struct.unpack_from(">I", payload, 4)[0]
            fat64 = magic == b"\xca\xfe\xba\xbf"
            entry_size = 32 if fat64 else 20
            if count > 16 or 8 + count * entry_size > len(payload):
                raise ValueError("Invalid universal Mach-O header.")
            result: dict[str, str] = {}
            for index in range(count):
                entry = 8 + index * entry_size
                offset, size = struct.unpack_from(">QQ" if fat64 else ">II", payload, entry + 8)
                if offset + size > len(payload):
                    raise ValueError("Truncated universal Mach-O slice.")
                for arch, minimum in read_slice(payload[offset : offset + size]).items():
                    if arch in result:
                        raise ValueError("Duplicate Mach-O architecture.")
                    result[arch] = minimum
            return result
        if magic not in (b"\xcf\xfa\xed\xfe", b"\xfe\xed\xfa\xcf") or len(payload) < 32:
            raise ValueError("Expected a 64-bit macOS Mach-O binary.")
        endian = "<" if magic == b"\xcf\xfa\xed\xfe" else ">"
        cpu, _, _, count, command_bytes = struct.unpack_from(endian + "IIIII", payload, 4)
        arch = {0x01000007: "amd64", 0x0100000C: "arm64"}.get(cpu)
        if arch is None or count > 4096 or 32 + command_bytes > len(payload):
            raise ValueError("Invalid Mach-O architecture or command table.")
        offset, minimum = 32, None
        for _ in range(count):
            if offset + 8 > 32 + command_bytes:
                raise ValueError("Truncated Mach-O load command.")
            command, size = struct.unpack_from(endian + "II", payload, offset)
            if size < 8 or offset + size > 32 + command_bytes:
                raise ValueError("Invalid Mach-O load command length.")
            if command == 0x32:
                if size < 24:
                    raise ValueError("Invalid LC_BUILD_VERSION.")
                platform, version = struct.unpack_from(endian + "II", payload, offset + 8)
                if platform != 1:
                    raise ValueError("The Mach-O binary does not target macOS.")
                minimum = version
            elif command == 0x24:
                if size < 16:
                    raise ValueError("Invalid LC_VERSION_MIN_MACOSX.")
                minimum = struct.unpack_from(endian + "I", payload, offset + 8)[0]
            offset += size
        if minimum is None:
            raise ValueError("The binary does not declare its macOS deployment target.")
        return {arch: f"{minimum >> 16}.{(minimum >> 8) & 255}.{minimum & 255}"}

    return read_slice(data)


def verify_macos_target(binary: Path, arch: str) -> dict[str, str]:
    minimums = macho_minimums(binary)
    if minimums != {arch: MACOS_MINIMUM + ".0"}:
        raise ValueError(f"Mach-O target {minimums} disagrees with {arch} / macOS {MACOS_MINIMUM}.")
    return minimums


def pe_signature_present(binary: Path) -> bool:
    data = binary.read_bytes()
    if data[:2] != b"MZ" or len(data) < 64:
        raise ValueError("Expected a Windows PE executable.")
    offset = struct.unpack_from("<I", data, 60)[0]
    if data[offset : offset + 4] != b"PE\0\0" or offset + 24 + 160 > len(data):
        raise ValueError("Invalid Windows PE header.")
    optional = offset + 24
    magic = struct.unpack_from("<H", data, optional)[0]
    if magic not in (0x10B, 0x20B):
        raise ValueError("Invalid Windows PE optional header.")
    certificate = optional + (96 if magic == 0x10B else 112) + 4 * 8
    address, size = struct.unpack_from("<II", data, certificate)
    return address != 0 and size >= 8 and address + size <= len(data)


def sign_windows_file(binary: Path) -> None:
    subprocess.run(
        [
            "powershell.exe",
            "-NoProfile",
            "-ExecutionPolicy",
            "Bypass",
            "-File",
            str(ROOT / "scripts/sign-windows.ps1"),
            "-Target",
            str(binary),
        ],
        check=True,
    )


def executable(path: Path) -> None:
    path.chmod(0o755)


def archive_zip(source: Path, target: Path) -> None:
    with zipfile.ZipFile(target, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for path in sorted(source.rglob("*")):
            if path.is_file():
                info = zipfile.ZipInfo.from_file(path, path.relative_to(source).as_posix())
                info.create_system = 3
                info.external_attr = (0o100755 if os.access(path, os.X_OK) else 0o100644) << 16
                archive.writestr(info, path.read_bytes(), compress_type=zipfile.ZIP_DEFLATED)


def package(
    platform: str,
    arch: str,
    binary: Path,
    version: str,
    out: Path,
    makensis: str,
    sign_windows: bool = False,
) -> list[Path]:
    if not re.fullmatch(r"\d+\.\d+\.\d+", version):
        raise ValueError("Use a version such as 0.3.0.")
    if platform not in {"windows", "linux", "macos"} or arch not in {"amd64", "arm64"}:
        raise ValueError("Unsupported package target.")
    if not binary.is_file():
        raise ValueError("Build the desktop executable first.")
    out.mkdir(parents=True, exist_ok=True)
    stem = f"Garbage-Truck-{version}-{platform}-{arch}"
    products: list[Path] = []
    with tempfile.TemporaryDirectory(prefix="garbage-package-") as temp:
        stage = Path(temp)
        if platform == "windows":
            if arch != "amd64":
                raise ValueError("The Windows installer currently supports x64.")
            output = (out / f"{stem}-Setup.exe").resolve()
            staged_binary = stage / "Garbage Truck.exe"
            shutil.copy2(binary, staged_binary)
            if sign_windows:
                if os.name != "nt":
                    raise ValueError("Authenticode signing requires Windows.")
                sign_windows_file(staged_binary)
            signature_present = pe_signature_present(staged_binary)
            prefix = "/" if os.name == "nt" else "-"
            subprocess.run(
                [
                    makensis,
                    f"{prefix}DVERSION={version}",
                    f"{prefix}DSOURCE_EXE={staged_binary.resolve()}",
                    f"{prefix}DICON_FILE={ROOT / 'packaging/windows/app.ico'}",
                    f"{prefix}DOUTPUT={output}",
                    *(
                        [f"{prefix}DSIGN_SCRIPT={ROOT / 'scripts/sign-windows.ps1'}"]
                        if sign_windows
                        else []
                    ),
                    str(ROOT / "packaging/windows/installer.nsi"),
                ],
                check=True,
            )
            products.append(output)
            if sign_windows:
                sign_windows_file(output)
            signing = out / f"{stem}-Signing.json"
            signing.write_text(
                json.dumps(
                    {
                        "version": version,
                        "application": {
                            "signature_present": signature_present,
                            "verified_on_windows": sign_windows,
                        },
                        "installer": {
                            "signature_present": pe_signature_present(output),
                            "verified_on_windows": sign_windows,
                        },
                        "uninstaller_signed": sign_windows,
                    },
                    indent=2,
                )
                + "\n",
                encoding="utf-8",
            )
            products.append(signing)
            (stage / "START HERE.txt").write_text(
                "Double-click Garbage Truck.exe. The interface opens in your browser.\n"
                "Use Quit Garbage Truck in the app when you are done.\n"
                "Your data is saved in %LOCALAPPDATA%\\GarbageTruck.\n"
                "This prototype uses demo profiles. No Go or Python installation is needed.\n",
                encoding="utf-8",
            )
            portable = out / f"{stem}-Portable.zip"
            archive_zip(stage, portable)
            products.append(portable)
        elif platform == "macos":
            minimums = verify_macos_target(binary, arch)
            contents = stage / "Garbage Truck.app/Contents"
            (contents / "MacOS").mkdir(parents=True)
            target = contents / "MacOS/garbage-truck-desktop"
            shutil.copy2(binary, target)
            executable(target)
            with (contents / "Info.plist").open("wb") as file:
                plistlib.dump(
                    {
                        "CFBundleName": "Garbage Truck",
                        "CFBundleDisplayName": "Garbage Truck",
                        "CFBundleIdentifier": "com.garbagetruck.desktop",
                        "CFBundleExecutable": "garbage-truck-desktop",
                        "CFBundlePackageType": "APPL",
                        "CFBundleShortVersionString": version,
                        "CFBundleVersion": version,
                        "LSMinimumSystemVersion": MACOS_MINIMUM,
                        "LSUIElement": True,
                        "NSHighResolutionCapable": True,
                    },
                    file,
                )
            (stage / "START HERE.txt").write_text(
                "Drag Garbage Truck.app to Applications, then open it.\n"
                "The interface opens in your browser. Use Quit Garbage Truck to stop it.\n"
                "The prototype is unsigned. macOS may require permission in Privacy & Security.\n"
                "Your workspace is saved in ~/Library/Application Support/GarbageTruck.\n",
                encoding="utf-8",
            )
            output = out / f"{stem}.zip"
            archive_zip(stage, output)
            products.append(output)
            metadata = out / f"{stem}-Compatibility.json"
            metadata.write_text(
                json.dumps(
                    {
                        "version": version,
                        "mach_o_minimums": minimums,
                        "plist_minimum": MACOS_MINIMUM,
                    },
                    indent=2,
                )
                + "\n",
                encoding="utf-8",
            )
            products.append(metadata)
        else:
            target = stage / "garbage-truck-desktop"
            shutil.copy2(binary, target)
            executable(target)
            shutil.copy2(ROOT / "internal/web/static/icon.svg", stage / "icon.svg")
            for name in ["install.sh", "uninstall.sh"]:
                shutil.copy2(ROOT / f"packaging/linux/{name}", stage / name)
                executable(stage / name)
            (stage / "START HERE.txt").write_text(
                "Run: sh install.sh\n"
                "Or double-click garbage-truck-desktop to run without installing.\n"
                "The interface opens in your browser. Use Quit Garbage Truck to stop it.\n"
                "Your workspace is kept when the app is upgraded or uninstalled.\n",
                encoding="utf-8",
            )
            output = out / f"{stem}.tar.gz"
            with tarfile.open(output, "w:gz") as archive:
                for path in sorted(stage.iterdir()):
                    archive.add(path, arcname=f"Garbage-Truck/{path.name}")
            products.append(output)
    for product in products:
        digest = hashlib.sha256(product.read_bytes()).hexdigest()
        product.with_name(product.name + ".sha256").write_text(
            f"{digest}  {product.name}\n", encoding="utf-8"
        )
    return products


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", required=True, choices=["windows", "linux", "macos"])
    parser.add_argument("--arch", required=True, choices=["amd64", "arm64"])
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--version", default="0.5.0")
    parser.add_argument("--out", type=Path, default=ROOT / "dist")
    parser.add_argument("--makensis", default="makensis")
    parser.add_argument("--sign-windows", action="store_true")
    args = parser.parse_args()
    for path in package(
        args.platform,
        args.arch,
        args.binary,
        args.version,
        args.out,
        args.makensis,
        args.sign_windows,
    ):
        print(path)


if __name__ == "__main__":
    main()
