# Ravel benchmarks

Run the repeatable local suite with:

```sh
./benchmarks/run.sh
```

The suite reports graph build, pure-Go Tree-sitter polyglot analysis, and query throughput without network access. `go test ./...` also compiles and smoke-tests every benchmark.

Run retrieval-quality cases against a built graph:

```sh
ravel benchmark --graph .reporavel --dataset benchmarks/example.jsonl \
  --retriever context --token-budget 2000 --out benchmark-results.json
```

Use `--retriever flat` to measure the ranked lexical baseline without expansion. Context mode builds one reusable index, selects IDF/BM25F-ranked seeds, traverses the graph with BFS or DFS, and atomically budgets each discovered node with its explanatory edge. `--top-k` is the hard context-node cap; the token budget remains the primary output bound.

The checked-in ten-question relationship suite runs against Ravel's own graph:

```sh
ravel build .
ravel benchmark --dataset benchmarks/ravel-retrieval.jsonl \
  --retriever context --top-k 25 --token-budget 800 \
  --dataset-revision ravel-repository-v1 --graph-revision <commit>
ravel benchmark --dataset benchmarks/ravel-retrieval.jsonl \
  --retriever flat --top-k 25 \
  --dataset-revision ravel-repository-v1 --graph-revision <commit>
```

Benchmark defaults come from the same `.reporavel.yaml` retrieval section as `ravel context`; explicit flags are recorded overrides.

Each JSONL record requires `id`, `dataset`, `question`, and either `expectedNodeIds` or `expectedEvidence`. Evidence IDs may be node IDs or edge IDs. Dataset names may be `repository-questions`, `LOCOMO`, `LongMemEval`, or a custom suite.

To attach externally adjudicated final-answer quality, add `expectedKeyFacts` to dataset cases and pass a separate answer ledger:

```sh
ravel benchmark --graph .reporavel --dataset benchmarks/example.jsonl \
  --answers benchmarks/example-answers.jsonl --out benchmark-results.json
```

Each answer-ledger JSONL record uses the dataset case `id` plus `correct` and/or `keyFactsFound`; optional accounting fields are `inputTokens`, `outputTokens`, `toolTokens`, and `costUsd`, with `model`, `judge`, and `runId` provenance. Ravel validates facts against the case rubric and reports accuracy, mean key-fact coverage, total agent tokens, and total spend overall and per dataset. Partial ledgers are allowed and expose their scored-case denominator. The strict format intentionally has no raw-answer field, and Ravel never invokes a model or judge. Values in `example-answers.jsonl` are illustrative fixtures, not published model results.

Token fields must be non-overlapping: `totalAgentTokens` is their sum. If a provider already includes tool-call or tool-result payloads in its input/output counts, record those provider totals in `inputTokens`/`outputTokens` and leave `toolTokens` at zero. Record `costUsd` as the case's full externally measured spend.

Version 3 results report:

- Node recall, precision, and reciprocal rank.
- Evidence recall and precision across returned node and edge IDs.
- Estimated compact-text tokens, node and evidence recall per 1,000 tokens, and truncation rate.
- Mean, p50, and p95 query latency plus separate index-build time.
- Logical graph SHA-256/version/build time, graph and dataset revisions, dataset SHA-256, adapter version, Ravel version, Go version, OS, and architecture.
- Optional externally adjudicated accuracy, key-fact coverage, total agent tokens, total spend, per-case provenance, and answer-ledger SHA-256.

The estimate conservatively uses three UTF-8 bytes per token for the compact text payload. A `--json` envelope adds field-name and formatting overhead and is not part of that estimate.

## Quality datasets

`datasets.json` defines the implemented repository-question contract. Custom dataset names are accepted after the caller converts their corpus and questions into evidence-tagged Ravel graphs plus the common JSONL format. Ravel does not ship or claim native LOCOMO/LongMemEval corpus adapters, download external datasets, or call a model/judge. Case isolation and adjudication remain the benchmark author's responsibility; the optional answer ledger records their resulting quality and cost measurements reproducibly.

Pass `--dataset-revision <revision>` and `--adapter-version <version>` for publishable runs. Do not compare scores produced with different graphs, datasets, retrieval settings, or model settings.

## Optional Graphify compatibility comparison

When Graphify is installed locally, `compare_graphify.py` can run the same repository questions and token budget through both CLIs. Graphify's raw `extract --no-cluster` output must first be converted to its clustered node-link graph because its query command expects that form:

```sh
graphify extract . --code-only --no-cluster --max-workers 1 --out /tmp/graphify-ravel
graphify cluster-only /tmp/graphify-ravel \
  --graph /tmp/graphify-ravel/graphify-out/graph.json --no-label --no-viz
python3 benchmarks/compare_graphify.py \
  --ravel ravel --ravel-graph .reporavel \
  --graphify graphify --graphify-graph /tmp/graphify-ravel/graphify-out/graph.json \
  --dataset benchmarks/ravel-retrieval.jsonl --token-budget 800 \
  --out /tmp/ravel-vs-graphify.json
```

The adapter reports normalized expected symbol-name recall because Ravel and Graphify use incompatible node and edge ID schemes. It does not compare evidence recall, model answers, or judge scores and must not be presented as a universal quality ranking. Keep the raw graphs, tool versions, dataset, and output with any published result.
