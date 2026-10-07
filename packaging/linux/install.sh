#!/usr/bin/env sh
set -eu
package_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
data_root=${XDG_DATA_HOME:-"$HOME/.local/share"}
case "$data_root" in /*) ;; *) echo "XDG_DATA_HOME must be an absolute path." >&2; exit 1 ;; esac
app_dir="$data_root/garbage-truck-app"
if [ -f "$data_root/GarbageTruck/app.lock" ] && command -v flock >/dev/null 2>&1 && ! flock -n "$data_root/GarbageTruck/app.lock" true; then
  echo "Quit Garbage Truck from its app menu before upgrading." >&2
  exit 1
fi
mkdir -p "$app_dir" "$data_root/applications"
install -m 755 "$package_dir/garbage-truck-desktop" "$app_dir/garbage-truck-desktop"
install -m 644 "$package_dir/icon.svg" "$app_dir/icon.svg"
install -m 755 "$package_dir/uninstall.sh" "$app_dir/uninstall.sh"
cat > "$app_dir/launch.sh" <<'LAUNCH'
#!/bin/sh
exec "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/garbage-truck-desktop" "$@"
LAUNCH
chmod 755 "$app_dir/launch.sh"
# Exec is parsed twice: desktop-entry value escapes, then command quoting.
# Escape for command quoting first and double backslashes for value parsing.
desktop_executable=$(printf '%s' "$app_dir/launch.sh" | sed 's/\\/\\\\/g;s/"/\\"/g;s/`/\\`/g;s/\$/\\$/g;s/%/%%/g;s/\\/\\\\/g')
desktop_icon=$(printf '%s' "$app_dir/icon.svg" | sed 's/\\/\\\\/g')
cat > "$data_root/applications/garbage-truck.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=Garbage Truck
Comment=Candidate coding workspace
# Keep argv[0] fixed: GIO checks it before expanding percent field codes.
Exec=/bin/sh "$desktop_executable"
Icon=$desktop_icon
Terminal=false
Categories=Office;Utility;
StartupNotify=false
EOF
if command -v update-desktop-database >/dev/null 2>&1; then update-desktop-database "$data_root/applications" >/dev/null 2>&1 || true; fi
printf '%s\n' "Garbage Truck is installed. Open it from your applications menu." "Your workspace is kept separately in $data_root/GarbageTruck."
if [ "${1:-}" != "--no-launch" ]; then "$app_dir/garbage-truck-desktop" >/dev/null 2>&1 & fi
