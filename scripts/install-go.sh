#!/usr/bin/env bash
set -eu
repo_dir="$(cd "$(dirname "$0")/.." && pwd)"
go_version=1.27.1
go_sha256=63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445
if [ -x "$repo_dir/.tools/go/bin/go" ] && [ "$("$repo_dir/.tools/go/bin/go" version | awk '{print $3}')" = "go$go_version" ]; then
  exit 0
fi
if [ "$(uname -s)" != Linux ] || [ "$(uname -m)" != x86_64 ]; then
  echo "Install Go $go_version for your OS from https://go.dev/dl/. This helper targets Linux amd64." >&2
  exit 1
fi
mkdir -p "$repo_dir/.tools"
go_archive="$(mktemp /tmp/garbage-go.XXXXXX.tar.gz)"
trap 'rm -f "$go_archive"' EXIT
curl --fail --location --silent --show-error "https://go.dev/dl/go$go_version.linux-amd64.tar.gz" --output "$go_archive"
echo "$go_sha256  $go_archive" | sha256sum --check
tar -xzf "$go_archive" -C "$repo_dir/.tools"
