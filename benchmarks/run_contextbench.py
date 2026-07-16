#!/usr/bin/env python3
"""Compare Ravel and Graphify on ContextBench gold repository context."""

from __future__ import annotations

import argparse
import concurrent.futures
import json
import math
import os
from pathlib import Path
import shutil
import statistics
import subprocess
import threading
import time

from polyglot_compare import (
    COMMON_ADAPTER_VERSION,
    RAVEL_PROFILES,
    build_graphify,
    build_ravel,
    executable_metadata,
    graphify_result,
    ravel_result,
    run_command,
    score_span_retrieval,
    sha256_file,
    stable_hash,
)


ADAPTER_VERSION = "contextbench-ravel-graphify-v1"
RESULT_LOCK = threading.Lock()


def require_pyarrow():
    try:
        import pyarrow.parquet as parquet
    except ImportError as error:
        raise SystemExit(
            "PyArrow is required for ContextBench Parquet files. Install it in an isolated "
            "environment and run this script with that environment's Python."
        ) from error
    return parquet


def parse_gold_context(raw: object) -> list[dict]:
    if isinstance(raw, str):
        raw = json.loads(raw)
    if not isinstance(raw, list):
        raise ValueError("gold_context must be a JSON list")
    spans = []
    for value in raw:
        if not isinstance(value, dict):
            continue
        path = str(value.get("file", "")).replace("\\", "/").lstrip("/")
        start = int(value.get("start_line") or 0)
        end = int(value.get("end_line") or 0)
        if path and start > 0 and end >= start:
            spans.append({
                "file": path,
                "start_line": start,
                "end_line": end,
                "content": str(value.get("content", "")),
            })
    if not spans:
        raise ValueError("gold_context contains no valid file spans")
    return spans


def prepare(args: argparse.Namespace) -> None:
    source = args.parquet.resolve()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    parquet = require_pyarrow()
    columns = [
        "instance_id", "original_inst_id", "repo", "repo_url", "language",
        "base_commit", "gold_context", "problem_statement", "source",
    ]
    table = parquet.read_table(source, columns=columns)
    wanted = {language.lower() for language in args.language}
    cases = []
    skipped = 0
    for row in table.to_pylist():
        language = str(row.get("language", "")).lower()
        if language not in wanted:
            continue
        try:
            gold = parse_gold_context(row.get("gold_context"))
        except (TypeError, ValueError, json.JSONDecodeError):
            skipped += 1
            continue
        instance_id = str(row.get("instance_id", ""))
        repo_url = str(row.get("repo_url", ""))
        commit = str(row.get("base_commit", ""))
        question = str(row.get("problem_statement", ""))
        if not instance_id or not repo_url or not commit or not question:
            skipped += 1
            continue
        cases.append({
            "id": instance_id,
            "originalInstanceId": str(row.get("original_inst_id", "")),
            "language": language,
            "repo": str(row.get("repo", "")),
            "repoUrl": repo_url,
            "baseCommit": commit,
            "problemStatement": question,
            "goldContext": gold,
            "source": str(row.get("source", "")),
            "graphKey": stable_hash(repo_url, commit),
        })
    cases.sort(key=lambda case: (case["language"], case["repo"], case["id"]))
    cases_path = output / "cases.jsonl"
    with cases_path.open("w", encoding="utf-8") as handle:
        for case in cases:
            handle.write(json.dumps(case, ensure_ascii=False, sort_keys=True) + "\n")
    languages = {
        language: sum(case["language"] == language for case in cases)
        for language in sorted(wanted)
    }
    manifest = {
        "version": 1,
        "benchmark": "ContextBench",
        "adapterVersion": ADAPTER_VERSION,
        "commonAdapterVersion": COMMON_ADAPTER_VERSION,
        "cases": len(cases),
        "skippedInvalidCases": skipped,
        "languages": languages,
        "repositories": len({(case["repoUrl"], case["baseCommit"]) for case in cases}),
        "casesSha256": sha256_file(cases_path),
        "source": {
            "path": source.name,
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
        raise SystemExit("unsupported ContextBench manifest version or adapter")
    if manifest.get("cases") != len(cases) or manifest.get("casesSha256") != sha256_file(cases_path):
        raise SystemExit("ContextBench manifest count or cases hash mismatch")
    actual_languages = {
        language: sum(case.get("language") == language for case in cases)
        for language in sorted({case.get("language") for case in cases})
    }
    if manifest.get("languages") != actual_languages:
        raise SystemExit(f"ContextBench language counts mismatch: {actual_languages}")
    for case in cases:
        if not case.get("repoUrl") or not case.get("baseCommit") or not case.get("goldContext"):
            raise SystemExit(f"ContextBench case {case['id']} is incomplete")
    print(f"Validated {len(cases)} ContextBench cases: {actual_languages}")


def repository_paths(cache: Path, case: dict) -> tuple[Path, Path]:
    repo_key = stable_hash(case["repoUrl"], length=20)
    base = cache / "repositories" / repo_key
    checkout = cache / "checkouts" / case["graphKey"]
    return base, checkout


def git_has_commit(repository: Path, commit: str) -> bool:
    result = subprocess.run(
        ["git", "-C", str(repository), "cat-file", "-e", f"{commit}^{{commit}}"],
        text=True,
        capture_output=True,
        check=False,
    )
    return result.returncode == 0


def ensure_checkout(case: dict, cache: Path, offline: bool) -> Path:
    base, checkout = repository_paths(cache, case)
    base.parent.mkdir(parents=True, exist_ok=True)
    checkout.parent.mkdir(parents=True, exist_ok=True)
    if not (base / ".git").exists():
        if offline:
            raise RuntimeError(f"repository is not cached for {case['id']}")
        run_command(
            ["git", "clone", "--filter=blob:none", "--no-checkout", case["repoUrl"], str(base)],
            base.parent,
        )
    if not git_has_commit(base, case["baseCommit"]):
        if offline:
            raise RuntimeError(f"commit is not cached for {case['id']}: {case['baseCommit']}")
        run_command(
            ["git", "-C", str(base), "fetch", "--depth", "1", "origin", case["baseCommit"]],
            base.parent,
        )
    marker = cache / "checkout-metadata" / f"{case['graphKey']}.json"
    marker.parent.mkdir(parents=True, exist_ok=True)
    if marker.exists():
        value = json.loads(marker.read_text(encoding="utf-8"))
        if value == {"repoUrl": case["repoUrl"], "baseCommit": case["baseCommit"]}:
            return checkout
        raise RuntimeError(f"checkout marker mismatch: {checkout}")
    if checkout.exists():
        raise RuntimeError(f"unmarked checkout already exists: {checkout}")
    try:
        run_command(
            ["git", "-C", str(base), "worktree", "add", "--detach", str(checkout), case["baseCommit"]],
            base.parent,
        )
    except RuntimeError:
        if offline:
            raise
        subprocess.run(
            ["git", "-C", str(base), "worktree", "remove", "--force", str(checkout)],
            text=True,
            capture_output=True,
            check=False,
        )
        if checkout.exists():
            shutil.rmtree(checkout)
        run_command(
            [
                "git", "-C", str(base), "fetch", "--refetch", "--depth", "1", "--no-filter",
                "origin", case["baseCommit"],
            ],
            base.parent,
        )
        run_command(
            ["git", "-C", str(base), "worktree", "add", "--detach", str(checkout), case["baseCommit"]],
            base.parent,
        )
    marker.write_text(
        json.dumps({"repoUrl": case["repoUrl"], "baseCommit": case["baseCommit"]}, sort_keys=True) + "\n",
        encoding="utf-8",
    )
    return checkout


def selected_cases(args: argparse.Namespace, manifest_path: Path) -> list[dict]:
    cases = read_cases(manifest_path.parent / "cases.jsonl")
    if args.language:
        cases = [case for case in cases if case["language"] in args.language]
    if args.limit:
        cases = cases[: args.limit]
    return cases


def fetch(args: argparse.Namespace) -> None:
    manifest_path = args.manifest.resolve()
    check(argparse.Namespace(manifest=manifest_path))
    cases = selected_cases(args, manifest_path)
    cache = args.cache.resolve()
    unique = {}
    for case in cases:
        unique.setdefault(case["graphKey"], case)
    for index, case in enumerate(unique.values(), 1):
        checkout = ensure_checkout(case, cache, offline=False)
        print(f"fetched={index}/{len(unique)} {case['repo']}@{case['baseCommit'][:12]} {checkout}", flush=True)


def build_one(args: argparse.Namespace, case: dict, checkout: Path, graph_root: Path) -> dict:
    started = time.perf_counter()
    record = {
        "graphKey": case["graphKey"],
        "repo": case["repo"],
        "repoUrl": case["repoUrl"],
        "baseCommit": case["baseCommit"],
    }
    try:
        root = graph_root / case["graphKey"]
        root.mkdir(parents=True, exist_ok=True)
        ravel_graph = root / "ravel"
        graphify_root = root / "graphify"
        ravel_build_ms = build_ravel(args.ravel, checkout, ravel_graph)
        graphify_graph, extract_ms, cluster_ms = build_graphify(
            args.graphify, checkout, graphify_root
        )
        record.update({
            "status": "ok",
            "ravel": {"graph": str(ravel_graph), "buildMs": ravel_build_ms},
            "graphify": {
                "graph": str(graphify_graph),
                "buildMs": extract_ms + cluster_ms,
                "extractMs": extract_ms,
                "clusterMs": cluster_ms,
            },
        })
    except Exception as error:
        record.update({"status": "error", "error": str(error)})
    record["wallMs"] = (time.perf_counter() - started) * 1000
    return record


def run_case(args: argparse.Namespace, case: dict, graph: dict) -> dict:
    started = time.perf_counter()
    result = {
        "id": case["id"],
        "language": case["language"],
        "repo": case["repo"],
        "baseCommit": case["baseCommit"],
        "graphKey": case["graphKey"],
        "goldSpans": len(case["goldContext"]),
    }
    try:
        question = case["problemStatement"]
        ravel = ravel_result(
            args.ravel, Path(graph["ravel"]["graph"]), question, args.token_budget, args.ravel_profile
        )
        ravel.update(score_span_retrieval(ravel["items"], case["goldContext"]))
        ravel["returned"] = len(ravel["items"])
        ravel["items"] = ravel["items"][: args.keep_items]

        graphify = graphify_result(
            args.graphify, Path(graph["graphify"]["graph"]), question, args.token_budget
        )
        graphify.update(score_span_retrieval(graphify["items"], case["goldContext"]))
        graphify["returned"] = len(graphify["items"])
        graphify["items"] = graphify["items"][: args.keep_items]
        result.update({"status": "ok", "ravel": ravel, "graphify": graphify})
    except Exception as error:
        result.update({"status": "error", "error": str(error)})
    result["wallMs"] = (time.perf_counter() - started) * 1000
    return result


def percentile(values: list[float], fraction: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    return ordered[min(len(ordered) - 1, max(0, math.ceil(len(ordered) * fraction) - 1))]


def summarize(results_path: Path, graphs_path: Path) -> dict:
    latest_rows = {}
    for line in results_path.read_text(encoding="utf-8").splitlines():
        if line.strip():
            row = json.loads(line)
            latest_rows[row["id"]] = row
    rows = list(latest_rows.values())
    latest_graphs = {}
    for line in graphs_path.read_text(encoding="utf-8").splitlines():
        if line.strip():
            graph = json.loads(line)
            latest_graphs[graph["graphKey"]] = graph
    graphs = list(latest_graphs.values())
    ok = [row for row in rows if row.get("status") == "ok"]
    ok_graphs = [row for row in graphs if row.get("status") == "ok"]
    summary = {
        "version": 1,
        "adapterVersion": ADAPTER_VERSION,
        "cases": len(rows),
        "successfulCases": len(ok),
        "failedCases": len(rows) - len(ok),
        "graphs": len(graphs),
        "successfulGraphs": len(ok_graphs),
        "languages": {
            language: sum(row.get("language") == language for row in rows)
            for language in sorted({row.get("language") for row in rows})
        },
    }
    for tool in ("ravel", "graphify"):
        values = [row[tool] for row in ok]
        build_values = [row[tool]["buildMs"] for row in ok_graphs]
        summary[tool] = {
            "fileRecall": statistics.fmean(value["fileRecall"] for value in values) if values else 0.0,
            "filePrecision": statistics.fmean(value["filePrecision"] for value in values) if values else 0.0,
            "fileF1": statistics.fmean(value["fileF1"] for value in values) if values else 0.0,
            "lineRecall": statistics.fmean(value["lineRecall"] for value in values) if values else 0.0,
            "linePrecision": statistics.fmean(value["linePrecision"] for value in values) if values else 0.0,
            "mrr": statistics.fmean(value["reciprocalRank"] for value in values) if values else 0.0,
            "meanEstimatedTokens": statistics.fmean(value["estimatedTokens"] for value in values) if values else 0.0,
            "meanReturned": statistics.fmean(value["returned"] for value in values) if values else 0.0,
            "meanBuildMs": statistics.fmean(build_values) if build_values else 0.0,
            "buildP95Ms": percentile(build_values, 0.95),
            "buildP99Ms": percentile(build_values, 0.99),
            "meanQueryMs": statistics.fmean(value["queryMs"] for value in values) if values else 0.0,
            "queryP95Ms": percentile([value["queryMs"] for value in values], 0.95),
            "queryP99Ms": percentile([value["queryMs"] for value in values], 0.99),
            "truncationRate": statistics.fmean(value["truncated"] for value in values) if values else 0.0,
        }
    return summary


def execute(args: argparse.Namespace) -> None:
    manifest_path = args.manifest.resolve()
    check(argparse.Namespace(manifest=manifest_path))
    cases = selected_cases(args, manifest_path)
    workspace = args.workspace.resolve()
    workspace.mkdir(parents=True, exist_ok=True)
    graph_root = workspace / "graphs"
    graph_root.mkdir(exist_ok=True)
    results_path = workspace / "results.jsonl"
    graphs_path = workspace / "graphs.jsonl"
    run_config_path = workspace / "run-config.json"
    ravel_version = run_command([args.ravel, "version"], workspace)[0].strip()
    graphify_version = run_command([args.graphify, "--version"], workspace)[0].strip()
    run_config = {
        "adapterVersion": ADAPTER_VERSION,
        "manifestSha256": sha256_file(manifest_path),
        "ravelProfile": args.ravel_profile,
        "ravelProfileArgs": list(RAVEL_PROFILES[args.ravel_profile]),
        "tokenBudget": args.token_budget,
        "queryWorkers": args.workers,
        "buildWorkers": 1,
        "ravelVersion": ravel_version,
        "graphifyVersion": graphify_version,
        "ravelExecutable": executable_metadata(args.ravel),
        "graphifyExecutable": executable_metadata(args.graphify),
    }
    if run_config_path.exists() and not args.no_resume:
        if json.loads(run_config_path.read_text(encoding="utf-8")) != run_config:
            raise SystemExit("workspace settings differ; use a new workspace or --no-resume")
    elif (results_path.exists() or graphs_path.exists()) and not args.no_resume:
        raise SystemExit("legacy workspace lacks run-config.json; use a new workspace or --no-resume")
    run_config_path.write_text(json.dumps(run_config, indent=2, sort_keys=True) + "\n", encoding="utf-8")

    graph_records = {}
    if graphs_path.exists() and not args.no_resume:
        for line in graphs_path.read_text(encoding="utf-8").splitlines():
            if line.strip():
                record = json.loads(line)
                graph_records[record["graphKey"]] = record
    unique_cases = {}
    for case in cases:
        unique_cases.setdefault(case["graphKey"], case)
    pending_graphs = [
        case
        for key, case in unique_cases.items()
        if graph_records.get(key, {}).get("status") != "ok"
    ]
    graph_mode = "w" if args.no_resume else "a"
    with graphs_path.open(graph_mode, encoding="utf-8") as graph_output:
        for index, case in enumerate(pending_graphs, 1):
            try:
                checkout = ensure_checkout(case, args.cache.resolve(), args.offline)
                record = build_one(args, case, checkout, graph_root)
            except Exception as error:
                record = {
                    "graphKey": case["graphKey"],
                    "repo": case["repo"],
                    "repoUrl": case["repoUrl"],
                    "baseCommit": case["baseCommit"],
                    "status": "error",
                    "error": str(error),
                }
            graph_records[case["graphKey"]] = record
            graph_output.write(json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n")
            graph_output.flush()
            print(
                f"built={index}/{len(pending_graphs)} status={record['status']} "
                f"repo={case['repo']} commit={case['baseCommit'][:12]}",
                flush=True,
            )

    completed = set()
    if results_path.exists() and not args.no_resume:
        completed = {
            row.get("id")
            for line in results_path.read_text(encoding="utf-8").splitlines()
            if line.strip()
            for row in (json.loads(line),)
            if row.get("status") == "ok"
        }
    pending = [case for case in cases if case["id"] not in completed]
    print(f"Running {len(pending)} pending of {len(cases)} ContextBench cases", flush=True)
    result_mode = "w" if args.no_resume else "a"
    failures = 0
    finished = 0
    with results_path.open(result_mode, encoding="utf-8") as output:
        with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
            futures = {}
            for case in pending:
                graph = graph_records.get(case["graphKey"], {})
                if graph.get("status") != "ok":
                    result = {
                        "id": case["id"],
                        "language": case["language"],
                        "repo": case["repo"],
                        "baseCommit": case["baseCommit"],
                        "graphKey": case["graphKey"],
                        "status": "error",
                        "error": "graph build unavailable: " + str(graph.get("error", "unknown error")),
                    }
                    output.write(json.dumps(result, ensure_ascii=False, sort_keys=True) + "\n")
                    failures += 1
                    finished += 1
                    continue
                futures[pool.submit(run_case, args, case, graph)] = case["id"]
            for future in concurrent.futures.as_completed(futures):
                result = future.result()
                failures += result.get("status") != "ok"
                with RESULT_LOCK:
                    output.write(json.dumps(result, ensure_ascii=False, sort_keys=True) + "\n")
                    output.flush()
                finished += 1
                if finished <= 10 or finished % args.progress_every == 0:
                    print(f"finished={finished}/{len(pending)} failures={failures} last={result['id']}", flush=True)
    summary = summarize(results_path, graphs_path)
    summary.update({
        "manifestSha256": sha256_file(manifest_path),
        "resultsSha256": sha256_file(results_path),
        "graphsSha256": sha256_file(graphs_path),
        "runConfigSha256": sha256_file(run_config_path),
        "tokenBudget": args.token_budget,
        "queryWorkers": args.workers,
        "buildWorkers": 1,
        "ravelProfile": args.ravel_profile,
        "ravelVersion": ravel_version,
        "graphifyVersion": graphify_version,
        "ravelExecutable": executable_metadata(args.ravel),
        "graphifyExecutable": executable_metadata(args.graphify),
        "platform": {"os": os.uname().sysname, "arch": os.uname().machine},
    })
    (workspace / "summary.json").write_text(
        json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    print(json.dumps(summary, indent=2, sort_keys=True))


def add_selection_arguments(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--language", action="append", choices=("go", "typescript"))
    parser.add_argument("--limit", type=int)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    prepare_parser = subparsers.add_parser("prepare", help="select and fingerprint ContextBench cases")
    prepare_parser.add_argument("--parquet", type=Path, required=True)
    prepare_parser.add_argument("--output", type=Path, required=True)
    prepare_parser.add_argument("--language", action="append", choices=("go", "typescript"), default=[])
    prepare_parser.set_defaults(function=prepare)
    check_parser = subparsers.add_parser("check", help="validate a prepared manifest offline")
    check_parser.add_argument("--manifest", type=Path, required=True)
    check_parser.set_defaults(function=check)
    fetch_parser = subparsers.add_parser("fetch", help="cache pinned repositories and commits")
    fetch_parser.add_argument("--manifest", type=Path, required=True)
    fetch_parser.add_argument("--cache", type=Path, required=True)
    add_selection_arguments(fetch_parser)
    fetch_parser.set_defaults(function=fetch)
    run_parser = subparsers.add_parser("run", help="build graphs and run or resume the comparison")
    run_parser.add_argument("--manifest", type=Path, required=True)
    run_parser.add_argument("--cache", type=Path, required=True)
    run_parser.add_argument("--workspace", type=Path, required=True)
    run_parser.add_argument("--ravel", default="ravel")
    run_parser.add_argument("--graphify", default="graphify")
    run_parser.add_argument("--workers", type=int, default=2)
    run_parser.add_argument("--token-budget", type=int, default=2000)
    run_parser.add_argument("--ravel-profile", choices=sorted(RAVEL_PROFILES), default="broad")
    run_parser.add_argument("--keep-items", type=int, default=20)
    run_parser.add_argument("--progress-every", type=int, default=25)
    run_parser.add_argument("--offline", action="store_true")
    run_parser.add_argument("--no-resume", action="store_true")
    add_selection_arguments(run_parser)
    run_parser.set_defaults(function=execute)
    args = parser.parse_args()
    if getattr(args, "command", "") == "prepare" and not args.language:
        parser.error("prepare requires at least one --language")
    if getattr(args, "workers", 1) < 1:
        parser.error("--workers must be positive")
    if getattr(args, "token_budget", 64) < 64:
        parser.error("--token-budget must be at least 64")
    args.function(args)


if __name__ == "__main__":
    main()
