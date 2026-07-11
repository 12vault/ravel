#!/bin/sh
set -eu

repo="${RAVEL_REPO:-12vault/ravel}"
version="${RAVEL_VERSION:-latest}"
install_dir="${RAVEL_INSTALL_DIR:-$HOME/.local/bin}"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "ravel: unsupported operating system" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "ravel: unsupported architecture" >&2; exit 1 ;;
esac

if [ "$version" = latest ]; then
  base="https://github.com/$repo/releases/latest/download"
else
  base="https://github.com/$repo/releases/download/$version"
fi
asset="ravel_${os}_${arch}.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

curl --fail --location --proto '=https' --tlsv1.2 "$base/$asset" --output "$tmp/$asset"
curl --fail --location --proto '=https' --tlsv1.2 "$base/checksums.txt" --output "$tmp/checksums.txt"
(cd "$tmp" && grep "  $asset\$" checksums.txt | shasum -a 256 -c -)
tar -xzf "$tmp/$asset" -C "$tmp" ravel
mkdir -p "$install_dir"
install -m 0755 "$tmp/ravel" "$install_dir/ravel"
echo "Installed ravel to $install_dir/ravel"

case ":${PATH:-}:" in
  *":$install_dir:"*) ;;
  *)
    echo ""
    echo "ravel: $install_dir is not on PATH." >&2
    echo "Run this now:" >&2
    printf '  export PATH="%s:$PATH"\n' "$install_dir" >&2
    case "${SHELL:-}" in
      */zsh) profile="$HOME/.zshrc" ;;
      */bash) profile="$HOME/.bashrc" ;;
      *) profile="your shell profile" ;;
    esac
    echo "Then add the same line to $profile and open a new terminal." >&2
    ;;
esac
