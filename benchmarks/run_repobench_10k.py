#!/usr/bin/env python3
"""Prepare and run a 10,000-case Ravel/Graphify RepoBench comparison.

RepoBench's cross-file-first split supplies a candidate snippet corpus, a gold
snippet index, the incomplete target file, and its next line. This adapter
materializes each case as an isolated miniature repository, asks both tools
which cross-file definition is needed, and scores gold identifier/path recall
and reciprocal rank. It never sends source or answers to a model.
"""

from __future__ import annotations

import argparse
import ast
import concurrent.futures
import hashlib
import json
import math
import os
from pathlib import Path, PurePosixPath
import re
import statistics
import subprocess
import tempfile
import threading
import time
from typing import Iterable, Iterator


ADAPTER_VERSION = "repobench-cross-file-first-v1"
SELECTION_SEED = "ravel-graphify-repobench-10000-v1"
LANGUAGE_COUNTS = {"python": 5000, "java": 5000}
SCORE_NORMALIZER = re.compile(r"[^a-z0-9]+")
RESULT_LOCK = threading.Lock()
DEFAULT_RAVEL_PROFILE = "broad"
RAVEL_PROFILES = {
    "compact": (),
    # Match Graphify's broad depth-3 traversal on these small fragment corpora.
    # Ravel still keeps its normal compact defaults outside this benchmark.
    "broad": (
        "--seed-limit", "20",
        "--max-depth", "3",
        "--max-nodes", "10000",
        "--branch-fanout", "10000",
        "--candidate-shortlist",
    ),
}


def require_pyarrow():
    try:
        import pyarrow.parquet as parquet
    except ImportError as error:
        raise SystemExit(
            "PyArrow is required for RepoBench Parquet files. Install it in an isolated "
            "environment, then run this script with that environment's Python."
        ) from error
    return parquet


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def normalized(value: str) -> str:
    return SCORE_NORMALIZER.sub("", value.lower())


def stable_case_id(language: str, row: dict) -> str:
    values = (
        language,
        str(row.get("repo_name", "")),
        str(row.get("file_path", "")),
        str(row.get("created_at", "")),
        str(row.get("next_line", "")),
        str(row.get("cropped_code", "")),
    )
    return hashlib.sha256("\0".join(values).encode("utf-8")).hexdigest()[:24]


def selection_key(case_id: str) -> str:
    return hashlib.sha256(f"{SELECTION_SEED}\0{case_id}".encode("utf-8")).hexdigest()


def load_source_rows(source: Path, columns: list[str]) -> Iterator[tuple[int, dict]]:
    parquet = require_pyarrow()
    row_index = 0
    for batch in parquet.ParquetFile(source).iter_batches(batch_size=128, columns=columns):
        for row in batch.to_pylist():
            yield row_index, row
            row_index += 1


def prepare(args: argparse.Namespace) -> None:
    data_root = args.data_root.resolve()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    source_paths = {
        "python": sorted((data_root / "python").glob("*.parquet")),
        "java": sorted((data_root / "java").glob("*.parquet")),
    }
    if any(not paths for paths in source_paths.values()):
        raise SystemExit("data root must contain python/*.parquet and java/*.parquet")

    sources: list[dict] = []
    candidates: dict[str, list[dict]] = {language: [] for language in LANGUAGE_COUNTS}
    seen: set[str] = set()
    columns = [
        "repo_name", "file_path", "context", "cropped_code", "next_line", "created_at", "level"
    ]
    parquet = require_pyarrow()
    for language, paths in source_paths.items():
        for path in paths:
            relative = path.relative_to(data_root).as_posix()
            row_count = parquet.ParquetFile(path).metadata.num_rows
            metadata_columns = columns + ["gold_snippet_index"]
            for row_index, row in load_source_rows(path, metadata_columns):
                context = row.get("context")
                gold_index = row.get("gold_snippet_index")
                if not isinstance(context, list) or not isinstance(gold_index, int):
                    continue
                if gold_index < 0 or gold_index >= len(context):
                    continue
                gold = context[gold_index]
                if not isinstance(gold, dict):
                    continue
                case_id = stable_case_id(language, row)
                if case_id in seen:
                    continue
                seen.add(case_id)
                candidates[language].append({
                    "id": case_id,
                    "language": language,
                    "source": relative,
                    "row": row_index,
                    "repo": row.get("repo_name", ""),
                    "filePath": row.get("file_path", ""),
                    "createdAt": row.get("created_at", ""),
                    "level": row.get("level", ""),
                    "contextCount": len(context),
                    "goldSnippetIndex": gold_index,
                    "goldIdentifier": str(gold.get("identifier", "")),
                    "goldPath": str(gold.get("path", "")),
                    "selectionKey": selection_key(case_id),
                })
            sources.append({
                "language": language,
                "path": relative,
                "rows": row_count,
                "bytes": path.stat().st_size,
                "sha256": sha256_file(path),
            })

    selected: list[dict] = []
    available = {}
    for language, count in LANGUAGE_COUNTS.items():
        ordered = sorted(candidates[language], key=lambda item: (item["selectionKey"], item["id"]))
        available[language] = len(ordered)
        if len(ordered) < count:
            raise SystemExit(f"{language}: only {len(ordered)} valid unique cases; need {count}")
        selected.extend(ordered[:count])
    selected.sort(key=lambda item: (item["language"], item["selectionKey"], item["id"]))

    cases_path = output / "cases.jsonl"
    with cases_path.open("w", encoding="utf-8") as handle:
        for item in selected:
            handle.write(json.dumps(item, ensure_ascii=False, sort_keys=True) + "\n")
    manifest = {
        "version": 1,
        "benchmark": "RepoBench v1.1 cross_file_first",
        "adapterVersion": ADAPTER_VERSION,
        "selectionSeed": SELECTION_SEED,
        "license": "CC-BY-4.0",
        "cases": len(selected),
        "languageCounts": LANGUAGE_COUNTS,
        "availableUniqueCases": available,
        "casesSha256": sha256_file(cases_path),
        "sources": sources,
    }
    (output / "manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    print(f"Prepared {len(selected)} unique cases in {output}")
    print(json.dumps({"available": available, "selected": LANGUAGE_COUNTS}, indent=2))


def read_cases(path: Path) -> list[dict]:
    cases = []
    seen = set()
    for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if not line.strip():
            continue
        item = json.loads(line)
        if not isinstance(item, dict) or not isinstance(item.get("id"), str):
            raise ValueError(f"{path}:{line_number}: invalid case")
        if item["id"] in seen:
            raise ValueError(f"{path}:{line_number}: duplicate id {item['id']}")
        seen.add(item["id"])
        cases.append(item)
    return cases


def check(args: argparse.Namespace) -> None:
    manifest_path = args.manifest.resolve()
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    cases_path = manifest_path.parent / "cases.jsonl"
    cases = read_cases(cases_path)
    if manifest.get("version") != 1 or manifest.get("adapterVersion") != ADAPTER_VERSION:
        raise SystemExit("unsupported manifest version or adapter")
    if manifest.get("cases") != len(cases):
        raise SystemExit("manifest case count does not match cases.jsonl")
    if manifest.get("casesSha256") != sha256_file(cases_path):
        raise SystemExit("cases.jsonl hash mismatch")
    language_counts = {language: sum(c["language"] == language for c in cases) for language in LANGUAGE_COUNTS}
    if language_counts != LANGUAGE_COUNTS:
        raise SystemExit(f"language counts do not match contract: {language_counts}")
    print(f"Validated {len(cases)} unique RepoBench cases: {language_counts}")


def safe_relative(value: str, fallback: str) -> Path:
    value = value.replace("\\", "/").lstrip("/")
    parts = [part for part in PurePosixPath(value).parts if part not in ("", ".", "..")]
    path = Path(*parts) if parts else Path(fallback)
    return path


def materialize(repo: Path, row: dict, language: str) -> list[Path]:
    suffix = ".py" if language == "python" else ".java"
    contexts = row["context"]
    written = []
    for index, snippet in enumerate(contexts):
        original = safe_relative(str(snippet.get("path", "")), f"snippet{suffix}")
        if not original.suffix:
            original = original.with_suffix(suffix)
        path = repo / "contexts" / f"{index:03d}" / original
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(str(snippet.get("snippet", "")) + "\n", encoding="utf-8")
        written.append(path)
    target = safe_relative(str(row.get("file_path", "")), f"target{suffix}")
    if not target.suffix:
        target = target.with_suffix(suffix)
    target_path = repo / "target" / target
    target_path.parent.mkdir(parents=True, exist_ok=True)
    target_path.write_text(
        str(row.get("import_statement", "")).rstrip()
        + "\n\n"
        + str(row.get("cropped_code", "")).rstrip()
        + "\n",
        encoding="utf-8",
    )
    return written


def query_text(row: dict) -> str:
    imports = str(row.get("import_statement", ""))[-1800:]
    code = str(row.get("cropped_code", ""))[-2200:]
    return (
        f"Which cross-file definition is needed to complete the next line of {row.get('file_path', '')}?\n"
        f"Imports:\n{imports}\nCode immediately before the cursor:\n{code}"
    )


def run_command(command: list[str], cwd: Path) -> tuple[str, float]:
    started = time.perf_counter()
    result = subprocess.run(command, cwd=cwd, text=True, capture_output=True, check=False)
    elapsed_ms = (time.perf_counter() - started) * 1000
    if result.returncode != 0:
        raise RuntimeError(
            f"command failed ({result.returncode}): {' '.join(command)}\n"
            f"{result.stdout[-2000:]}{result.stderr[-2000:]}"
        )
    return result.stdout, elapsed_ms


def ravel_result(binary: str, graph: Path, question: str, budget: int, profile: str) -> dict:
    output, query_ms = run_command(
        [
            binary, "context", "--json", "--out", str(graph),
            "--token-budget", str(budget), *RAVEL_PROFILES[profile], question,
        ],
        graph.parent,
    )
    value = json.loads(output)
    nodes = value.get("nodes") or []
    stats = value.get("stats") or {}
    return {
        "queryMs": query_ms,
        "profile": profile,
        "estimatedTokens": stats.get("estimatedTokens", 0),
        "headerTokens": stats.get("headerTokens", 0),
        "candidateTokens": stats.get("candidateTokens", 0),
        "explanationTokens": stats.get("explanationTokens", 0),
        "truncated": bool(stats.get("truncated")),
        "truncatedReasons": [str(reason) for reason in stats.get("truncatedReason") or []],
        "exploredNodes": stats.get("exploredNodes", 0),
        "lexicalCandidates": stats.get("lexicalCandidates", 0),
        "deduplicatedNodes": stats.get("deduplicatedNodes", 0),
        "unselectedNodes": stats.get("unselectedNodes", 0),
        "explanationEdgesOmitted": stats.get("explanationEdgesOmitted", 0),
        "branchFanout": stats.get("branchFanout", 0),
        "branchesPruned": stats.get("branchesPruned", 0),
        "items": [
            {"name": str(node.get("name", "")), "path": str(node.get("path", ""))}
            for node in nodes if isinstance(node, dict)
        ],
    }


def graphify_result(binary: str, graph: Path, question: str, budget: int) -> dict:
    output, query_ms = run_command(
        [binary, "query", question, "--budget", str(budget), "--graph", str(graph)], graph.parent
    )
    items = []
    for line in output.splitlines():
        if not line.startswith("NODE "):
            continue
        label = line[5:].split(" [", 1)[0]
        match = re.search(r"\[src=(.+?)\s+loc=L\d+", line)
        items.append({"name": label, "path": match.group(1) if match else ""})
    first_line = output.splitlines()[0] if output else ""
    start_items = []
    if "Start: " in first_line:
        raw = first_line.split("Start: ", 1)[1].split(" |", 1)[0]
        try:
            starts = ast.literal_eval(raw)
        except (SyntaxError, ValueError):
            starts = []
        if isinstance(starts, list):
            for label in starts:
                start_items.append({"name": str(label), "path": ""})
    start_names = {normalized(item["name"]) for item in start_items}
    items = start_items + [item for item in items if normalized(item["name"]) not in start_names]
    return {
        "queryMs": query_ms,
        "estimatedTokens": math.ceil(len(output.encode("utf-8")) / 3),
        "truncated": "truncated" in output.lower(),
        "items": items,
    }


def score(items: list[dict], gold_identifier: str, gold_materialized: Path) -> tuple[bool, int | None]:
    expected_name = normalized(gold_identifier)
    expected_suffix = gold_materialized.as_posix()
    for rank, item in enumerate(items, 1):
        name_hit = bool(expected_name) and normalized(item.get("name", "")) == expected_name
        path = str(item.get("path", "")).replace("\\", "/")
        path_hit = bool(path) and path.endswith(expected_suffix)
        if name_hit or path_hit:
            return True, rank
    return False, None


def run_case(args: argparse.Namespace, case: dict, row: dict, temp_root: Path) -> dict:
    started = time.perf_counter()
    result = {
        "id": case["id"],
        "language": case["language"],
        "repo": case["repo"],
        "level": case["level"],
        "contextCount": case["contextCount"],
        "goldIdentifier": case["goldIdentifier"],
        "goldPath": case["goldPath"],
    }
    try:
        with tempfile.TemporaryDirectory(prefix=f"case-{case['id']}-", dir=temp_root) as temporary:
            root = Path(temporary)
            repo = root / "repo"
            repo.mkdir()
            context_paths = materialize(repo, row, case["language"])
            gold_index = case["goldSnippetIndex"]
            if gold_index < 0 or gold_index >= len(context_paths):
                raise RuntimeError("gold snippet index is outside materialized contexts")
            gold_materialized = context_paths[gold_index].relative_to(repo)
            question = query_text(row)

            ravel_graph = root / "ravel"
            _, ravel_build_ms = run_command(
                [args.ravel, "build", "--out", str(ravel_graph), str(repo)], root
            )
            ravel = ravel_result(
                args.ravel, ravel_graph, question, args.token_budget, args.ravel_profile
            )
            ravel_hit, ravel_rank = score(ravel["items"], case["goldIdentifier"], gold_materialized)
            ravel.update({
                "buildMs": ravel_build_ms,
                "hit": ravel_hit,
                "rank": ravel_rank,
                "reciprocalRank": 0.0 if ravel_rank is None else 1.0 / ravel_rank,
                "returned": len(ravel["items"]),
            })
            ravel["items"] = ravel["items"][: args.keep_items]

            graphify_root = root / "graphify"
            _, graphify_extract_ms = run_command(
                [
                    args.graphify, "extract", str(repo), "--code-only", "--no-cluster",
                    "--max-workers", "1", "--out", str(graphify_root),
                ],
                root,
            )
            graphify_graph = graphify_root / "graphify-out" / "graph.json"
            _, graphify_cluster_ms = run_command(
                [
                    args.graphify, "cluster-only", str(graphify_root), "--graph", str(graphify_graph),
                    "--no-label", "--no-viz",
                ],
                root,
            )
            graphify = graphify_result(args.graphify, graphify_graph, question, args.token_budget)
            graphify_hit, graphify_rank = score(
                graphify["items"], case["goldIdentifier"], gold_materialized
            )
            graphify.update({
                "buildMs": graphify_extract_ms + graphify_cluster_ms,
                "extractMs": graphify_extract_ms,
                "clusterMs": graphify_cluster_ms,
                "hit": graphify_hit,
                "rank": graphify_rank,
                "reciprocalRank": 0.0 if graphify_rank is None else 1.0 / graphify_rank,
                "returned": len(graphify["items"]),
            })
            graphify["items"] = graphify["items"][: args.keep_items]
            result.update({"status": "ok", "ravel": ravel, "graphify": graphify})
    except Exception as error:
        result.update({"status": "error", "error": str(error)})
    result["wallMs"] = (time.perf_counter() - started) * 1000
    return result


def selected_rows(data_root: Path, cases: list[dict]) -> Iterator[tuple[dict, dict]]:
    by_source: dict[str, dict[int, dict]] = {}
    for case in cases:
        by_source.setdefault(case["source"], {})[case["row"]] = case
    columns = [
        "repo_name", "file_path", "context", "import_statement", "cropped_code", "next_line",
        "gold_snippet_index", "created_at", "level",
    ]
    for source, wanted in sorted(by_source.items()):
        path = (data_root / source).resolve()
        try:
            path.relative_to(data_root)
        except ValueError as error:
            raise RuntimeError(f"source escapes data root: {source}") from error
        for row_index, row in load_source_rows(path, columns):
            case = wanted.get(row_index)
            if case is not None:
                if stable_case_id(case["language"], row) != case["id"]:
                    raise RuntimeError(f"case fingerprint mismatch: {case['id']}")
                yield case, row


def percentile(values: list[float], fraction: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    return ordered[min(len(ordered) - 1, max(0, math.ceil(len(ordered) * fraction) - 1))]


def summarize_results(results_path: Path, output_path: Path) -> dict:
    rows = [json.loads(line) for line in results_path.read_text(encoding="utf-8").splitlines() if line.strip()]
    ok = [row for row in rows if row.get("status") == "ok"]
    summary = {
        "version": 1,
        "adapterVersion": ADAPTER_VERSION,
        "cases": len(rows),
        "successfulCases": len(ok),
        "failedCases": len(rows) - len(ok),
        "languages": {
            language: sum(row.get("language") == language for row in rows)
            for language in LANGUAGE_COUNTS
        },
    }
    for tool in ("ravel", "graphify"):
        values = [row[tool] for row in ok]
        tool_summary = {
            "recall": statistics.fmean(value["hit"] for value in values) if values else 0.0,
            "mrr": statistics.fmean(value["reciprocalRank"] for value in values) if values else 0.0,
            "meanEstimatedTokens": statistics.fmean(value["estimatedTokens"] for value in values) if values else 0.0,
            "meanReturned": statistics.fmean(value["returned"] for value in values) if values else 0.0,
            "meanBuildMs": statistics.fmean(value["buildMs"] for value in values) if values else 0.0,
            "buildP50Ms": percentile([value["buildMs"] for value in values], 0.50),
            "buildP95Ms": percentile([value["buildMs"] for value in values], 0.95),
            "buildP99Ms": percentile([value["buildMs"] for value in values], 0.99),
            "meanQueryMs": statistics.fmean(value["queryMs"] for value in values) if values else 0.0,
            "queryP50Ms": percentile([value["queryMs"] for value in values], 0.50),
            "queryP95Ms": percentile([value["queryMs"] for value in values], 0.95),
            "queryP99Ms": percentile([value["queryMs"] for value in values], 0.99),
            "truncationRate": statistics.fmean(value["truncated"] for value in values) if values else 0.0,
        }
        if tool == "ravel":
            tool_summary.update({
                "meanHeaderTokens": statistics.fmean(value.get("headerTokens", 0) for value in values) if values else 0.0,
                "meanCandidateTokens": statistics.fmean(value.get("candidateTokens", 0) for value in values) if values else 0.0,
                "meanExplanationTokens": statistics.fmean(value.get("explanationTokens", 0) for value in values) if values else 0.0,
                "meanUnselectedNodes": statistics.fmean(value.get("unselectedNodes", 0) for value in values) if values else 0.0,
                "shortlistSelectionRate": statistics.fmean(value.get("unselectedNodes", 0) > 0 for value in values) if values else 0.0,
                "meanDeduplicatedNodes": statistics.fmean(value.get("deduplicatedNodes", 0) for value in values) if values else 0.0,
                "meanExplanationEdgesOmitted": statistics.fmean(value.get("explanationEdgesOmitted", 0) for value in values) if values else 0.0,
            })
        summary[tool] = tool_summary
    output_path.write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    return summary


def execute(args: argparse.Namespace) -> None:
    manifest_path = args.manifest.resolve()
    check(argparse.Namespace(manifest=manifest_path))
    cases = read_cases(manifest_path.parent / "cases.jsonl")
    if args.language:
        cases = [case for case in cases if case["language"] == args.language]
    if args.limit:
        cases = cases[: args.limit]
    data_root = args.data_root.resolve()
    workspace = args.workspace.resolve()
    workspace.mkdir(parents=True, exist_ok=True)
    temp_root = workspace / "tmp"
    temp_root.mkdir(exist_ok=True)
    results_path = workspace / "results.jsonl"
    run_config_path = workspace / "run-config.json"
    run_config = {
        "adapterVersion": ADAPTER_VERSION,
        "manifestSha256": sha256_file(manifest_path),
        "ravelProfile": args.ravel_profile,
        "ravelProfileArgs": list(RAVEL_PROFILES[args.ravel_profile]),
        "tokenBudget": args.token_budget,
    }
    if run_config_path.exists() and not args.no_resume:
        existing_config = json.loads(run_config_path.read_text(encoding="utf-8"))
        if existing_config != run_config:
            raise SystemExit(
                "workspace retrieval settings do not match this run; use a new workspace "
                "or pass --no-resume"
            )
    elif results_path.exists() and not args.no_resume:
        raise SystemExit(
            "workspace has legacy results without run-config.json; use a new workspace "
            "or pass --no-resume"
        )
    run_config_path.write_text(
        json.dumps(run_config, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    completed = set()
    if results_path.exists() and not args.no_resume:
        for line in results_path.read_text(encoding="utf-8").splitlines():
            if line.strip():
                completed.add(json.loads(line).get("id"))
    pending_cases = [case for case in cases if case["id"] not in completed]
    pending_ids = {case["id"] for case in pending_cases}
    print(
        f"Running {len(pending_cases)} pending of {len(cases)} cases with {args.workers} workers "
        f"(resumed {len(cases) - len(pending_cases)})",
        flush=True,
    )

    row_iter = (
        pair for pair in selected_rows(data_root, pending_cases) if pair[0]["id"] in pending_ids
    )
    submitted = 0
    finished = 0
    failures = 0
    mode = "w" if args.no_resume else "a"
    with results_path.open(mode, encoding="utf-8") as output:
        with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
            active: dict[concurrent.futures.Future, str] = {}

            def fill() -> None:
                nonlocal submitted
                while len(active) < args.workers * 2:
                    try:
                        case, row = next(row_iter)
                    except StopIteration:
                        return
                    future = pool.submit(run_case, args, case, row, temp_root)
                    active[future] = case["id"]
                    submitted += 1

            fill()
            while active:
                done, _ = concurrent.futures.wait(active, return_when=concurrent.futures.FIRST_COMPLETED)
                for future in done:
                    active.pop(future)
                    result = future.result()
                    if result.get("status") != "ok":
                        failures += 1
                    with RESULT_LOCK:
                        output.write(json.dumps(result, ensure_ascii=False, sort_keys=True) + "\n")
                        output.flush()
                    finished += 1
                    if finished <= 10 or finished % args.progress_every == 0:
                        print(
                            f"finished={finished}/{len(pending_cases)} failures={failures} "
                            f"last={result['id']} status={result['status']}",
                            flush=True,
                        )
                fill()
    if submitted != len(pending_cases):
        raise RuntimeError(f"loaded {submitted} pending rows, expected {len(pending_cases)}")
    summary = summarize_results(results_path, workspace / "summary.json")
    summary.update({
        "manifestSha256": sha256_file(manifest_path),
        "resultsSha256": sha256_file(results_path),
        "tokenBudget": args.token_budget,
        "ravelProfile": args.ravel_profile,
        "ravelProfileArgs": list(RAVEL_PROFILES[args.ravel_profile]),
        "runConfigSha256": sha256_file(run_config_path),
        "ravelVersion": run_command([args.ravel, "version"], workspace)[0].strip(),
        "graphifyVersion": run_command([args.graphify, "--version"], workspace)[0].strip(),
        "platform": {"os": os.uname().sysname, "arch": os.uname().machine},
    })
    (workspace / "summary.json").write_text(
        json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    print(json.dumps(summary, indent=2, sort_keys=True), flush=True)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)

    prepare_parser = subparsers.add_parser("prepare", help="select and fingerprint 10,000 cases")
    prepare_parser.add_argument("--data-root", type=Path, required=True)
    prepare_parser.add_argument("--output", type=Path, required=True)
    prepare_parser.set_defaults(function=prepare)

    check_parser = subparsers.add_parser("check", help="validate a prepared manifest offline")
    check_parser.add_argument("--manifest", type=Path, required=True)
    check_parser.set_defaults(function=check)

    run_parser = subparsers.add_parser("run", help="run or resume the comparison")
    run_parser.add_argument("--manifest", type=Path, required=True)
    run_parser.add_argument("--data-root", type=Path, required=True)
    run_parser.add_argument("--workspace", type=Path, required=True)
    run_parser.add_argument("--ravel", default="ravel")
    run_parser.add_argument("--graphify", default="graphify")
    run_parser.add_argument("--workers", type=int, default=2)
    run_parser.add_argument("--token-budget", type=int, default=2000)
    run_parser.add_argument(
        "--ravel-profile", choices=sorted(RAVEL_PROFILES), default=DEFAULT_RAVEL_PROFILE,
        help="Ravel retrieval breadth: broad matches Graphify-style traversal; compact uses defaults",
    )
    run_parser.add_argument("--keep-items", type=int, default=20)
    run_parser.add_argument("--limit", type=int)
    run_parser.add_argument("--language", choices=sorted(LANGUAGE_COUNTS))
    run_parser.add_argument("--progress-every", type=int, default=100)
    run_parser.add_argument("--no-resume", action="store_true")
    run_parser.set_defaults(function=execute)

    args = parser.parse_args()
    if getattr(args, "workers", 1) < 1:
        parser.error("--workers must be positive")
    if getattr(args, "token_budget", 64) < 64:
        parser.error("--token-budget must be at least 64")
    args.function(args)


if __name__ == "__main__":
    main()
