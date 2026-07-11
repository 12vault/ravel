#!/bin/sh
set -eu
root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$root"
go test -run '^$' -bench . -benchmem ./internal/build ./internal/query
