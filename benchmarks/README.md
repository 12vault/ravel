# Ravel benchmarks

Run the repeatable local suite with:

```sh
./benchmarks/run.sh
```

The suite reports graph build and query throughput without network access. `go test ./...` also compiles and smoke-tests every benchmark.

Run retrieval-quality cases against a built graph:

```sh
ravel benchmark --graph .reporavel --dataset benchmarks/example.jsonl --out benchmark-results.json
```

Each JSONL record requires `id`, `dataset`, `question`, and `expectedNodeIds`. Dataset names may be `repository-questions`, `LOCOMO`, `LongMemEval`, or a custom suite. The runner reports recall, precision, reciprocal rank, latency, and graph size overall and per dataset.

## Quality datasets

`datasets.json` defines adapters for repository questions, LOCOMO, and LongMemEval. The latter two measure long-context memory rather than parser quality, so Ravel evaluates them as a retrieval layer: convert each context into evidence-tagged graph fragments and each question into the common JSONL case format. Dataset content and model credentials are never bundled or downloaded automatically.

An evaluation run must record the Ravel version, dataset revision, adapter version, model/provider when used, configuration, hardware, and raw result file. Do not compare scores produced with different datasets or model settings.
