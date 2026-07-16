#!/usr/bin/env python3
"""Run a TypeScript/Go Ravel-vs-Graphify scale test on Real-FIM-Eval.

Real-FIM-Eval is a model infilling benchmark and has no gold retrieval spans.
This adapter therefore measures tool stability, payload, and build/query tail
latency only. It never claims retrieval or answer correctness. Each tool sees
the same single-file pre-change corpus and a query built only from the public
prefix, suffix, path, and (for Edit cases) the code being replaced. The hidden
canonical solution is fingerprinted but never materialized or queried.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import gzip
import hashlib
import json
import math
import os
from pathlib import Path, PurePosixPath
import statistics
import tempfile
import threading
import time
from typing import Iterator

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


ADAPTER_VERSION = "real-fim-typescript-go-scale-v1"
QUERY_MODE = "prefix-suffix-local-context-v1"
LANGUAGES = ("go", "typescript")
SPLITS = ("add", "edit")
RESULT_LOCK = threading.Lock()


def safe_relative(value: str, fallback: str, language: str) -> Path:
    value = value.replace("\\", "/").lstrip("/")
    parts = [part for part in PurePosixPath(value).parts if part not in ("", ".", "..")]
    path = Path(*parts) if parts else Path(fallback)
    if path.suffix:
        return path
    return path.with_suffix(".go" if language == "go" else ".ts")


def source_paths(args: argparse.Namespace) -> dict[str, Path]:
    return {"add": args.add.resolve(), "edit": args.edit.resolve()}


def open_rows(path: Path) -> Iterator[tuple[int, dict]]:
    with gzip.open(path, "rt", encoding="utf-8") as handle:
        for row, line in enumerate(handle):
            if line.strip():
                yield row, json.loads(line)


def stable_case_id(split: str, row: dict) -> str:
    return stable_hash(
        "real-fim-eval",
        split,
        row.get("repo", ""),
        row.get("ref", ""),
        row.get("path", ""),
        row.get("timestamp", ""),
        row.get("prompt", ""),
        row.get("suffix", ""),
        row.get("to_remove", ""),
        row.get("canonical_solution", ""),
    )


def selection_key(case_id: str) -> str:
    return stable_hash("ravel-graphify-real-fim-scale-v1", case_id, length=64)


def prepare(args: argparse.Namespace) -> None:
    paths = source_paths(args)
    missing = [str(path) for path in paths.values() if not path.is_file()]
    if missing:
        raise SystemExit("missing Real-FIM source files: " + ", ".join(missing))
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    cases = []
    seen = set()
    source_rows = {}
    for split, path in paths.items():
        rows = 0
        for row_number, row in open_rows(path):
            rows = row_number + 1
            language = str(row.get("lang", "")).lower()
            if language not in LANGUAGES:
                continue
            required = ("repo", "ref", "path", "prompt", "suffix", "canonical_solution")
            if any(not isinstance(row.get(key), str) for key in required):
                raise RuntimeError(f"{path}:{row_number + 1}: malformed Real-FIM row")
            case_id = stable_case_id(split, row)
            if case_id in seen:
                raise RuntimeError(f"duplicate Real-FIM case id: {case_id}")
            seen.add(case_id)
            solution = row["canonical_solution"]
            cases.append({
                "id": case_id,
                "selectionKey": selection_key(case_id),
                "split": split,
                "language": language,
                "source": path.name,
                "sourceRow": row_number,
                "repo": row["repo"],
                "ref": row["ref"],
                "path": row["path"],
                "timestamp": str(row.get("timestamp", "")),
                "promptChars": len(row["prompt"]),
                "suffixChars": len(row["suffix"]),
                "removedChars": len(str(row.get("to_remove", ""))),
                "solutionChars": len(solution),
                "solutionSha256": hashlib.sha256(solution.encode("utf-8")).hexdigest(),
            })
        source_rows[split] = rows
    cases.sort(key=lambda case: (case["selectionKey"], case["id"]))
    cases_path = output / "cases.jsonl"
    with cases_path.open("w", encoding="utf-8") as handle:
        for case in cases:
            handle.write(json.dumps(case, ensure_ascii=False, sort_keys=True) + "\n")
    languages = {
        language: sum(case["language"] == language for case in cases)
        for language in LANGUAGES
    }
    splits = {split: sum(case["split"] == split for case in cases) for split in SPLITS}
    language_splits = {
        f"{language}:{split}": sum(
            case["language"] == language and case["split"] == split for case in cases
        )
        for language in LANGUAGES
        for split in SPLITS
    }
    manifest = {
        "version": 1,
        "benchmark": "Real-FIM-Eval TypeScript/Go scale compatibility",
        "adapterVersion": ADAPTER_VERSION,
        "commonAdapterVersion": COMMON_ADAPTER_VERSION,
        "queryMode": QUERY_MODE,
        "officialMetric": False,
        "claimsCorrectness": False,
        "cases": len(cases),
        "languages": languages,
        "splits": splits,
        "languageSplits": language_splits,
        "uniqueRepositories": len({case["repo"] for case in cases}),
        "uniqueRepositoryRefs": len({(case["repo"], case["ref"]) for case in cases}),
        "casesSha256": sha256_file(cases_path),
        "sources": [
            {
                "split": split,
                "path": path.name,
                "rows": source_rows[split],
                "bytes": path.stat().st_size,
                "sha256": sha256_file(path),
            }
            for split, path in paths.items()
        ],
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
        raise SystemExit("unsupported Real-FIM manifest version or adapter")
    if manifest.get("cases") != len(cases) or manifest.get("casesSha256") != sha256_file(cases_path):
        raise SystemExit("Real-FIM manifest count or cases hash mismatch")
    languages = {
        language: sum(case["language"] == language for case in cases)
        for language in LANGUAGES
    }
    splits = {split: sum(case["split"] == split for case in cases) for split in SPLITS}
    if manifest.get("languages") != languages or manifest.get("splits") != splits:
        raise SystemExit(f"Real-FIM language/split counts mismatch: {languages} {splits}")
    if len(cases) != 5769 or languages != {"go": 2587, "typescript": 3182}:
        raise SystemExit(f"unexpected official TypeScript/Go case contract: {languages}, total={len(cases)}")
    print(f"Validated {len(cases)} Real-FIM scale cases: {languages}, {splits}")


def selected_cases(args: argparse.Namespace, manifest_path: Path) -> list[dict]:
    cases = read_cases(manifest_path.parent / "cases.jsonl")
    if args.language:
        cases = [case for case in cases if case["language"] in args.language]
    if args.split:
        cases = [case for case in cases if case["split"] in args.split]
    if args.limit:
        cases = cases[: args.limit]
    return cases


def verify_sources(manifest: dict, paths: dict[str, Path]) -> None:
    expected = {source["split"]: source for source in manifest.get("sources", [])}
    for split, path in paths.items():
        source = expected.get(split)
        if source is None:
            raise SystemExit(f"manifest lacks the {split} source fingerprint")
        if not path.is_file():
            raise SystemExit(f"missing Real-FIM {split} source: {path}")
        if path.stat().st_size != source.get("bytes") or sha256_file(path) != source.get("sha256"):
            raise SystemExit(f"Real-FIM {split} source fingerprint mismatch: {path}")


def selected_rows(paths: dict[str, Path], cases: list[dict]) -> Iterator[tuple[dict, dict]]:
    wanted: dict[str, dict[int, dict]] = {}
    for case in cases:
        wanted.setdefault(case["split"], {})[case["sourceRow"]] = case
    for split in SPLITS:
        rows = wanted.get(split, {})
        if not rows:
            continue
        for row_number, row in open_rows(paths[split]):
            case = rows.get(row_number)
            if case is None:
                continue
            if stable_case_id(split, row) != case["id"]:
                raise RuntimeError(f"Real-FIM case fingerprint mismatch: {case['id']}")
            yield case, row


def materialized_source(row: dict, split: str, language: str) -> str:
    marker = "// <REAL_FIM_HOLE>"
    prompt = str(row.get("prompt", "")).rstrip()
    suffix = str(row.get("suffix", "")).lstrip()
    if split == "edit":
        removed = str(row.get("to_remove", "")).strip()
        middle = f"{marker}\n{removed}\n// </REAL_FIM_HOLE>" if removed else marker
    else:
        middle = marker
    return f"{prompt}\n{middle}\n{suffix}\n"


def materialize(repository: Path, case: dict, row: dict) -> Path:
    target = repository / safe_relative(case["path"], "target", case["language"])
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(
        materialized_source(row, case["split"], case["language"]),
        encoding="utf-8",
    )
    return target


def query_text(case: dict, row: dict) -> str:
    prompt = str(row.get("prompt", ""))[-3000:]
    suffix = str(row.get("suffix", ""))[:1800]
    removed = str(row.get("to_remove", ""))[:800] if case["split"] == "edit" else ""
    replacement = f"\nExisting code being replaced:\n{removed}" if removed else ""
    return (
        f"Which local definitions and types in {case['path']} are relevant to completing the "
        f"missing {case['language']} code?\n"
        f"Code before the hole:\n{prompt}{replacement}\nCode after the hole:\n{suffix}"
    )


def target_returned(items: list[dict], target: Path, repository: Path) -> bool:
    relative = target.relative_to(repository).as_posix()
    return any(paths_match(str(item.get("path", "")), relative) for item in items)


def run_case(args: argparse.Namespace, case: dict, row: dict, temp_root: Path) -> dict:
    started = time.perf_counter()
    result = {
        "id": case["id"],
        "split": case["split"],
        "language": case["language"],
        "repo": case["repo"],
        "ref": case["ref"],
        "path": case["path"],
        "promptChars": case["promptChars"],
        "suffixChars": case["suffixChars"],
        "removedChars": case["removedChars"],
        "solutionChars": case["solutionChars"],
        "solutionSha256": case["solutionSha256"],
    }
    try:
        with tempfile.TemporaryDirectory(prefix=f"real-fim-{case['id']}-", dir=temp_root) as temporary:
            root = Path(temporary)
            repository = root / "repo"
            repository.mkdir()
            target = materialize(repository, case, row)
            question = query_text(case, row)

            ravel_graph = root / "ravel"
            ravel_build_ms = build_ravel(args.ravel, repository, ravel_graph)
            ravel = ravel_result(args.ravel, ravel_graph, question, args.token_budget, args.ravel_profile)
            ravel.update({
                "buildMs": ravel_build_ms,
                "returned": len(ravel["items"]),
                "nonEmpty": bool(ravel["items"]),
                "targetFileReturned": target_returned(ravel["items"], target, repository),
            })
            ravel["items"] = ravel["items"][: args.keep_items]

            graphify_root = root / "graphify"
            graphify_graph, extract_ms, cluster_ms = build_graphify(
                args.graphify, repository, graphify_root
            )
            graphify = graphify_result(args.graphify, graphify_graph, question, args.token_budget)
            graphify.update({
                "buildMs": extract_ms + cluster_ms,
                "extractMs": extract_ms,
                "clusterMs": cluster_ms,
                "returned": len(graphify["items"]),
                "nonEmpty": bool(graphify["items"]),
                "targetFileReturned": target_returned(graphify["items"], target, repository),
            })
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


def tool_summary(values: list[dict]) -> dict:
    build = [value["buildMs"] for value in values]
    query = [value["queryMs"] for value in values]
    return {
        "nonEmptyRate": statistics.fmean(value["nonEmpty"] for value in values) if values else 0.0,
        "targetFileRate": statistics.fmean(value["targetFileReturned"] for value in values) if values else 0.0,
        "meanEstimatedTokens": statistics.fmean(value["estimatedTokens"] for value in values) if values else 0.0,
        "meanReturned": statistics.fmean(value["returned"] for value in values) if values else 0.0,
        "meanBuildMs": statistics.fmean(build) if build else 0.0,
        "buildP50Ms": percentile(build, 0.50),
        "buildP95Ms": percentile(build, 0.95),
        "buildP99Ms": percentile(build, 0.99),
        "buildMaxMs": max(build) if build else 0.0,
        "meanQueryMs": statistics.fmean(query) if query else 0.0,
        "queryP50Ms": percentile(query, 0.50),
        "queryP95Ms": percentile(query, 0.95),
        "queryP99Ms": percentile(query, 0.99),
        "queryMaxMs": max(query) if query else 0.0,
        "truncationRate": statistics.fmean(value["truncated"] for value in values) if values else 0.0,
    }


def summarize_group(rows: list[dict]) -> dict:
    ok = [row for row in rows if row.get("status") == "ok"]
    return {
        "cases": len(rows),
        "successfulCases": len(ok),
        "failedCases": len(rows) - len(ok),
        "ravel": tool_summary([row["ravel"] for row in ok]),
        "graphify": tool_summary([row["graphify"] for row in ok]),
    }


def summarize(results_path: Path) -> dict:
    latest = {}
    for line in results_path.read_text(encoding="utf-8").splitlines():
        if line.strip():
            row = json.loads(line)
            latest[row["id"]] = row
    rows = list(latest.values())
    summary = {"version": 1, "adapterVersion": ADAPTER_VERSION, **summarize_group(rows)}
    summary["byLanguage"] = {
        language: summarize_group([row for row in rows if row.get("language") == language])
        for language in LANGUAGES
    }
    summary["bySplit"] = {
        split: summarize_group([row for row in rows if row.get("split") == split])
        for split in SPLITS
    }
    return summary


def execute(args: argparse.Namespace) -> None:
    manifest_path = args.manifest.resolve()
    check(argparse.Namespace(manifest=manifest_path))
    cases = selected_cases(args, manifest_path)
    paths = source_paths(args)
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    verify_sources(manifest, paths)
    workspace = args.workspace.resolve()
    workspace.mkdir(parents=True, exist_ok=True)
    temp_root = workspace / "tmp"
    temp_root.mkdir(exist_ok=True)
    results_path = workspace / "results.jsonl"
    run_config_path = workspace / "run-config.json"
    run_config = {
        "adapterVersion": ADAPTER_VERSION,
        "queryMode": QUERY_MODE,
        "manifestSha256": sha256_file(manifest_path),
        "ravelProfile": args.ravel_profile,
        "ravelProfileArgs": list(RAVEL_PROFILES[args.ravel_profile]),
        "tokenBudget": args.token_budget,
        "workers": args.workers,
        "ravelExecutable": executable_metadata(args.ravel),
        "graphifyExecutable": executable_metadata(args.graphify),
    }
    if run_config_path.exists() and not args.no_resume:
        if json.loads(run_config_path.read_text(encoding="utf-8")) != run_config:
            raise SystemExit("workspace settings differ; use a new workspace or --no-resume")
    elif results_path.exists() and not args.no_resume:
        raise SystemExit("legacy workspace lacks run-config.json; use a new workspace or --no-resume")
    run_config_path.write_text(json.dumps(run_config, indent=2, sort_keys=True) + "\n", encoding="utf-8")
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
    print(f"Running {len(pending)} pending of {len(cases)} Real-FIM scale cases", flush=True)
    row_iter = selected_rows(paths, pending)
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
                    failures += result.get("status") != "ok"
                    with RESULT_LOCK:
                        output.write(json.dumps(result, ensure_ascii=False, sort_keys=True) + "\n")
                        output.flush()
                    finished += 1
                    if finished <= 10 or finished % args.progress_every == 0:
                        print(
                            f"finished={finished}/{len(pending)} failures={failures} "
                            f"last={result['id']}",
                            flush=True,
                        )
                fill()
    if submitted != len(pending):
        raise RuntimeError(f"loaded {submitted} pending rows, expected {len(pending)}")
    summary = summarize(results_path)
    summary.update({
        "benchmark": "Real-FIM-Eval TypeScript/Go scale compatibility",
        "queryMode": QUERY_MODE,
        "claimsCorrectness": False,
        "manifestSha256": sha256_file(manifest_path),
        "resultsSha256": sha256_file(results_path),
        "runConfigSha256": sha256_file(run_config_path),
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
    prepare_parser = subparsers.add_parser("prepare", help="select and fingerprint all TS/Go cases")
    prepare_parser.add_argument("--add", type=Path, required=True)
    prepare_parser.add_argument("--edit", type=Path, required=True)
    prepare_parser.add_argument("--output", type=Path, required=True)
    prepare_parser.set_defaults(function=prepare)
    check_parser = subparsers.add_parser("check", help="validate a prepared manifest offline")
    check_parser.add_argument("--manifest", type=Path, required=True)
    check_parser.set_defaults(function=check)
    run_parser = subparsers.add_parser("run", help="run or resume the scale comparison")
    run_parser.add_argument("--manifest", type=Path, required=True)
    run_parser.add_argument("--add", type=Path, required=True)
    run_parser.add_argument("--edit", type=Path, required=True)
    run_parser.add_argument("--workspace", type=Path, required=True)
    run_parser.add_argument("--ravel", default="ravel")
    run_parser.add_argument("--graphify", default="graphify")
    run_parser.add_argument("--workers", type=int, default=2)
    run_parser.add_argument("--token-budget", type=int, default=2000)
    run_parser.add_argument("--ravel-profile", choices=sorted(RAVEL_PROFILES), default="broad")
    run_parser.add_argument("--keep-items", type=int, default=20)
    run_parser.add_argument("--progress-every", type=int, default=100)
    run_parser.add_argument("--limit", type=int)
    run_parser.add_argument("--language", action="append", choices=LANGUAGES)
    run_parser.add_argument("--split", action="append", choices=SPLITS)
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
