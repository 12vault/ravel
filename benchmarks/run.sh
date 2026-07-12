#!/bin/sh
set -eu
root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$root"
go test -run '^$' -bench . -benchmem ./internal/build ./internal/lang/treeanalyzer ./internal/query

out=${RAVEL_BENCH_OUT:-${TMPDIR:-/tmp}/ravel-benchmarks-$$}
mkdir -p "$out"
go run ./cmd/ravel build --out "$out/graph" .
for retriever in context flat; do
  go run ./cmd/ravel benchmark \
    --graph "$out/graph" \
    --dataset benchmarks/ravel-retrieval.jsonl \
    --retriever "$retriever" \
    --top-k 25 \
    --token-budget 800 \
    --dataset-revision ravel-repository-v1 \
    --graph-revision "${RAVEL_GRAPH_REVISION:-working-tree}" \
    --out "$out/$retriever.json"
done
echo "Raw retrieval results: $out/context.json and $out/flat.json"
