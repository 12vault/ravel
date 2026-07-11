#!/bin/sh
set -eu

if command -v ravel >/dev/null 2>&1; then
  ravel version
  exit 0
fi

repo="${RAVEL_REPO:-12vault/ravel}"
install_dir="${RAVEL_INSTALL_DIR:-$HOME/.local/bin}"
case "$(uname -s)" in Darwin) os=darwin ;; Linux) os=linux ;; *) echo "ravel: unsupported operating system" >&2; exit 1 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) echo "ravel: unsupported architecture" >&2; exit 1 ;; esac
asset="ravel_${os}_${arch}.tar.gz"
base="https://github.com/$repo/releases/latest/download"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM
curl --fail --location --proto '=https' --tlsv1.2 "$base/$asset" --output "$tmp/$asset"
curl --fail --location --proto '=https' --tlsv1.2 "$base/checksums.txt" --output "$tmp/checksums.txt"
(cd "$tmp" && grep "  $asset\$" checksums.txt | shasum -a 256 -c -)
tar -xzf "$tmp/$asset" -C "$tmp" ravel
mkdir -p "$install_dir"
install -m 0755 "$tmp/ravel" "$install_dir/ravel"
"$install_dir/ravel" version
