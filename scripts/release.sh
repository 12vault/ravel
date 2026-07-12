#!/bin/sh
set -eu
if [ "$#" -ne 1 ]; then
  echo "usage: scripts/release.sh <semver>" >&2
  exit 2
fi
root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$root"
python3 scripts/release.py "$1"
gofmt -w internal/cli/commands.go
go test ./...
go vet ./...
grammar_tags="$(cat scripts/grammar_tags.txt)"
go test -tags "$grammar_tags" ./internal/lang/treeanalyzer ./internal/build
python3 scripts/release.py "$1" --check
python3 scripts/test_release.py
git diff --check
claude plugin validate .
echo "Release files are synchronized. Review, commit, then push tag v${1#v}."
