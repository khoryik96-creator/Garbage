# Install Garbage Truck

The desktop preview includes the interface, database engine, and Country rules in
one app. It opens in your browser and runs on your computer. You do not need a
development environment, a Python worker, or account credentials to try it.

## Windows

1. Open the [0.3.0 downloads](https://github.com/khoryik96-creator/Garbage/releases/tag/v0.3.0).
2. Download the Windows x64 **Setup.exe** file and run it.
3. Click **Install**, then **Finish** with **Open Garbage Truck** selected.

Setup adds Start-menu and desktop shortcuts for your Windows account. It does not
require administrator access. The interface opens automatically; future launches
reuse an already-running app instead of starting a second workspace.

For a portable copy, extract the Windows **Portable.zip** and double-click
**Garbage Truck.exe**. Both portable and installed copies use the same saved-work
folder for the same Windows user. Windows 10/11 x64 is the supported target.

The preview is unsigned. Windows may show a publisher warning because a signing
certificate has not been configured. Only use the downloads from this repository;
the release includes SHA-256 checksums.

## macOS

Extract the ZIP matching your processor, drag **Garbage Truck.app** into
**Applications**, and open it. The release filename states `arm64` for Apple
Silicon or `amd64` for Intel. Use only a package matching your processor.

The preview is not notarized. If macOS blocks it, authorize the downloaded app in
**System Settings → Privacy & Security**. Signing and notarization are future
distribution work; no bypass commands are required in the installation flow.

## Linux

Extract the matching TAR.GZ, open the extracted **Garbage-Truck** folder, and run:

```sh
sh install.sh
```

The app gets an entry in your applications menu and starts automatically. For a
portable launch, open `garbage-truck-desktop` directly. A normal desktop session
with a default browser is required for automatic opening. For headless testing,
use `--no-browser` and read `instance.json` in the workspace folder.

## Your workspace

The app starts with demo profiles. Begin with **Preview**, inspect the evidence,
then start a **Review** run when you want to try approving changes. **Profiles**,
**Run history**, **Audit trail**, and **Workspace settings** are in the sidebar.
JobAdder login and AI document processing are not enabled in this preview.

Closing the browser tab keeps the local app running. Choose **Quit Garbage Truck**
or **Save and quit app** in Workspace settings to stop it. Your work is saved
automatically. Reopening the app keeps the same profiles, runs, and audit history.

| Computer | Saved workspace |
| --- | --- |
| Windows | `%LOCALAPPDATA%\GarbageTruck` |
| macOS | `~/Library/Application Support/GarbageTruck` |
| Linux | `$XDG_DATA_HOME/GarbageTruck`, normally `~/.local/share/GarbageTruck` |

Workspace settings shows the actual folder. To back up, quit the app and copy the
whole folder. Upgrades and uninstallers leave it in place. Windows installs the
program in `%LOCALAPPDATA%\Programs\Garbage Truck`; Linux installs the program in
the separate `garbage-truck-app` folder. Do not delete the saved-work folder unless
you want to remove your profiles and history.

Developer web runs continue using the repository's `.data/autocoder.db`. They are
kept separate from an installed desktop workspace. To open an existing developer
database with the desktop launcher, pass `--data-dir` with that database's folder.

## Release builds

The **Desktop installers** workflow builds native Go/SQLite hosts on Windows,
macOS, and Linux, exercises launch/reopen/quit/restart, and packages each app.
Windows also checks silent installation, upgrading, and preservation on uninstall.
Branch builds are downloadable from GitHub Actions. A version tag publishes a
demo prerelease only after every native build succeeds. Platform binaries and
SHA-256 checksum files are attached to that release.

Local developer builds need Go 1.27.1 and a C compiler. Windows packaging also
needs NSIS. `scripts/package-desktop.py` takes an already-built binary; these are
build requirements, not requirements for the person installing the app.
