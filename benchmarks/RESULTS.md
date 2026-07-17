# Benchmark results log

This file is the human-readable record of Ravel vs Graphify runs. Raw, machine-readable results live in [`results/`](results/).

## 2026-07-17: T3 Code retrieval ranking and adjacency verification

This Ravel-only working-tree verification reused the pinned T3 Code graph and all 487 documentation-derived questions from the persistent TypeScript run below. Graphify was not rerun. The comparison isolates commit `018ccc2`, which builds compact directional adjacency and degree indexes once, filters and sorts only the neighbor lists actually expanded by traversal, and reuses outgoing adjacency when collecting result edges.

The cached representation preserved every measured retrieval and payload result from the post-ranking baseline:

| Quality measurement | Before adjacency cache | Cached adjacency |
| --- | ---: | ---: |
| Exact declaration retrieval | 183/487 (37.58%) | 183/487 (37.58%) |
| MRR | 0.1431 | 0.1431 |
| Rank-one hits | 39 | 39 |
| Mean payload | 1,855 tokens | 1,855 tokens |
| Payload p95 | 1,924 tokens | 1,924 tokens |

On the synthetic 1,000-node query benchmark, median BFS retrieval improved from 1.987 ms to 1.309 ms, allocated bytes fell from 1,908,813 to 1,146,473 per operation, and allocations fell from 9,451 to 4,431 per operation. That is 34.1% less time, 39.9% fewer bytes, and 53.1% fewer allocations. The cost moves to reusable index construction: median index-build time changed from 10.121 ms to 10.237 ms (+1.1%) and allocated bytes from 9,133,475 to 9,207,337 (+0.8%). Retrieval medians use three baseline and five updated trials; index-build medians use three trials each.

| Warm latency across 487 T3 questions | Before adjacency cache | Cached adjacency, repeat |
| --- | ---: | ---: |
| Mean | 1,371.54 ms | 1,177.23 ms |
| p50 | 1,271.12 ms | 1,061.77 ms |
| p95 | 2,224.02 ms | 2,196.26 ms |
| p99 | 2,905.12 ms | 3,116.36 ms |
| Maximum | 5,423.56 ms | 6,915.08 ms |

The full-corpus repeat improved mean latency by 14.2%, p50 by 16.5%, and p95 by 1.2%. Its p99 and maximum were 7.3% and 27.5% higher, but those separate-run extremes did not reproduce when isolated: ten direct runs of the slow `PREVIEW_WEBVIEW_PREFERENCES` case improved from 6,167.84 ms mean / 7,241.84 ms maximum to 5,540.74 ms mean / 5,828.33 ms maximum. Treat the full-run p99 and maximum differences as machine-state noise, not a demonstrated tail regression.

Regression coverage compares cached adjacency with the previous construction across outgoing, incoming, bidirectional, relation-filtered, direction-preferred, community-aware, self-loop, and invalid-endpoint cases. It also verifies that only expanded nodes are sorted and that the histogram hub threshold matches the exact 99th percentile. The full Go suite, the 32-test Python benchmark suite, and the query package under the race detector passed. Aggregate measurements, hashes, trial counts, and limitations are recorded in the [`machine-readable summary`](results/t3code-typescript-ravel-query-adjacency-working-tree-2026-07-17.json).

## 2026-07-17: T3 Code TypeScript extraction development verification

This Ravel-only working-tree verification reused the pinned, documentation-stripped T3 Code corpus and all 487 questions from the persistent run below. Graphify was not rerun; its archived result is included only as the fixed comparison point. The change adds syntax-backed module-level TypeScript/JavaScript bindings and narrowly recovers valid TypeScript declarations that the embedded grammar represents as `ERROR` nodes. Recovered declarations are marked partial rather than presented as complete parses.

| Measurement | Previous Ravel | Updated Ravel | Archived Graphify |
| --- | ---: | ---: | ---: |
| Declaration graph coverage | 334/487 (68.58%) | 478/487 (98.15%) | 451/487 (92.61%) |
| Exact declaration retrieval | 50/487 (10.27%) | 69/487 (14.17%) | 20/487 (4.11%) |
| MRR | 0.0406 | 0.0520 | 0.0010 |
| Graph nodes | 122,193 | 121,925 | 18,455 |
| Graph edges | 148,724 | 152,324 | 35,998 |
| Mean payload | 1,892 tokens | 1,893 tokens | 2,158 tokens |

The updated Ravel-only run recorded warm mean/p50/p95/p99/max query times of 2.524/2.235/4.848/6.788/8.607 seconds. These timings were collected later under different machine state and are not a paired latency comparison with the archived runs. A rejected all-local-binding experiment reached 487/487 graph coverage but added about 26,000 nodes without improving the 135 `const`/`let` exact-hit count; it raised their mean/p95/max query latency to 4.256/7.432/16.255 seconds. The production change therefore keeps module declarations and omits function-local temporaries.

## 2026-07-17: TypeScript on T3 Code, persistent Ravel rerun

Environment: Apple M1 Pro, macOS arm64, 2,000-token budget, Ravel broad retrieval profile, two query workers. The shared corpus is pinned upstream [`pingdotgg/t3code`](https://github.com/pingdotgg/t3code) revision `2a33a18716854b8d07378008cf3101ad999209ae`: 1,952 first-party TypeScript files, 513,548 lines, and 487 documentation-derived exact-declaration questions. Both tools received byte-identical documentation-stripped source.

Ravel used the new fixed-snapshot `context-batch` JSONL command. Each of the two workers loaded the graph and built its reusable index once, then served warm queries. Normal one-shot `ravel context` behavior is unchanged. The benchmark records Ravel startup separately; Graphify still launches one process per query, so the latency columns below are useful operational measurements but not an apples-to-apples tool-speed contest.

| Measurement | Ravel persistent | Graphify process |
| --- | ---: | ---: |
| Exact declaration retrieval | 50/487 (10.27%) | 20/487 (4.11%) |
| MRR | 0.0406 | 0.0010 |
| Declaration graph coverage | 334/487 (68.58%) | 451/487 (92.61%) |
| Top-20 same-symbol retrieval | 84/487 (17.25%) | 100/487 (20.53%) |
| Mean balanced build | 15.67 s | 32.87 s |
| Mean query | 1.730 s warm | 1.565 s process |
| Query p50 | 1.613 s | 1.556 s |
| Query p95 | 2.765 s | 1.839 s |
| Query p99 | 3.762 s | 2.014 s |
| Query maximum | 6.122 s | 2.601 s |
| Mean payload | 1,892 tokens | 2,158 tokens |
| Truncated output | 0.00% | 90.76% |

Ravel session startup was 6.05 s and 6.00 s. Per session, graph loading took about 1.41 s and index construction took 4.56–4.61 s. Compared with the first one-shot baseline below, warm Ravel latency fell 79.9% at p50, 87.8% at p95, 95.7% at p99, and 97.6% at maximum. The old 80–303 second paired stalls disappeared; the corrected maximum was 6.12 seconds across all 487 cases.

The reusable path initially exposed an existing tie-order bug: compound-name IDF weights were summed by iterating a Go map, so duplicate call-site nodes could switch source lines between identical queries. The fix stores a sorted name-term order. A repeated-query regression test and focused checks of all five unstable real cases now match one-shot `ravel context` exactly. Aggregate Ravel quality and payload match the original baseline: 50 exact hits, MRR 0.0406, and 1,892 mean tokens.

Pairwise quality was Ravel-only on 47 cases, Graphify-only on 17, both hit on 3, and both missed on 420. Graphify still has better declaration coverage and top-20 same-symbol recall. Ravel has more exact documentation-site hits, much higher MRR, smaller payloads, and no output truncation.

This is a retrieval-compatibility benchmark, not an official T3 Code metric or an LLM answer-quality test. Full summary, raw results, execution/startup metadata, build trials, run config, and provenance are adjacent to [`the persistent-run artifact`](results/t3code-typescript-ravel-context-batch-working-tree-vs-graphify-0.9.12-2026-07-17.json).

## 2026-07-17: TypeScript on T3 Code, original one-shot diagnostics

Environment: Apple M1 Pro, macOS arm64, Graphify 0.9.12, 2,000-token budget, Ravel broad retrieval profile, two query workers. Ravel was the same dirty working-tree binary in both runs: revision `200fed605b3100a93f8dfa0d949ddfe3923db28e`, executable SHA-256 `7bb7202f…`, and module version `v0.2.6-0.20260716114846-200fed605b31+dirty`; its CLI still printed `v0.2.5`, so these artifacts are deliberately not labeled as the published v0.2.5 release.

The shared corpus is pinned upstream [`pingdotgg/t3code`](https://github.com/pingdotgg/t3code) revision `2a33a18716854b8d07378008cf3101ad999209ae`: 1,952 tracked first-party `.ts`, `.tsx`, `.mts`, and `.cts` files (513,548 lines) under `apps/`, `infra/`, `oxlint-plugin-t3code/`, `packages/`, and `scripts/`. T3 Code's checked-in `.repos/` fixture/vendor tree was excluded. JSDoc was blanked without moving source lines, exact declaration symbols were redacted from 487 documentation-derived queries, and both tools received byte-identical source bytes.

| Measurement | Ravel run 1 | Ravel run 2 | Graphify run 1 | Graphify run 2 |
| --- | ---: | ---: | ---: | ---: |
| Exact declaration retrieval | 50 (10.27%) | 50 (10.27%) | 20 (4.11%) | 18 (3.70%) |
| MRR | 0.0406 | 0.0406 | 0.0010 | 0.0010 |
| Declaration graph coverage | 334 (68.58%) | 334 (68.58%) | 451 (92.61%) | 451 (92.61%) |
| Top-20 same-symbol retrieval | 84 (17.25%) | 84 (17.25%) | 100 (20.53%) | 100 (20.53%) |
| Build trials | 90.30 / 16.37 s | 21.12 / 26.08 s | 33.62 / 31.78 s | 36.00 / 36.04 s |
| Mean query | 11.65 s | 11.05 s | 1.98 s | 2.07 s |
| Query p50 | 8.04 s | 8.02 s | 1.70 s | 1.77 s |
| Query p95 | 22.67 s | 18.66 s | 3.13 s | 2.83 s |
| Query p99 | 88.32 s | 84.18 s | 6.74 s | 11.53 s |
| Query maximum | 260.12 s | 302.90 s | 12.65 s | 20.89 s |
| Mean payload | 1,892 tokens | 1,892 tokens | 2,158 tokens | 2,163 tokens |
| Truncated output | 0.00% | 0.00% | 90.76% | 91.79% |

Ravel's retrieval metrics, graph coverage, payload, and truncation were identical across both runs. Graphify's declaration coverage and top-20 same-symbol retrieval were stable, but its exact-line result changed from 20 to 18 hits. The build trials are cache-sensitive: Ravel's run-1 trials differed by 73.94 seconds, while its run-2 trials were within 4.96 seconds, so the simple mean should not be treated as a stable cold-build number.

The poor one-shot Ravel query tail reproduced. Run 2 again clustered extreme stalls across both concurrent workers: two unrelated queries took 301–303 seconds together. Latency had no meaningful relationship to explored nodes, returned nodes, tokens, declaration kind, hit/miss status, or tool order. The persistent rerun above confirmed repeated fresh-process graph loading and index construction were the main cause.

This is a retrieval-compatibility benchmark, not an official T3 Code metric or an LLM answer-quality test. The repository now preserves each run's summary, run config, raw results, build metadata, the shared manifest/cases, and [`provenance`](results/t3code-typescript-ravel-multilang-working-tree-vs-graphify-0.9.12-2026-07-17-provenance.json). See [`run 1`](results/t3code-typescript-ravel-multilang-working-tree-vs-graphify-0.9.12-2026-07-17-run1.json) and [`run 2`](results/t3code-typescript-ravel-multilang-working-tree-vs-graphify-0.9.12-2026-07-17-run2.json).

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
