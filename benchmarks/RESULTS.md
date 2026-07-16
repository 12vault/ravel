# Benchmark results log

This file is the human-readable record of Ravel vs Graphify runs. Raw, machine-readable results live in [`results/`](results/).

## 2026-07-16: Swift on Ghostty

Environment: Apple M1 Pro, macOS arm64, 2,000-token budget, Ravel broad retrieval profile. The shared corpus contains all 188 tracked Swift files under Ghostty's `macos/` tree at commit `73534c4680a809398b396c94ac7f12fcccb7963d` (36,025 lines). All `///` documentation was blanked from the corpus with line numbers preserved, and exact symbols were redacted from 455 documentation-derived queries.

| Measurement | Ravel | Graphify | Winner |
| --- | ---: | ---: | --- |
| Exact declaration retrieval | 43/455 (9.45%) | 40/455 (8.79%) | Ravel |
| MRR | 0.0716 | 0.0033 | Ravel |
| Declaration graph coverage | 96/455 (21.10%) | 373/455 (81.98%) | Graphify |
| Function coverage | 0/299 | 237/299 | Graphify |
| Build time | 88.55 s | 3.36 s | Graphify |
| Mean query | 39.18 ms | 346.47 ms | Ravel |
| Query p99 | 50.16 ms | 405.16 ms | Ravel |
| Mean payload | 1,915 tokens | 2,061 tokens | Ravel |
| Truncated output | 0.00% | 89.67% | Ravel |

The overall Swift verdict is split. Graphify is the much stronger extractor: it covered 82.0% of gold declarations and built 26.4× faster, while Ravel covered 21.1% and extracted no eligible functions. Ravel is the stronger retriever for what it did extract: 44.8% conditional recall versus Graphify's 10.7%, 21.6× higher MRR, 8.8× faster queries, and 7.1% smaller payloads. Its higher total hit count came from enum and struct retrieval; Graphify owned function retrieval.

Ravel's recorded build emitted eight Swift parser timeouts and 89 diagnostics overall. A 20-case smoke build produced 506 nodes, while the full build produced 491, so timeout-bounded Swift extraction is not fully stable on this corpus. No LLM generated or judged final answers; these are documentation-to-declaration retrieval proxies.

Full metrics, per-kind counts, graph coverage, hashes, and limitations: [`results/swift-ghostty-ravel-v0.2.5-vs-graphify-0.9.12-2026-07-16.json`](results/swift-ghostty-ravel-v0.2.5-vs-graphify-0.9.12-2026-07-16.json)

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
