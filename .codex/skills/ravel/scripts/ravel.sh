#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
case "$(uname -s)" in Darwin) os=darwin ;; Linux) os=linux ;; *) echo "ravel: unsupported operating system" >&2; exit 1 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) echo "ravel: unsupported architecture" >&2; exit 1 ;; esac
binary="$script_dir/../bin/ravel_${os}_${arch}"
if [ ! -x "$binary" ]; then
  echo "ravel: bundled binary is missing: $binary" >&2
  exit 1
fi
exec "$binary" "$@"
