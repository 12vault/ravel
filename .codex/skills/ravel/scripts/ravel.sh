#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
required_version=$(sed -n '1{s/^v//;p;}' "$script_dir/../VERSION" 2>/dev/null || true)

case "$(uname -s)" in Darwin) os=darwin ;; Linux) os=linux ;; *) echo "ravel: unsupported operating system" >&2; exit 1 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) echo "ravel: unsupported architecture" >&2; exit 1 ;; esac

bundled="$script_dir/../bin/ravel_${os}_${arch}"
if [ ! -x "$bundled" ]; then
  bundled=""
  # Source skills avoid a third binary copy. Probe the synchronized marketplace
  # package at both valid checkout depths.
  for relative_root in ../../.. ../../../..; do
    checkout_root=$(CDPATH= cd -- "$script_dir/$relative_root" 2>/dev/null && pwd || true)
    [ -n "$checkout_root" ] || continue
    candidate="$checkout_root/.agents/plugins/plugins/ravel/skills/ravel/bin/ravel_${os}_${arch}"
    if [ -x "$candidate" ]; then bundled=$candidate; break; fi
  done
fi

global=$(command -v ravel 2>/dev/null || true)
case "$global" in "$0"|"$script_dir/ravel"|"$script_dir/ravel.sh") global="" ;; esac

cli_version() {
  "$1" version 2>/dev/null | sed -n '1{s/^ravel v//;s/^v//;p;}'
}

# Exit successfully only when $1 is lower than $2 according to SemVer 2.0.
semver_lt() {
  awk -v left="$1" -v right="$2" '
    function parse(v, core, pre, a) {
      sub(/^v/, "", v); sub(/\+.*/, "", v)
      pre = ""; if (index(v, "-")) { pre = substr(v, index(v, "-") + 1); v = substr(v, 1, index(v, "-") - 1) }
      split(v, a, "."); core[1] = a[1] + 0; core[2] = a[2] + 0; core[3] = a[3] + 0; core[4] = pre
    }
    function numeric(s) { return s ~ /^[0-9]+$/ }
    BEGIN {
      parse(left, l); parse(right, r)
      for (i = 1; i <= 3; i++) { if (l[i] < r[i]) exit 0; if (l[i] > r[i]) exit 1 }
      if (l[4] == r[4]) exit 1
      if (l[4] == "") exit 1
      if (r[4] == "") exit 0
      nl = split(l[4], la, "."); nr = split(r[4], ra, "."); n = nl < nr ? nl : nr
      for (i = 1; i <= n; i++) {
        if (la[i] == ra[i]) continue
        ln = numeric(la[i]); rn = numeric(ra[i])
        if (ln && rn) exit ((la[i] + 0) < (ra[i] + 0) ? 0 : 1)
        if (ln != rn) exit (ln ? 0 : 1)
        exit (la[i] < ra[i] ? 0 : 1)
      }
      exit (nl < nr ? 0 : 1)
    }'
}

global_version=""
[ -z "$global" ] || global_version=$(cli_version "$global" || true)

selected=$global
using_bundle=false
if [ -n "$bundled" ]; then
  # The bundle is the exact binary paired with this skill. Prefer it for equal
  # versions too; only a strictly newer global CLI supersedes it.
  if [ -z "$global_version" ] || { [ -n "$required_version" ] && ! semver_lt "$required_version" "$global_version"; }; then
    selected=$bundled
    using_bundle=true
  fi
fi

if [ -z "$selected" ]; then
  echo "ravel: no compatible CLI found; update or reinstall Ravel with consent" >&2
  exit 1
fi

# The skill invokes `version` once during bootstrap. Report the mismatch there,
# without repeating it for every subsequent tool call or making a network call.
if [ "${1:-}" = version ] && [ -n "$global_version" ] && [ -n "$required_version" ] && semver_lt "$global_version" "$required_version"; then
  echo "Your global Ravel is v$global_version; this skill requires v$required_version." >&2
  if $using_bundle; then
    echo "Using the bundled v$required_version binary for this task." >&2
  else
    echo "No compatible bundled binary is available; continuing with the global CLI." >&2
  fi
  echo "To update globally: ravel self-update --platforms codex" >&2
fi

exec "$selected" "$@"
