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

## Recorded T3 Code comparison

The current 2026-07-12 snapshot used an Apple M1 Pro (`darwin/arm64`), Ravel v0.2.5, Graphify 0.9.12, and `t3tools/t3code` commit `c1ec1915fc16f3dc1ec5d47d9a97f6210a574526`.

| Measurement | Ravel | Graphify |
| --- | ---: | ---: |
| Cold graph build | 166.54 s | 90.52 s |
| Resulting graph nodes | 296,334 | 49,517 |
| Resulting graph edges | 366,815 | 104,160 clustered edges |
| Tool-native reclustering | 13.23 s | 19.71 s |
| Compact context, 10 questions, 800-token budget | 6,821 estimated tokens | 9,009 estimated tokens |

The comparison uses each tool's native graph schema and extractors, so graph coverage is not equivalent. Ravel scanned code and supported repository documents, graphified 5,065 files, and skipped 565 accepted files with no graph content. Graphify used `--code-only`, reported 805 source files with no nodes, and skipped 11 SQL files because its optional SQL parser was unavailable. Its reclustering used the installed NetworkX 3.6.1 Louvain fallback. Context size uses the same checked-in questions and `ceil(UTF-8 bytes / 3)` estimate; it does not measure answer correctness or model billing.

Reproduce the context payload measurement after building both graphs:

```sh
python3 benchmarks/compare_context_payloads.py \
  --ravel ravel --ravel-graph /tmp/t3code-ravel \
  --graphify graphify --graphify-graph /tmp/t3code-graphify/graphify-out/graph.json \
  --questions benchmarks/t3code-context-questions.json --token-budget 800 \
  --repository t3tools/t3code --revision c1ec1915fc16f3dc1ec5d47d9a97f6210a574526 \
  --out /tmp/t3code-context-comparison.json
```

Raw records:

- [`results/t3code-ravel-v0.2.5-vs-graphify-0.9.12-2026-07-12.json`](results/t3code-ravel-v0.2.5-vs-graphify-0.9.12-2026-07-12.json)

The older self-repository retrieval and synthetic clustering snapshots remain under `benchmarks/results/` as historical records, but they are no longer used for the headline comparison.
