"""Package prebuilt native desktop hosts without bundling development dependencies."""

import argparse
import hashlib
import os
import plistlib
import re
import shutil
import subprocess
import tarfile
import tempfile
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


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
    platform: str, arch: str, binary: Path, version: str, out: Path, makensis: str
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
            prefix = "/" if os.name == "nt" else "-"
            subprocess.run(
                [
                    makensis,
                    f"{prefix}DVERSION={version}",
                    f"{prefix}DSOURCE_EXE={binary.resolve()}",
                    f"{prefix}DICON_FILE={ROOT / 'packaging/windows/app.ico'}",
                    f"{prefix}DOUTPUT={output}",
                    str(ROOT / "packaging/windows/installer.nsi"),
                ],
                check=True,
            )
            products.append(output)
            shutil.copy2(binary, stage / "Garbage Truck.exe")
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
                        "LSMinimumSystemVersion": "12.0",
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
    parser.add_argument("--version", default="0.3.0")
    parser.add_argument("--out", type=Path, default=ROOT / "dist")
    parser.add_argument("--makensis", default="makensis")
    args = parser.parse_args()
    for path in package(
        args.platform, args.arch, args.binary, args.version, args.out, args.makensis
    ):
        print(path)


if __name__ == "__main__":
    main()
