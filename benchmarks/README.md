# Ravel benchmarks

See [`RESULTS.md`](RESULTS.md) for the running human-readable Ravel vs Graphify results log.

## Real repositories used by the language suite

These comparisons run against first-party source from pinned, public project repositories. The repository owners do not endorse the benchmark; “official repository” means the project's own upstream repository, not an official project metric.

| Language | Real upstream repository | Pinned revision | Source files | Eligible exact-gold cases |
| --- | --- | --- | ---: | ---: |
| TypeScript/TSX | [`pingdotgg/t3code`](https://github.com/pingdotgg/t3code) | `2a33a18716854b8d07378008cf3101ad999209ae` | 1,952 first-party files | 487 |
| Swift | [`ghostty-org/ghostty`](https://github.com/ghostty-org/ghostty) | `73534c4680a809398b396c94ac7f12fcccb7963d` | 188 files under `macos/` | 455 |
| Rust | [`BurntSushi/ripgrep`](https://github.com/BurntSushi/ripgrep) | `3a612f88b805e14aef45bfa43e25a54abc6297fc` (`15.0.0`) | 100 | 1,174 |
| C | [`libgit2/libgit2`](https://github.com/libgit2/libgit2) | `1affb8b19346c4f90e163a9a0364959ff1410f64` (`v1.9.3`) | 473 | 1,167 |
| C++ | [`nlohmann/json`](https://github.com/nlohmann/json) | `55f93686c01528224f448c19128836e7df245f72` (`v3.12.0`) | 46 | 242 |

The TypeScript row excludes T3 Code's checked-in `.repos/` fixture/vendor tree and includes only tracked `.ts`, `.tsx`, `.mts`, and `.cts` files under `apps/`, `infra/`, `oxlint-plugin-t3code/`, `packages/`, and `scripts/`. The other rows likewise document their source-root filters below. Every manifest records the full revision, selected-file count, line count, and a SHA-256 fingerprint of the selected source bytes.

Dataset adapters are a separate category. CrossCodeEval, CodeSearchNet, RepoBench, and Real-FIM materialize published candidate snippets or incomplete files; they are not presented as full upstream-repository runs. ContextBench does use real pinned repositories, but its 104 Go issue cases span multiple upstream projects rather than one common project corpus.

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
  --gate benchmarks/self-quality-gate.json \
  --dataset-revision ravel-repository-v2 --graph-revision <commit>
ravel benchmark --dataset benchmarks/ravel-retrieval.jsonl \
  --retriever flat --top-k 25 \
  --dataset-revision ravel-repository-v1 --graph-revision <commit>
```

Benchmark defaults come from the same `.reporavel.yaml` retrieval section as `ravel context`; explicit flags are recorded overrides. A quality gate is a strict JSON file containing case-count and metric thresholds. When `requireFreshExpectations` is true, Ravel rejects expected node or edge IDs that are absent from the current graph before scoring. Threshold failures still write the result report, attach a `qualityGate` result, and return a non-zero status.

The CI corpus adds 54 relationship questions across exact revisions of chi (Go), Express (JavaScript), and Click (Python). Validate its manifest without network access:

```sh
python3 benchmarks/run_external_quality.py --check
```

Run the full pinned suite with a locally built Ravel binary:

```sh
go build -o /tmp/ravel ./cmd/ravel
python3 benchmarks/run_external_quality.py \
  --ravel /tmp/ravel --workspace /tmp/ravel-external-quality
```

The runner audits each checkout before building its graph, enforces at least 50 total cases, runs the shared quality gate independently for every repository, and leaves raw reports under the workspace `results/` directory. The repositories, full commit SHAs, datasets, and retrieval settings live in `external/suite.json`.

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
- Optional quality-gate status, failure reasons, and gate configuration SHA-256.

The estimate conservatively uses three UTF-8 bytes per token for the compact text payload. A `--json` envelope adds field-name and formatting overhead and is not part of that estimate.

## Quality datasets

`datasets.json` defines the implemented repository-question contract. The checked-in external suite uses pinned public repository revisions; custom dataset names are accepted after the caller converts their corpus and questions into evidence-tagged Ravel graphs plus the common JSONL format. Ravel does not ship or claim native LOCOMO/LongMemEval corpus adapters, download external datasets outside the explicit suite runner, or call a model/judge. Case isolation and adjudication remain the benchmark author's responsibility; the optional answer ledger records their resulting quality and cost measurements reproducibly.

Pass `--dataset-revision <revision>` and `--adapter-version <version>` for publishable runs. Do not compare scores produced with different graphs, datasets, retrieval settings, or model settings.

### RepoBench 10,000-case compatibility run

`run_repobench_10k.py` adapts the CC-BY-4.0 RepoBench v1.1 `cross_file_first` splits into 5,000 Python and 5,000 Java retrieval cases. Each case is an isolated miniature repository containing the benchmark's candidate snippets and incomplete target file. Ravel and Graphify receive the same corpus and query; the adapter scores retrieval of the gold cross-file identifier or path, reciprocal rank, context size, latency, truncation, and per-case build time. It does not invoke a model or claim final-answer correctness.

The runner defaults to `--ravel-profile broad` for a retrieval-shape comparison with Graphify: up to 20 lexical seeds, depth 3, effectively unpruned traversal on these small fragment graphs, and the explicit candidate-shortlist output profile. Use `--ravel-profile compact` to measure Ravel's normal balanced agent-context defaults instead. Ravel's raw records preserve token-component, deduplication, shortlist-selection, and omitted-explanation accounting; summaries report p99 build/query latency and aggregate those Ravel-specific fields. `truncationRate` measures hard output or traversal limits, while `shortlistSelectionRate` reports cases where lower-ranked discovery alternatives were intentionally left outside the output shortlist. The selected profile and exact flags are recorded in `run-config.json` and `summary.json`; a workspace cannot resume under different retrieval settings.

Download the official Python and Java Parquet shards separately, install PyArrow in an isolated environment, then prepare a stable manifest:

```sh
python benchmarks/run_repobench_10k.py prepare \
  --data-root /tmp/repobench-10k-data \
  --output /tmp/repobench-10k-manifest
python benchmarks/run_repobench_10k.py check \
  --manifest /tmp/repobench-10k-manifest/manifest.json
```

Run a smoke sample before starting or resuming all 10,000 cases:

```sh
python benchmarks/run_repobench_10k.py run \
  --manifest /tmp/repobench-10k-manifest/manifest.json \
  --data-root /tmp/repobench-10k-data \
  --workspace /tmp/repobench-ravel-vs-graphify \
  --ravel /path/to/ravel --graphify graphify --limit 100
python benchmarks/run_repobench_10k.py run \
  --manifest /tmp/repobench-10k-manifest/manifest.json \
  --data-root /tmp/repobench-10k-data \
  --workspace /tmp/repobench-ravel-vs-graphify \
  --ravel /path/to/ravel --graphify graphify
```

The append-only `results.jsonl` is the raw resumable record. `summary.json` records aggregate scores, versions, hashes, and platform metadata. Do not combine the resulting deterministic retrieval score with SWE-QA-Pro's LLM-judged answer score.

### TypeScript and Go comparisons

`run_t3code_typescript.py` runs the real-repository TypeScript comparison on the pinned T3 Code revision in the table above. It derives all 487 eligible questions from JSDoc attached to named declarations, removes JSDoc from both tool corpora without moving source lines, redacts the exact declaration symbol from each query, and scores the exact path, symbol, and declaration line. The tools receive separate byte-identical corpora, build twice in reversed order, and alternate query order by stable case key. This is a retrieval compatibility benchmark, not an official T3 Code metric or an LLM answer-quality test.

```sh
git clone https://github.com/pingdotgg/t3code.git /tmp/t3code
git -C /tmp/t3code checkout 2a33a18716854b8d07378008cf3101ad999209ae
python3 benchmarks/run_t3code_typescript.py prepare \
  --repository /tmp/t3code --output /tmp/t3code-typescript-manifest
python3 benchmarks/run_t3code_typescript.py check \
  --manifest /tmp/t3code-typescript-manifest/manifest.json
python3 benchmarks/run_t3code_typescript.py run \
  --manifest /tmp/t3code-typescript-manifest/manifest.json \
  --repository /tmp/t3code --workspace /tmp/t3code-typescript-ravel-vs-graphify \
  --ravel /path/to/ravel --graphify graphify --workers 2
```

Use a fresh workspace and omit `--limit` for all 487 cases. The manifest explicitly excludes `.repos/`; adding those checked-in third-party fixtures would turn the result into a mixed-corpus benchmark and inflate the apparent T3 Code source count.

The runner now uses `ravel context-batch` by default. It starts one fixed-snapshot JSONL process per worker, loads the graph and reusable query index once, and records graph-load/index-build startup separately from warm per-query retrieval. Use `--ravel-query-mode process` only when reproducing the old one-process-per-question baseline. Because Graphify still uses one process per question, the new Ravel and Graphify latency fields have different semantics and must not be presented as a direct speed win; quality and payload remain paired comparisons.

The corrected 487-case persistent run is recorded in [`results/t3code-typescript-ravel-context-batch-working-tree-vs-graphify-0.9.12-2026-07-17.json`](results/t3code-typescript-ravel-context-batch-working-tree-vs-graphify-0.9.12-2026-07-17.json), with adjacent execution metadata, run config, raw JSONL results, build metadata, and provenance. The two original one-shot diagnostics remain as [`run 1`](results/t3code-typescript-ravel-multilang-working-tree-vs-graphify-0.9.12-2026-07-17-run1.json) and [`run 2`](results/t3code-typescript-ravel-multilang-working-tree-vs-graphify-0.9.12-2026-07-17-run2.json). All use dirty working-tree Ravel binaries rather than the published v0.2.5 release. Graphify's multi-file AST worker may require process permissions unavailable in a restricted sandbox; keep that environment limitation separate from benchmark scores.

The latest TypeScript extraction development check is documented in [`RESULTS.md`](RESULTS.md#2026-07-17-t3-code-typescript-extraction-development-verification). It reused the same pinned, documentation-stripped corpus and all 487 questions, but reran only Ravel; the Graphify values are the archived comparison above, not a new paired latency run. Syntax-backed module bindings and narrow partial recovery for valid declarations represented by Tree-sitter `ERROR` nodes raised Ravel declaration coverage from 334/487 (68.58%) to 478/487 (98.15%) and exact retrieval from 50/487 (10.27%) to 69/487 (14.17%). MRR rose from 0.0406 to 0.0520 while mean payload stayed near 1,893 tokens.

Function-local constants remain intentionally outside the normal declaration graph. An experimental all-local mode reached 487/487 coverage but added about 26,000 nodes, did not improve the 135-case `const`/`let` exact-hit count, and raised that subset's warm mean/p95/max query latency to 4.256/7.432/16.255 seconds. Do not present the 100%-coverage experiment as the production result. The retained implementation covers 478/487 declarations and preserves the smaller graph; its later Ravel-only timing was not collected under the same machine state as the archived paired run.

The later query-ranking and adjacency-cache verification is documented in [`RESULTS.md`](RESULTS.md#2026-07-17-t3-code-retrieval-ranking-and-adjacency-verification) and its [`machine-readable summary`](results/t3code-typescript-ravel-query-adjacency-working-tree-2026-07-17.json). On the same 487 cases, cached adjacency preserved all 183 exact hits, MRR, and payload measurements while a 1,000-node microbenchmark used 34.1% less retrieval time, 39.9% fewer allocated bytes, and 53.1% fewer allocations. This was a Ravel-only development verification; Graphify was not rerun.

`run_crosscodeeval_typescript.py` adapts the 3,356-case CrossCodeEval TypeScript release. Because CrossCodeEval does not publish its original repositories or explicit gold retrieval spans, this is deliberately labeled a compatibility benchmark rather than the official model-completion metric. The adapter joins all six published retrieval files by `task_id`, materializes the deduplicated candidate chunks as the same miniature repository for both tools, and derives scorable gold APIs from the hidden line's referenced imports or cross-file definitions. The current official release yields 3,122 scorable cases; source, payload, manifest, and result hashes make that derivation reproducible.

```sh
python3 benchmarks/run_crosscodeeval_typescript.py prepare \
  --data-root /tmp/crosscodeeval_data \
  --output /tmp/crosscodeeval-typescript-manifest
python3 benchmarks/run_crosscodeeval_typescript.py check \
  --manifest /tmp/crosscodeeval-typescript-manifest/manifest.json
python3 benchmarks/run_crosscodeeval_typescript.py run \
  --manifest /tmp/crosscodeeval-typescript-manifest/manifest.json \
  --workspace /tmp/crosscodeeval-typescript-ravel-vs-graphify \
  --ravel /path/to/ravel --graphify graphify --limit 100
```

Remove `--limit 100` to run or resume all scorable cases. Raw records report gold-identifier recall, hit rate, reciprocal rank, tokens, truncation, build latency, and query p95/p99 for both tools. The candidate union includes CrossCodeEval's oracle retrieval views, although neither tool receives their labels, scores, or ordering. This therefore measures reranking and graph traversal over the published candidate pool; it does not claim answer correctness or full-repository retrieval quality.

`run_contextbench.py` uses ContextBench's human-labeled repository file and line spans. It checks out every exact public commit, builds each tool's graph once per case revision, sends the same issue statement and token budget to both tools, and reports file/span precision and recall, MRR, tokens, truncation, and build/query tail latency. Both tools receive the same checkout, but retain their native corpus coverage: Graphify uses its code-only extractor while Ravel also accepts its supported repository documents. Preparing Parquet input requires PyArrow; fetching the pinned repositories is an explicit network step, after which `run --offline` performs no repository fetches.

```sh
python benchmarks/run_contextbench.py prepare \
  --parquet /tmp/ContextBench/full.parquet \
  --output /tmp/contextbench-go-manifest --language go
python benchmarks/run_contextbench.py fetch \
  --manifest /tmp/contextbench-go-manifest/manifest.json \
  --cache /tmp/contextbench-repository-cache
python benchmarks/run_contextbench.py run \
  --manifest /tmp/contextbench-go-manifest/manifest.json \
  --cache /tmp/contextbench-repository-cache \
  --workspace /tmp/contextbench-go-ravel-vs-graphify \
  --ravel /path/to/ravel --graphify graphify --offline
```

ContextBench currently supplies 104 Go cases. Its gold spans may overlap; the adapter merges intervals before scoring so duplicated annotations do not inflate recall or precision. Graph build measurements are aggregated once per unique pinned revision rather than once per question.

`run_codesearchnet_go.py` selects a deterministic 1,005-case slice from the 14,291-row CodeSearchNet Go test split. It builds one shared corpus containing the 1,005 paired functions, removes documentation from the corpus, redacts the exact gold symbol from each natural-language query, and scores retrieval of the exact paired function file. This is not the official CodeSearchNet challenge metric: the paired documentation/function relation is proxy gold and other functions may also satisfy a query. The source is the public CodeSearchNet corpus; the reproducible Parquet transport currently has SHA-256 `bedf275a31459a8ecf5bcaadfeec7f1b6971f07735f3b8a0ebd4ed4648b67af3`.

```sh
python-with-pyarrow benchmarks/run_codesearchnet_go.py prepare \
  --parquet /tmp/codesearchnet-go-test.parquet \
  --output /tmp/codesearchnet-go-manifest
python3 benchmarks/run_codesearchnet_go.py check \
  --manifest /tmp/codesearchnet-go-manifest/manifest.json
python-with-pyarrow benchmarks/run_codesearchnet_go.py run \
  --manifest /tmp/codesearchnet-go-manifest/manifest.json \
  --parquet /tmp/codesearchnet-go-test.parquet \
  --workspace /tmp/codesearchnet-go-ravel-vs-graphify \
  --ravel /path/to/ravel --graphify graphify --limit 20
```

Use a fresh workspace and remove `--limit 20` for all 1,005 cases because the candidate corpus is shared and therefore changes with the limit. PyArrow is needed only for `prepare` and `run`; `check` remains dependency-free. The adapter records source, selection, manifest, executable, configuration, and result hashes, and rejects a run if either tool produces an empty graph. Graphify's multi-file AST worker must be allowed to create local process-synchronization primitives; a restricted sandbox can otherwise fail extraction with `Operation not permitted`.

`run_ghostty_swift.py` runs a real-repository Swift comparison on a pinned Ghostty checkout. It copies every tracked `.swift` file under `macos/` into one shared corpus, blanks all `///` documentation while preserving line numbers, redacts the exact declaration symbol from each documentation-derived query, and scores the exact path, symbol, and declaration line. The adapter uses every eligible named class, struct, enum, protocol, actor, or function instead of padding the suite with weak undocumented cases. This is a retrieval compatibility benchmark, not an official Ghostty or Swift metric, and the documentation/declaration relation is proxy gold.

```sh
git clone https://github.com/ghostty-org/ghostty /tmp/ghostty
git -C /tmp/ghostty checkout 73534c4680a809398b396c94ac7f12fcccb7963d
python3 benchmarks/run_ghostty_swift.py prepare \
  --repository /tmp/ghostty --output /tmp/ghostty-swift-manifest
python3 benchmarks/run_ghostty_swift.py check \
  --manifest /tmp/ghostty-swift-manifest/manifest.json
python3 benchmarks/run_ghostty_swift.py run \
  --manifest /tmp/ghostty-swift-manifest/manifest.json \
  --repository /tmp/ghostty --workspace /tmp/ghostty-swift-ravel-vs-graphify \
  --ravel /path/to/ravel --graphify graphify --limit 20
```

Use a fresh workspace and remove `--limit 20` for every eligible case. Unlike the snippet-based Go benchmark, smoke and full runs use the same complete Swift corpus. The manifest fingerprints the exact Ghostty commit and all tracked Swift source bytes; the runner rejects source drift, changed executables or settings, missing graphs, and empty graphs.

The post-extractor-upgrade run's full metrics, per-kind retrieval, declaration coverage, executable and artifact hashes, and limitations are recorded in [`results/swift-ghostty-ravel-multilang-working-tree-vs-graphify-0.9.12-2026-07-16.json`](results/swift-ghostty-ravel-multilang-working-tree-vs-graphify-0.9.12-2026-07-16.json). The original published-v0.2.5 baseline remains in [`results/swift-ghostty-ravel-v0.2.5-vs-graphify-0.9.12-2026-07-16.json`](results/swift-ghostty-ravel-v0.2.5-vs-graphify-0.9.12-2026-07-16.json).

`run_ripgrep_rust.py` applies the same documentation-to-declaration design to a pinned ripgrep checkout. It uses every eligible documented Rust function, struct, enum, trait, constant, static, and type alias. Both tools receive all tracked `.rs` files with `///` rustdoc blanked without changing line numbers. Exact symbols are redacted and fenced rustdoc examples are omitted from queries; scoring requires the exact declaration path, symbol, and source line.

```sh
git clone --depth 1 --branch 15.0.0 https://github.com/BurntSushi/ripgrep.git /tmp/ripgrep
python3 benchmarks/run_ripgrep_rust.py prepare \
  --repository /tmp/ripgrep --output /tmp/ripgrep-rust-manifest
python3 benchmarks/run_ripgrep_rust.py check \
  --manifest /tmp/ripgrep-rust-manifest/manifest.json
python3 benchmarks/run_ripgrep_rust.py run \
  --manifest /tmp/ripgrep-rust-manifest/manifest.json \
  --repository /tmp/ripgrep --workspace /tmp/ripgrep-rust-ravel-vs-graphify \
  --ravel /path/to/ravel --graphify graphify --limit 20
```

Use a fresh workspace and remove `--limit 20` for all 1,174 cases. Smoke and full runs build the same complete Rust corpus. The adapter fingerprints the exact commit and every tracked Rust source byte, rejects changed executables or settings on resume, and rejects empty graphs. The 2026-07-16 full result is recorded in [`results/rust-ripgrep-ravel-multilang-working-tree-vs-graphify-0.9.12-2026-07-16.json`](results/rust-ripgrep-ravel-multilang-working-tree-vs-graphify-0.9.12-2026-07-16.json).

`run_c_family.py` benchmarks C on pinned libgit2 sources and C++ on pinned nlohmann/json headers. It derives exact documentation-attached declarations, blanks Doxygen comments without changing line numbers, redacts the gold symbol, and reports both exact path/symbol/line retrieval and a secondary top-20 same-symbol-anywhere metric. The secondary metric matters for C because public documentation commonly sits on a header prototype while graph tools may retain only the implementation definition.

```sh
git clone --depth 1 --branch v1.9.3 https://github.com/libgit2/libgit2.git /tmp/libgit2
python3 benchmarks/run_c_family.py prepare --language c \
  --repository /tmp/libgit2 --output /tmp/libgit2-c-manifest
python3 benchmarks/run_c_family.py check \
  --manifest /tmp/libgit2-c-manifest/manifest.json
python3 benchmarks/run_c_family.py run \
  --manifest /tmp/libgit2-c-manifest/manifest.json \
  --repository /tmp/libgit2 --workspace /tmp/libgit2-c-ravel-vs-graphify \
  --ravel /path/to/ravel --graphify graphify --limit 20

git clone --depth 1 --branch v3.12.0 https://github.com/nlohmann/json.git /tmp/nlohmann-json
python3 benchmarks/run_c_family.py prepare --language cpp \
  --repository /tmp/nlohmann-json --output /tmp/nlohmann-json-cpp-manifest
python3 benchmarks/run_c_family.py check \
  --manifest /tmp/nlohmann-json-cpp-manifest/manifest.json
python3 benchmarks/run_c_family.py run \
  --manifest /tmp/nlohmann-json-cpp-manifest/manifest.json \
  --repository /tmp/nlohmann-json --workspace /tmp/nlohmann-json-cpp-ravel-vs-graphify \
  --ravel /path/to/ravel --graphify graphify --limit 20
```

Use fresh workspaces and remove `--limit 20` for all 1,167 C cases and 242 C++ cases. Each run creates separate byte-identical corpora for the tools, verifies that neither corpus changed, rejects source paths outside the assigned corpus, reverses build order across two trials, and alternates query order by stable case key. Full results are recorded in [`results/c-cpp-libgit2-nlohmann-ravel-working-tree-vs-graphify-0.9.12-2026-07-16.json`](results/c-cpp-libgit2-nlohmann-ravel-working-tree-vs-graphify-0.9.12-2026-07-16.json).

`run_real_fim_scale.py` adds a large stability and tail-latency pass over all 5,769 TypeScript and Go cases in the official Real-FIM-Eval Add/Edit archives (3,182 TypeScript and 2,587 Go). Real-FIM-Eval does not provide gold retrieval spans, so this adapter must not be used to claim answer or retrieval correctness. It materializes the same pre-change file for both tools, fingerprints but never exposes the hidden canonical solution, and reports errors, non-empty output, target-file-return rate, payload, truncation, and build/query p50/p95/p99/max.

```sh
python3 benchmarks/run_real_fim_scale.py prepare \
  --add /tmp/real-fim-eval-add.jsonl.gz \
  --edit /tmp/real-fim-eval-edit.jsonl.gz \
  --output /tmp/real-fim-typescript-go-manifest
python3 benchmarks/run_real_fim_scale.py check \
  --manifest /tmp/real-fim-typescript-go-manifest/manifest.json
python3 benchmarks/run_real_fim_scale.py run \
  --manifest /tmp/real-fim-typescript-go-manifest/manifest.json \
  --add /tmp/real-fim-eval-add.jsonl.gz \
  --edit /tmp/real-fim-eval-edit.jsonl.gz \
  --workspace /tmp/real-fim-typescript-go-ravel-vs-graphify \
  --ravel /path/to/ravel --graphify graphify --limit 20
```

Remove `--limit 20` to run or resume all 5,769 scale cases. Combined with the 3,122 scorable TypeScript CrossCodeEval cases, 104 gold-span Go ContextBench cases, and 1,005 paired-function CodeSearchNet Go cases above, this exercises exactly 10,000 benchmark cases: 6,304 TypeScript and 3,696 Go. The first 4,231 have explicit or deterministic retrieval gold; Real-FIM's 5,769 cases are scale/parser checks only.

The isolated 2026-07-16 TypeScript and Go summaries, executable hashes, pairwise counts, and limitations are recorded in [`results/typescript-go-ravel-v0.2.5-vs-graphify-0.9.12-2026-07-16.json`](results/typescript-go-ravel-v0.2.5-vs-graphify-0.9.12-2026-07-16.json).

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

## Historical ten-question T3 Code payload snapshot

This older 2026-07-12 snapshot used an Apple M1 Pro (`darwin/arm64`), Ravel v0.2.5, Graphify 0.9.12, and the repository then recorded as `t3tools/t3code` at commit `c1ec1915fc16f3dc1ec5d47d9a97f6210a574526`. It is retained for payload/build history; the 487-case pinned `pingdotgg/t3code` suite above is the answer-quality retrieval comparison.

**Bottom line:** there is no overall winner in this snapshot. Graphify built its graph faster. Ravel reclustered faster and returned a smaller context payload. Graph size is not a quality score, and this run did not test answer correctness.

| Measurement | Ravel | Graphify | Winner | Why |
| --- | ---: | ---: | --- | --- |
| Cold graph build | 166.54 s | 90.52 s | **Graphify** | 76.02 s faster (45.6% less time), but the tools scanned different input scopes |
| Resulting graph | 296,334 nodes / 366,815 edges | 49,517 nodes / 104,160 clustered edges | **No winner** | Tool-native schemas and extractors produced non-equivalent coverage; more nodes or edges does not prove better retrieval |
| Tool-native reclustering | 13.23 s | 19.71 s | **Ravel** | 6.48 s faster (32.9% less time) on each tool's own graph |
| Compact context, 10 questions, 800-token budget | 6,821 estimated tokens | 9,009 estimated tokens | **Ravel** | 2,188 fewer estimated tokens (24.3% smaller) under the same questions and token setting |

The payload result means Ravel returned less text for these ten questions. It does not show whether that text produced better answers. The comparison uses each tool's native graph schema and extractors, so graph coverage is not equivalent. Ravel scanned code and supported repository documents, graphified 5,065 files, and skipped 565 accepted files with no graph content. Graphify used `--code-only`, reported 805 source files with no nodes, and skipped 11 SQL files because its optional SQL parser was unavailable. Its reclustering used the installed NetworkX 3.6.1 Louvain fallback. Context size uses the same checked-in questions and `ceil(UTF-8 bytes / 3)` estimate; it does not measure answer correctness or model billing.

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
