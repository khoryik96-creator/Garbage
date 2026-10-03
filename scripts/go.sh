#!/usr/bin/env bash
set -eu
repo_dir="$(cd "$(dirname "$0")/.." && pwd)"
export GOPATH="${GOPATH:-/tmp/garbage-go-path}"
export GOMODCACHE="${GOMODCACHE:-/tmp/garbage-go-modules}"
export GOCACHE="${GOCACHE:-/tmp/garbage-go-cache}"
export GOTOOLCHAIN=local
# Native Git access works in this environment; normal proxy archives are blocked.
export GOPROXY="${GOPROXY:-direct}"
cd "$repo_dir"
exec "$repo_dir/.tools/go/bin/go" "$@"
