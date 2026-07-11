#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
case "$(uname -s)" in Darwin) os=darwin ;; Linux) os=linux ;; *) echo "ravel: unsupported operating system" >&2; exit 1 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) echo "ravel: unsupported architecture" >&2; exit 1 ;; esac
binary="$script_dir/../bin/ravel_${os}_${arch}"
if [ -x "$binary" ]; then
  exec "$binary" "$@"
fi

# The repository-local .codex skill deliberately avoids a third binary copy;
# use the validator-synchronized Codex marketplace package in the checkout.
checkout_root=$(CDPATH= cd -- "$script_dir/../../../.." 2>/dev/null && pwd || true)
checkout_binary="$checkout_root/.agents/plugins/plugins/ravel/skills/ravel/bin/ravel_${os}_${arch}"
if [ -x "$checkout_binary" ]; then
  exec "$checkout_binary" "$@"
fi

echo "ravel: no compatible bundled binary found; update or reinstall Ravel with consent" >&2
exit 1
