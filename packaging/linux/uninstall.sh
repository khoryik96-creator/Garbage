#!/usr/bin/env sh
set -eu
data_root=${XDG_DATA_HOME:-"$HOME/.local/share"}
case "$data_root" in /*) ;; *) echo "XDG_DATA_HOME must be an absolute path." >&2; exit 1 ;; esac
app_dir="$data_root/garbage-truck-app"
if [ -f "$data_root/GarbageTruck/app.lock" ] && command -v flock >/dev/null 2>&1 && ! flock -n "$data_root/GarbageTruck/app.lock" true; then
  echo "Quit Garbage Truck from its app menu before uninstalling." >&2
  exit 1
fi
rm -f "$data_root/applications/garbage-truck.desktop" "$app_dir/garbage-truck-desktop" "$app_dir/icon.svg" "$app_dir/uninstall.sh"
rmdir "$app_dir" 2>/dev/null || true
printf '%s\n' "Garbage Truck is uninstalled. Your saved workspace in $data_root/GarbageTruck has been kept."
