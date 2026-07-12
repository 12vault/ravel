#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
case "$(uname -s)" in Darwin) os=darwin ;; Linux) os=linux ;; *) echo "ravel: unsupported operating system" >&2; exit 1 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) echo "ravel: unsupported architecture" >&2; exit 1 ;; esac
binary="$script_dir/../bin/ravel_${os}_${arch}"
if [ -x "$binary" ]; then
  exec "$binary" "$@"
fi

# The canonical source skill and repository-local .codex mirror deliberately
# avoid a third binary copy. They sit at different depths, so probe both valid
# checkout layouts for the validator-synchronized marketplace package.
for relative_root in ../../.. ../../../..; do
  checkout_root=$(CDPATH= cd -- "$script_dir/$relative_root" 2>/dev/null && pwd || true)
  [ -n "$checkout_root" ] || continue
  checkout_binary="$checkout_root/.agents/plugins/plugins/ravel/skills/ravel/bin/ravel_${os}_${arch}"
  if [ -x "$checkout_binary" ]; then
    exec "$checkout_binary" "$@"
  fi
done

echo "ravel: no compatible bundled binary found; update or reinstall Ravel with consent" >&2
exit 1
