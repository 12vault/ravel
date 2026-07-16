# Benchmark results log

This file is the human-readable record of Ravel vs Graphify runs. Raw, machine-readable results live in [`results/`](results/).

## 2026-07-17: TypeScript on T3 Code

Environment: Apple M1 Pro, macOS arm64, Ravel v0.2.5, Graphify 0.9.12, 2,000-token budget, Ravel broad retrieval profile. The shared corpus is the pinned upstream [`pingdotgg/t3code`](https://github.com/pingdotgg/t3code) revision `2a33a18716854b8d07378008cf3101ad999209ae`: 1,952 tracked first-party `.ts`, `.tsx`, `.mts`, and `.cts` files (513,548 lines) under `apps/`, `infra/`, `oxlint-plugin-t3code/`, `packages/`, and `scripts/`. T3 Code's checked-in `.repos/` fixture/vendor tree was excluded. JSDoc was blanked from both corpora without moving source lines, declaration symbols were redacted from 487 documentation-derived queries, and both tools received byte-identical source bytes.

| Measurement | Ravel | Graphify | Winner |
| --- | ---: | ---: | --- |
| Exact declaration retrieval | 50/487 (10.27%) | 20/487 (4.11%) | Ravel |
| MRR | 0.0406 | 0.0010 | Ravel |
| Declaration graph coverage | 334/487 (68.58%) | 451/487 (92.61%) | Graphify |
| Top-20 same-symbol retrieval | 84/487 (17.25%) | 100/487 (20.53%) | Graphify |
| Mean balanced build | 53.34 s | 32.70 s | Graphify |
| Mean query | 11,652 ms | 1,975 ms | Graphify |
| Mean payload | 1,892 tokens | 2,158 tokens | Ravel |
| Truncated output | 0.00% | 90.76% | Ravel |

Ravel wins the exact documentation-to-declaration retrieval metric (50 hits versus 20), MRR, payload size, and truncation. Graphify extracts more of the documented declarations (451 versus 334), finds more same-symbol results within its first 20 items, builds 1.63× faster, and queries 5.90× faster. The pairwise result was Ravel-only on 47 cases, Graphify-only on 17, both hit on 3, and both missed on 420. The extreme Ravel query tail (p95 22.67 s, p99 88.32 s, max 260.12 s) is a material performance issue on this large corpus even though its output is smaller and never truncated.

This is a retrieval-compatibility benchmark, not an official T3 Code metric and not an LLM answer-quality test; no model generated or judged answers. Full metrics, source and executable hashes, build-order trials, and run settings are in [`results/t3code-typescript-ravel-v0.2.5-vs-graphify-0.9.12-2026-07-17.json`](results/t3code-typescript-ravel-v0.2.5-vs-graphify-0.9.12-2026-07-17.json) and [`results/t3code-typescript-ravel-v0.2.5-vs-graphify-0.9.12-2026-07-17.run-config.json`](results/t3code-typescript-ravel-v0.2.5-vs-graphify-0.9.12-2026-07-17.run-config.json).

## 2026-07-16: C on libgit2 and C++ on nlohmann/json

Environment: Apple M1 Pro, macOS arm64, 2,000-token budget, Ravel broad retrieval profile. Each tool received a separate byte-identical corpus; corpus hashes were verified unchanged after building. Build order was reversed across two trials, and query order alternated deterministically by case.

### C: 1,167 libgit2 cases

| Measurement | Ravel | Graphify | Winner |
| --- | ---: | ---: | --- |
| Exact documented declaration | 13/1,167 (1.11%) | 4/1,167 (0.34%) | Ravel |
| Top-20 same symbol anywhere | 316/1,167 (27.08%) | 268/1,167 (22.96%) | Ravel |
| Same-symbol graph coverage | 1,109/1,167 (95.03%) | 1,149/1,167 (98.46%) | Graphify |
| Exact header-declaration coverage | 64/1,167 (5.48%) | 35/1,167 (3.00%) | Ravel |
| Mean balanced build | 4.93 s | 7.63 s | Ravel |
| Mean query | 1,167.49 ms | 535.63 ms | Graphify |
| Mean payload | 1,908 tokens | 2,041 tokens | Ravel |
| Truncated output | 0.00% | 87.57% | Ravel |

Both tools are weak at representing the documentation-attached C header prototype itself. This is not the same as missing the symbol entirely: 95.0% of gold symbols existed somewhere in Ravel's graph and 98.5% in Graphify's graph, usually as implementation definitions. Ravel returned more matching symbols in its first 20 items, while Graphify covered more symbols overall, ranked its hits better, and queried 2.18× faster.

### C++: 242 nlohmann/json cases

| Measurement | Ravel | Graphify | Winner |
| --- | ---: | ---: | --- |
| Exact documented declaration | 24/242 (9.92%) | 20/242 (8.26%) | Ravel |
| Top-20 same symbol anywhere | 66/242 (27.27%) | 114/242 (47.11%) | Graphify |
| Same-symbol graph coverage | 182/242 (75.21%) | 220/242 (90.91%) | Graphify |
| Exact declaration coverage | 106/242 (43.80%) | 63/242 (26.03%) | Ravel |
| Mean balanced build | 0.86 s | 1.15 s | Ravel |
| Mean query | 112.92 ms | 161.82 ms | Ravel |
| Mean payload | 1,903 tokens | 1,979 tokens | Ravel |
| Truncated output | 0.00% | 79.75% | Ravel |

The C++ verdict is split. Ravel wins exact documentation-site coverage, exact retrieval, build time, query time, payload, and truncation. Graphify is much stronger when any same-named declaration or implementation is accepted: 114 top-20 hits versus 66 and 90.9% graph symbol coverage versus 75.2%.

Across all 1,409 cases, the tools tied at 382 top-20 same-symbol hits. Graphify ranked those hits better and had more symbols somewhere in its graphs; Ravel had more exact documentation-site hits and exact declaration nodes. No LLM generated or judged answers.

Full metrics, order trials, isolation checks, hashes, and limitations: [`results/c-cpp-libgit2-nlohmann-ravel-working-tree-vs-graphify-0.9.12-2026-07-16.json`](results/c-cpp-libgit2-nlohmann-ravel-working-tree-vs-graphify-0.9.12-2026-07-16.json)

## 2026-07-16: Rust on ripgrep

Environment: Apple M1 Pro, macOS arm64, 2,000-token budget, Ravel broad retrieval profile. The shared corpus contains all 100 tracked Rust files from ripgrep 15.0.0 at commit `3a612f88b805e14aef45bfa43e25a54abc6297fc` (52,287 lines). All `///` rustdoc was blanked from the corpus with line numbers preserved, fenced examples were omitted from queries, and exact declaration symbols were redacted from 1,174 rustdoc-derived questions.

| Measurement | Ravel | Graphify | Winner |
| --- | ---: | ---: | --- |
| Exact declaration retrieval | 256/1,174 (21.81%) | 155/1,174 (13.20%) | Ravel |
| MRR | 0.0906 | 0.0038 | Ravel |
| Declaration graph coverage | 1,159/1,174 (98.72%) | 1,095/1,174 (93.27%) | Ravel |
| Function coverage | 959/959 | 895/959 | Ravel |
| Build time | 0.88 s | 3.05 s | Ravel |
| Mean query | 497.62 ms | 343.61 ms | Graphify |
| Query p99 | 650.72 ms | 425.05 ms | Graphify |
| Mean payload | 1,897 tokens | 2,050 tokens | Ravel |
| Truncated output | 0.00% | 84.67% | Ravel |

Ravel wins the Rust run overall: 101 more exact retrieval hits, 5.45 percentage points more declaration coverage, 64 more covered functions, 23.6× higher MRR, a 3.45× faster build, and 7.47% smaller payloads. Graphify's clear win is query speed: its mean query was 1.45× faster and its p99 was 34.7% lower.

By kind, Ravel retrieved more functions (221 versus 99) and enums (16 versus 3). Graphify retrieved more structs (52 versus 19) and the only retrieved trait. Both graphs missed the 15 eligible constants, statics, and type aliases. No LLM generated or judged final answers; these are rustdoc-to-declaration retrieval proxies.

Full metrics, per-kind counts, hashes, and limitations: [`results/rust-ripgrep-ravel-multilang-working-tree-vs-graphify-0.9.12-2026-07-16.json`](results/rust-ripgrep-ravel-multilang-working-tree-vs-graphify-0.9.12-2026-07-16.json)

## 2026-07-16: Swift on Ghostty

Environment: Apple M1 Pro, macOS arm64, 2,000-token budget, Ravel broad retrieval profile. The shared corpus contains all 188 tracked Swift files under Ghostty's `macos/` tree at commit `73534c4680a809398b396c94ac7f12fcccb7963d` (36,025 lines). All `///` documentation was blanked from the corpus with line numbers preserved, and exact symbols were redacted from 455 documentation-derived queries.

| Measurement | Ravel | Graphify | Winner |
| --- | ---: | ---: | --- |
| Exact declaration retrieval | 164/455 (36.04%) | 40/455 (8.79%) | Ravel |
| MRR | 0.1891 | 0.0033 | Ravel |
| Declaration graph coverage | 378/455 (83.08%) | 373/455 (81.98%) | Ravel |
| Function coverage | 241/299 | 237/299 | Ravel |
| Build time | 62.02 s | 3.28 s | Graphify |
| Mean query | 134.27 ms | 353.70 ms | Ravel |
| Query p99 | 174.80 ms | 408.99 ms | Ravel |
| Mean payload | 1,904 tokens | 2,066 tokens | Ravel |
| Truncated output | 0.00% | 89.89% | Ravel |

This rerun uses a working-tree Ravel build with structural named-declaration extraction for every advertised programming language and bounded, syntax-backed declaration recovery when a parser reaches its deadline. Ravel now wins overall Swift declaration coverage by 5 declarations, function coverage by 4 functions, retrieval by 164 hits to 40, ranking, query latency, payload size, and truncation. Graphify remains the clear build winner at 18.9× faster.

Against the published-v0.2.5 baseline below, Ravel coverage rose from 96 to 378 declarations, function coverage rose from 0 to 241, and recall rose from 9.45% to 36.04%. Its build became 30.0% faster, while mean query time grew from 39.18 ms to 134.27 ms with the larger graph. The new graph contains 912 declarations recovered from 38 stopped parses; each is explicitly marked `partial=true` and `parse_complete=false`, and incomplete calls, imports, and heritage are discarded.

No LLM generated or judged final answers; these are documentation-to-declaration retrieval proxies. The updated binary still reports `v0.2.5`, but its hash identifies a working-tree build rather than the published release.

Full updated metrics, per-kind counts, hashes, and limitations: [`results/swift-ghostty-ravel-multilang-working-tree-vs-graphify-0.9.12-2026-07-16.json`](results/swift-ghostty-ravel-multilang-working-tree-vs-graphify-0.9.12-2026-07-16.json)

Before the extractor upgrade, the published-v0.2.5 baseline covered 96/455 declarations, covered 0/299 functions, and retrieved 43/455 declarations: [`results/swift-ghostty-ravel-v0.2.5-vs-graphify-0.9.12-2026-07-16.json`](results/swift-ghostty-ravel-v0.2.5-vs-graphify-0.9.12-2026-07-16.json)

## 2026-07-16: TypeScript and Go

Environment: Apple M1 Pro, macOS arm64, 2,000-token budget, Ravel broad retrieval profile.

| Benchmark | Ravel | Graphify | Winner |
| --- | ---: | ---: | --- |
| TypeScript recall | 96.09% | 67.52% | Ravel |
| TypeScript MRR | 0.580 | 0.157 | Ravel |
| TypeScript mean build | 394 ms | 489 ms | Ravel |
| TypeScript mean query | 23 ms | 191 ms | Ravel |
| TypeScript mean payload | 1,523 tokens | 415 tokens | Graphify |
| Go gold-file hits | 39/104 | 0/104 | Ravel |
| Go mean build | 4.06 s | 0.75 s | Graphify |
| Go paired-function retrieval | 467/1,005 | 471/1,005 | Graphify |
| Go paired-function MRR | 0.294 | 0.024 | Ravel |

### What these runs mean

- TypeScript used 3,122 scorable CrossCodeEval compatibility cases. Ravel won recall, ranking, build time, and query time. Graphify returned a much smaller payload.
- Go used 104 ContextBench cases with gold file and line spans. Ravel found at least one gold file in 39 cases. Graphify returned no matching nodes in all 104 cases.
- The added Go code-search run used 1,005 CodeSearchNet test functions in one shared corpus. Documentation was removed from code and exact function names were redacted from queries. Graphify retrieved four more paired files, while Ravel ranked relevant files much higher: 205 Ravel results were rank one versus zero exact-file rank-one Graphify results.
- Graphify's faster Go query and tiny Go payload are not counted as wins because the result was empty.
- These are retrieval comparisons, not answer-quality tests. No LLM generated or judged final answers.

### Reproducibility

- Ravel: `v0.2.5`, executable SHA-256 `ae3b506247cb61d0c1d2316f9bb5bf3a8c290603b488d991b45f064cb1736c1e`
- Graphify: `0.9.12`, executable SHA-256 `90ab6099efdd91f20dad4d6aa51cf6dee2712bc2e6d6ef5ef7fb59ac07d6a021`
- Full metrics, pairwise counts, source hashes, and limitations: [`results/typescript-go-ravel-v0.2.5-vs-graphify-0.9.12-2026-07-16.json`](results/typescript-go-ravel-v0.2.5-vs-graphify-0.9.12-2026-07-16.json)

### Completed scale and parser-compatibility run

The Real-FIM-Eval scale run completed all 5,769 paired cases with zero execution failures: 3,182 TypeScript and 2,587 Go. It measures failures, partial-file parser compatibility, payload size, truncation, and build/query p50, p95, p99, and maximum latency. It does not measure retrieval or answer correctness because Real-FIM-Eval has no gold retrieval spans.

| Scale measurement | Ravel | Graphify | Winner |
| --- | ---: | ---: | --- |
| Successful paired cases | 5,769/5,769 | 5,769/5,769 | Tie |
| Mean build | 190 ms | 351 ms | Ravel |
| Build p99 | 851 ms | 425 ms | Graphify |
| Mean query | 18 ms | 147 ms | Ravel |
| Query p99 | 43 ms | 177 ms | Ravel |
| Mean payload | 1,579 tokens | 835 tokens | Graphify |
| Target file returned | 93.90% | 99.01% | Graphify |
| Go target file returned | 88.29% | 99.85% | Graphify |
| Truncated output | 0.00% | 5.63% | Ravel |

Graphify's clearest new win is incomplete-Go-file tolerance: Ravel returned only directory nodes on 343 cases, while Graphify missed the sole source file only four times across 2,587 Go cases. Ravel's broad traversal did not create a query tail-latency problem here, but its build tail was worse: overall build p99 was 100% higher than Graphify's and Go build p99 was 1.04 s versus 410 ms.

Together, the completed retrieval and scale runs cover exactly 10,000 benchmark cases: 6,304 TypeScript and 3,696 Go. Of those, 4,231 have explicit or deterministic retrieval gold; the 5,769 Real-FIM cases are scale/parser measurements only. Each Real-FIM corpus contains one incomplete pre-change file, so target-file rate is not cross-file retrieval quality.
