#!/usr/bin/env python3
"""Compare Ravel and Graphify on 1,005 CodeSearchNet Go retrieval pairs.

This is a deterministic retrieval compatibility slice, not the official
CodeSearchNet challenge metric. Each natural-language function description
is paired with one exact gold function file in a shared 1,005-file corpus.
The gold symbol is redacted from the query and documentation is not copied
into the code corpus, avoiding direct lexical answer leakage.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import hashlib
import json
import math
import os
from pathlib import Path
import re
import shutil
import statistics

from polyglot_compare import (
    COMMON_ADAPTER_VERSION,
    RAVEL_PROFILES,
    build_graphify,
    build_ravel,
    executable_metadata,
    graphify_result,
    paths_match,
    ravel_result,
    run_command,
    sha256_file,
    stable_hash,
)


ADAPTER_VERSION = "codesearchnet-go-redacted-paired-v1"
QUERY_MODE = "documentation-symbol-redacted-v1"
EXPECTED_SOURCE_ROWS = 14291
CASE_COUNT = 1005
GO_SYMBOL = re.compile(r"\bfunc\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)\s*(?:\[[^]]+\]\s*)?\(")
COMMENT_PREFIX = re.compile(r"^\s*(?://+|/\*+|\*+|\*/+)\s?")
WORD = re.compile(r"[A-Za-z][A-Za-z0-9_-]+")


def import_pyarrow():
    try:
        import pyarrow.parquet as parquet
    except ImportError as error:
        raise SystemExit(
            "PyArrow is required to read CodeSearchNet Parquet; run this adapter "
            "with a Python environment that provides pyarrow"
        ) from error
    return parquet


def read_source(path: Path) -> list[dict]:
    parquet = import_pyarrow()
    columns = (
        "repository_name",
        "func_path_in_repository",
        "func_name",
        "func_code_string",
        "func_documentation_string",
        "func_code_url",
        "language",
    )
    return parquet.read_table(path, columns=list(columns)).to_pylist()


def extracted_symbol(code: str) -> str:
    match = GO_SYMBOL.search(code)
    return match.group(1) if match else ""


def clean_documentation(documentation: str) -> str:
    lines = [COMMENT_PREFIX.sub("", line).strip() for line in documentation.splitlines()]
    return " ".join(line for line in lines if line).strip()


def query_text(documentation: str, symbol: str) -> str:
    query = clean_documentation(documentation)
    if symbol:
        query = re.sub(rf"\b{re.escape(symbol)}\b", "[redacted symbol]", query, flags=re.IGNORECASE)
    return f"Find the Go function that best matches this description:\n{query}"


def stable_case_id(row: dict) -> str:
    return stable_hash(
        "codesearchnet-go-test",
        row.get("repository_name", ""),
        row.get("func_path_in_repository", ""),
        row.get("func_name", ""),
        row.get("func_code_url", ""),
        row.get("func_code_string", ""),
        row.get("func_documentation_string", ""),
    )


def selection_key(case_id: str) -> str:
    return stable_hash("ravel-graphify-codesearchnet-go-1005-v1", case_id, length=64)


def target_path(case_id: str) -> str:
    return f"functions/{case_id}/snippet.go"


def eligible_case(row_number: int, row: dict) -> dict | None:
    required = (
        "repository_name",
        "func_path_in_repository",
        "func_name",
        "func_code_string",
        "func_documentation_string",
        "func_code_url",
    )
    if str(row.get("language", "")).lower() != "go":
        return None
    if any(not isinstance(row.get(key), str) or not row[key].strip() for key in required):
        return None
    code = row["func_code_string"].strip()
    documentation = row["func_documentation_string"].strip()
    symbol = extracted_symbol(code)
    if not symbol:
        return None
    redacted = query_text(documentation, symbol)
    meaningful = [word for word in WORD.findall(redacted.split("\n", 1)[-1]) if word != "redacted"]
    if len(meaningful) < 3:
        return None
    case_id = stable_case_id(row)
    return {
        "id": case_id,
        "selectionKey": selection_key(case_id),
        "sourceRow": row_number,
        "repository": row["repository_name"],
        "sourcePath": row["func_path_in_repository"],
        "sourceUrl": row["func_code_url"],
        "sourceFunctionName": row["func_name"],
        "goldSymbol": symbol,
        "goldPath": target_path(case_id),
        "codeSha256": hashlib.sha256(code.encode("utf-8")).hexdigest(),
        "documentationSha256": hashlib.sha256(documentation.encode("utf-8")).hexdigest(),
        "querySha256": hashlib.sha256(redacted.encode("utf-8")).hexdigest(),
        "codeChars": len(code),
        "documentationChars": len(documentation),
    }


def prepare(args: argparse.Namespace) -> None:
    source = args.parquet.resolve()
    if not source.is_file():
        raise SystemExit(f"missing CodeSearchNet Go test Parquet: {source}")
    rows = read_source(source)
    if len(rows) != EXPECTED_SOURCE_ROWS:
        raise SystemExit(f"expected {EXPECTED_SOURCE_ROWS} Go test rows, found {len(rows)}")
    eligible = []
    seen_code = set()
    duplicate_code = 0
    for row_number, row in enumerate(rows):
        case = eligible_case(row_number, row)
        if case is None:
            continue
        if case["codeSha256"] in seen_code:
            duplicate_code += 1
            continue
        seen_code.add(case["codeSha256"])
        eligible.append(case)
    eligible.sort(key=lambda case: (case["selectionKey"], case["id"]))
    cases = eligible[:CASE_COUNT]
    if len(cases) != CASE_COUNT:
        raise SystemExit(f"only {len(cases)} eligible unique cases; need {CASE_COUNT}")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    cases_path = output / "cases.jsonl"
    with cases_path.open("w", encoding="utf-8") as handle:
        for case in cases:
            handle.write(json.dumps(case, ensure_ascii=False, sort_keys=True) + "\n")
    manifest = {
        "version": 1,
        "benchmark": "CodeSearchNet Go redacted paired retrieval compatibility",
        "adapterVersion": ADAPTER_VERSION,
        "commonAdapterVersion": COMMON_ADAPTER_VERSION,
        "queryMode": QUERY_MODE,
        "officialMetric": False,
        "claimsAnswerCorrectness": False,
        "retrievalGold": "exact paired function file",
        "cases": len(cases),
        "sourceRows": len(rows),
        "eligibleUniqueCases": len(eligible),
        "duplicateCodeRowsSkipped": duplicate_code,
        "casesSha256": sha256_file(cases_path),
        "source": {
            "name": source.name,
            "bytes": source.stat().st_size,
            "sha256": sha256_file(source),
        },
    }
    (output / "manifest.json").write_text(
        json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    print(json.dumps(manifest, indent=2, sort_keys=True))


def read_cases(path: Path) -> list[dict]:
    cases = []
    seen = set()
    for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if not line.strip():
            continue
        case = json.loads(line)
        if not isinstance(case.get("id"), str) or case["id"] in seen:
            raise ValueError(f"{path}:{line_number}: invalid or duplicate case id")
        seen.add(case["id"])
        cases.append(case)
    return cases


def check(args: argparse.Namespace) -> None:
    manifest_path = args.manifest.resolve()
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    cases_path = manifest_path.parent / "cases.jsonl"
    cases = read_cases(cases_path)
    if manifest.get("version") != 1 or manifest.get("adapterVersion") != ADAPTER_VERSION:
        raise SystemExit("unsupported CodeSearchNet Go manifest version or adapter")
    if len(cases) != CASE_COUNT or manifest.get("cases") != len(cases):
        raise SystemExit(f"expected exactly {CASE_COUNT} CodeSearchNet Go cases")
    if manifest.get("casesSha256") != sha256_file(cases_path):
        raise SystemExit("CodeSearchNet Go cases hash mismatch")
    for case in cases:
        if not case.get("goldPath") or not case.get("goldSymbol") or not case.get("querySha256"):
            raise SystemExit(f"case {case.get('id')} lacks retrieval gold or query fingerprint")
    print(f"Validated {len(cases)} CodeSearchNet Go paired retrieval cases")


def verify_source(manifest: dict, source: Path) -> None:
    expected = manifest.get("source") or {}
    if not source.is_file():
        raise SystemExit(f"missing CodeSearchNet Go test Parquet: {source}")
    if source.stat().st_size != expected.get("bytes") or sha256_file(source) != expected.get("sha256"):
        raise SystemExit(f"CodeSearchNet Go source fingerprint mismatch: {source}")


def load_selected_rows(source: Path, cases: list[dict]) -> dict[str, dict]:
    rows = read_source(source)
    selected = {}
    for case in cases:
        row_number = case["sourceRow"]
        if not isinstance(row_number, int) or not (0 <= row_number < len(rows)):
            raise RuntimeError(f"invalid source row for case {case['id']}")
        row = rows[row_number]
        if stable_case_id(row) != case["id"]:
            raise RuntimeError(f"CodeSearchNet case fingerprint mismatch: {case['id']}")
        code = row["func_code_string"].strip()
        documentation = row["func_documentation_string"].strip()
        if hashlib.sha256(code.encode("utf-8")).hexdigest() != case["codeSha256"]:
            raise RuntimeError(f"CodeSearchNet code hash mismatch: {case['id']}")
        if hashlib.sha256(documentation.encode("utf-8")).hexdigest() != case["documentationSha256"]:
            raise RuntimeError(f"CodeSearchNet documentation hash mismatch: {case['id']}")
        question = query_text(documentation, case["goldSymbol"])
        if hashlib.sha256(question.encode("utf-8")).hexdigest() != case["querySha256"]:
            raise RuntimeError(f"CodeSearchNet query hash mismatch: {case['id']}")
        selected[case["id"]] = row
    return selected


def materialize(repository: Path, cases: list[dict], rows: dict[str, dict]) -> None:
    for case in cases:
        destination = repository / case["goldPath"]
        destination.parent.mkdir(parents=True, exist_ok=True)
        code = rows[case["id"]]["func_code_string"].strip()
        destination.write_text(f"package benchmark\n\n{code}\n", encoding="utf-8")


def score_path(items: list[dict], gold_path: str) -> dict:
    rank = next(
        (rank for rank, item in enumerate(items, 1) if paths_match(str(item.get("path", "")), gold_path)),
        None,
    )
    return {
        "hit": rank is not None,
        "rank": rank,
        "reciprocalRank": 0.0 if rank is None else 1.0 / rank,
    }


def percentile(values: list[float], fraction: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    return ordered[min(len(ordered) - 1, max(0, math.ceil(len(ordered) * fraction) - 1))]


def tool_summary(values: list[dict]) -> dict:
    query = [value["queryMs"] for value in values]
    return {
        "recall": statistics.fmean(value["hit"] for value in values) if values else 0.0,
        "mrr": statistics.fmean(value["reciprocalRank"] for value in values) if values else 0.0,
        "meanEstimatedTokens": statistics.fmean(value["estimatedTokens"] for value in values) if values else 0.0,
        "meanReturned": statistics.fmean(value["returned"] for value in values) if values else 0.0,
        "meanQueryMs": statistics.fmean(query) if query else 0.0,
        "queryP50Ms": percentile(query, 0.50),
        "queryP95Ms": percentile(query, 0.95),
        "queryP99Ms": percentile(query, 0.99),
        "queryMaxMs": max(query) if query else 0.0,
        "truncationRate": statistics.fmean(value["truncated"] for value in values) if values else 0.0,
    }


def summarize(results_path: Path, build: dict) -> dict:
    latest = {}
    for line in results_path.read_text(encoding="utf-8").splitlines():
        if line.strip():
            row = json.loads(line)
            latest[row["id"]] = row
    rows = list(latest.values())
    ok = [row for row in rows if row.get("status") == "ok"]
    summary = {
        "version": 1,
        "adapterVersion": ADAPTER_VERSION,
        "cases": len(rows),
        "successfulCases": len(ok),
        "failedCases": len(rows) - len(ok),
        "language": "go",
        "sharedCorpusFiles": build["corpusFiles"],
        "ravel": {
            "buildMs": build["ravelBuildMs"],
            "graphNodes": build["ravelGraphNodes"],
            **tool_summary([row["ravel"] for row in ok]),
        },
        "graphify": {
            "buildMs": build["graphifyBuildMs"],
            "extractMs": build["graphifyExtractMs"],
            "clusterMs": build["graphifyClusterMs"],
            "graphNodes": build["graphifyGraphNodes"],
            **tool_summary([row["graphify"] for row in ok]),
        },
    }
    summary["pairwise"] = {
        "bothHit": sum(row["ravel"]["hit"] and row["graphify"]["hit"] for row in ok),
        "ravelOnlyHit": sum(row["ravel"]["hit"] and not row["graphify"]["hit"] for row in ok),
        "graphifyOnlyHit": sum(not row["ravel"]["hit"] and row["graphify"]["hit"] for row in ok),
        "bothMiss": sum(not row["ravel"]["hit"] and not row["graphify"]["hit"] for row in ok),
        "ravelQueryWins": sum(row["ravel"]["queryMs"] < row["graphify"]["queryMs"] for row in ok),
    }
    return summary


def run_case(args: argparse.Namespace, case: dict, row: dict, graphs: dict[str, Path]) -> dict:
    result = {
        "id": case["id"],
        "language": "go",
        "repository": case["repository"],
        "sourcePath": case["sourcePath"],
        "sourceFunctionName": case["sourceFunctionName"],
        "goldSymbol": case["goldSymbol"],
        "goldPath": case["goldPath"],
    }
    try:
        question = query_text(row["func_documentation_string"], case["goldSymbol"])
        ravel = ravel_result(args.ravel, graphs["ravel"], question, args.token_budget, args.ravel_profile)
        ravel.update(score_path(ravel["items"], case["goldPath"]))
        ravel["returned"] = len(ravel["items"])
        ravel["items"] = ravel["items"][: args.keep_items]
        graphify = graphify_result(args.graphify, graphs["graphify"], question, args.token_budget)
        graphify.update(score_path(graphify["items"], case["goldPath"]))
        graphify["returned"] = len(graphify["items"])
        graphify["items"] = graphify["items"][: args.keep_items]
        result.update({"status": "ok", "ravel": ravel, "graphify": graphify})
    except Exception as error:
        result.update({"status": "error", "error": str(error)})
    return result


def graph_node_count(path: Path) -> int:
    value = json.loads(path.read_text(encoding="utf-8"))
    nodes = value.get("nodes")
    return len(nodes) if isinstance(nodes, list) else 0


def build_corpus_and_graphs(
    args: argparse.Namespace,
    workspace: Path,
    cases: list[dict],
    rows: dict[str, dict],
) -> tuple[dict, dict[str, Path]]:
    repository = workspace / "corpus"
    ravel_graph = workspace / "ravel-graph"
    graphify_root = workspace / "graphify-graph"
    build_path = workspace / "build.json"
    graphify_graph = graphify_root / "graphify-out" / "graph.json"
    if build_path.exists():
        build = json.loads(build_path.read_text(encoding="utf-8"))
        if not (ravel_graph / "graph.json").is_file() or not graphify_graph.is_file():
            raise SystemExit("workspace build metadata exists but a graph is missing; use a new workspace")
        if graph_node_count(ravel_graph / "graph.json") < 1 or graph_node_count(graphify_graph) < 1:
            raise SystemExit("workspace contains an empty tool graph; use a new workspace")
        return build, {"ravel": ravel_graph, "graphify": graphify_graph}
    for generated in (repository, ravel_graph, graphify_root):
        if generated.exists():
            shutil.rmtree(generated)
    repository.mkdir(parents=True)
    materialize(repository, cases, rows)
    ravel_build_ms = build_ravel(args.ravel, repository, ravel_graph)
    graphify_graph, extract_ms, cluster_ms = build_graphify(
        args.graphify, repository, graphify_root
    )
    ravel_nodes = graph_node_count(ravel_graph / "graph.json")
    graphify_nodes = graph_node_count(graphify_graph)
    if ravel_nodes < 1 or graphify_nodes < 1:
        raise RuntimeError(
            f"invalid empty graph: ravel nodes={ravel_nodes}, graphify nodes={graphify_nodes}"
        )
    build = {
        "version": 1,
        "corpusFiles": len(cases),
        "ravelGraphNodes": ravel_nodes,
        "graphifyGraphNodes": graphify_nodes,
        "ravelBuildMs": ravel_build_ms,
        "graphifyBuildMs": extract_ms + cluster_ms,
        "graphifyExtractMs": extract_ms,
        "graphifyClusterMs": cluster_ms,
    }
    build_path.write_text(json.dumps(build, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    return build, {"ravel": ravel_graph, "graphify": graphify_graph}


def execute(args: argparse.Namespace) -> None:
    manifest_path = args.manifest.resolve()
    check(argparse.Namespace(manifest=manifest_path))
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    source = args.parquet.resolve()
    verify_source(manifest, source)
    cases = read_cases(manifest_path.parent / "cases.jsonl")
    if args.limit:
        cases = cases[: args.limit]
    selection_sha = stable_hash(*(case["id"] for case in cases), length=64)
    workspace = args.workspace.resolve()
    workspace.mkdir(parents=True, exist_ok=True)
    results_path = workspace / "results.jsonl"
    config_path = workspace / "run-config.json"
    run_config = {
        "adapterVersion": ADAPTER_VERSION,
        "queryMode": QUERY_MODE,
        "manifestSha256": sha256_file(manifest_path),
        "sourceSha256": sha256_file(source),
        "cases": len(cases),
        "selectionSha256": selection_sha,
        "ravelProfile": args.ravel_profile,
        "ravelProfileArgs": list(RAVEL_PROFILES[args.ravel_profile]),
        "tokenBudget": args.token_budget,
        "workers": args.workers,
        "ravelExecutable": executable_metadata(args.ravel),
        "graphifyExecutable": executable_metadata(args.graphify),
    }
    if config_path.exists():
        if json.loads(config_path.read_text(encoding="utf-8")) != run_config:
            raise SystemExit("workspace settings differ; use a new workspace")
    elif results_path.exists() or (workspace / "build.json").exists():
        raise SystemExit("legacy workspace lacks run-config.json; use a new workspace")
    config_path.write_text(json.dumps(run_config, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    rows = load_selected_rows(source, cases)
    build, graphs = build_corpus_and_graphs(args, workspace, cases, rows)
    completed = set()
    if results_path.exists():
        completed = {
            row.get("id")
            for line in results_path.read_text(encoding="utf-8").splitlines()
            if line.strip()
            for row in (json.loads(line),)
            if row.get("status") == "ok"
        }
    pending = [case for case in cases if case["id"] not in completed]
    print(f"Running {len(pending)} pending of {len(cases)} CodeSearchNet Go cases", flush=True)
    finished = 0
    failures = 0
    with results_path.open("a", encoding="utf-8") as output:
        with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
            futures = {
                pool.submit(run_case, args, case, rows[case["id"]], graphs): case["id"]
                for case in pending
            }
            for future in concurrent.futures.as_completed(futures):
                result = future.result()
                failures += result.get("status") != "ok"
                output.write(json.dumps(result, ensure_ascii=False, sort_keys=True) + "\n")
                output.flush()
                finished += 1
                if finished <= 10 or finished % args.progress_every == 0:
                    print(
                        f"finished={finished}/{len(pending)} failures={failures} last={result['id']}",
                        flush=True,
                    )
    summary = summarize(results_path, build)
    summary.update({
        "benchmark": "CodeSearchNet Go redacted paired retrieval compatibility",
        "queryMode": QUERY_MODE,
        "officialMetric": False,
        "claimsAnswerCorrectness": False,
        "manifestSha256": sha256_file(manifest_path),
        "resultsSha256": sha256_file(results_path),
        "runConfigSha256": sha256_file(config_path),
        "tokenBudget": args.token_budget,
        "workers": args.workers,
        "ravelProfile": args.ravel_profile,
        "ravelProfileArgs": list(RAVEL_PROFILES[args.ravel_profile]),
        "ravelVersion": run_command([args.ravel, "version"], workspace)[0].strip(),
        "graphifyVersion": run_command([args.graphify, "--version"], workspace)[0].strip(),
        "ravelExecutable": executable_metadata(args.ravel),
        "graphifyExecutable": executable_metadata(args.graphify),
        "platform": {"os": os.uname().sysname, "arch": os.uname().machine},
    })
    (workspace / "summary.json").write_text(
        json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    print(json.dumps(summary, indent=2, sort_keys=True))


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    prepare_parser = subparsers.add_parser("prepare", help="select and fingerprint 1,005 Go cases")
    prepare_parser.add_argument("--parquet", type=Path, required=True)
    prepare_parser.add_argument("--output", type=Path, required=True)
    prepare_parser.set_defaults(function=prepare)
    check_parser = subparsers.add_parser("check", help="validate a prepared manifest offline")
    check_parser.add_argument("--manifest", type=Path, required=True)
    check_parser.set_defaults(function=check)
    run_parser = subparsers.add_parser("run", help="build the shared corpus and run or resume")
    run_parser.add_argument("--manifest", type=Path, required=True)
    run_parser.add_argument("--parquet", type=Path, required=True)
    run_parser.add_argument("--workspace", type=Path, required=True)
    run_parser.add_argument("--ravel", default="ravel")
    run_parser.add_argument("--graphify", default="graphify")
    run_parser.add_argument("--workers", type=int, default=2)
    run_parser.add_argument("--token-budget", type=int, default=2000)
    run_parser.add_argument("--ravel-profile", choices=sorted(RAVEL_PROFILES), default="broad")
    run_parser.add_argument("--keep-items", type=int, default=20)
    run_parser.add_argument("--limit", type=int)
    run_parser.add_argument("--progress-every", type=int, default=100)
    run_parser.set_defaults(function=execute)
    args = parser.parse_args()
    if getattr(args, "workers", 1) < 1:
        parser.error("--workers must be positive")
    if getattr(args, "token_budget", 64) < 64:
        parser.error("--token-budget must be at least 64")
    limit = getattr(args, "limit", None)
    if limit is not None and limit < 1:
        parser.error("--limit must be positive")
    args.function(args)


if __name__ == "__main__":
    main()
