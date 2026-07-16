#!/usr/bin/env python3
"""Run Ravel and Graphify on CrossCodeEval's TypeScript cross-file payloads.

CrossCodeEval does not publish its original repositories or gold retrieval
spans. This adapter therefore builds the same miniature repository for both
tools from the union of the six published five-chunk retrieval views. Gold
identifiers are project-level APIs referenced by the missing line and present
in those cross-file chunks. This is a retrieval compatibility benchmark, not
CrossCodeEval's official model-completion score.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import json
import math
import os
from pathlib import Path, PurePosixPath
import re
import sqlite3
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
    ravel_result,
    run_command,
    score_identifier_retrieval,
    sha256_file,
    stable_hash,
)


ADAPTER_VERSION = "crosscodeeval-typescript-candidate-union-v1"
CANDIDATE_FILES = (
    "line_completion_oracle_bm25.jsonl",
    "line_completion_oracle_openai_cosine_sim.jsonl",
    "line_completion_oracle_unixcoder_cosine_sim.jsonl",
    "line_completion_rg1_bm25.jsonl",
    "line_completion_rg1_openai_cosine_sim.jsonl",
    "line_completion_rg1_unixcoder_cosine_sim.jsonl",
)
IDENTIFIER = re.compile(r"[A-Za-z_$][\w$]*")
IMPORT_PATTERNS = (
    re.compile(r"import\s+(?:type\s+)?\{([^}]+)\}\s+from\s+['\"]([^'\"]+)['\"]", re.DOTALL),
    re.compile(r"import\s+(?:type\s+)?([A-Za-z_$][\w$]*)\s+from\s+['\"]([^'\"]+)['\"]"),
    re.compile(r"import\s+\*\s+as\s+([A-Za-z_$][\w$]*)\s+from\s+['\"]([^'\"]+)['\"]"),
)
TYPESCRIPT_WORDS = {
    "abstract", "any", "as", "asserts", "async", "await", "bigint", "boolean", "break",
    "case", "catch", "class", "const", "constructor", "continue", "debugger", "declare",
    "default", "delete", "do", "else", "enum", "export", "extends", "false", "finally",
    "for", "from", "function", "get", "if", "implements", "import", "in", "infer",
    "instanceof", "interface", "is", "keyof", "let", "module", "namespace", "never", "new",
    "null", "number", "object", "of", "private", "protected", "public", "readonly", "require",
    "return", "set", "static", "string", "super", "switch", "symbol", "this", "throw", "true",
    "try", "type", "typeof", "undefined", "unique", "unknown", "var", "void", "while", "with",
    "yield",
}
RESULT_LOCK = threading.Lock()


def safe_relative(value: str, fallback: str) -> Path:
    value = value.replace("\\", "/").lstrip("/")
    parts = [part for part in PurePosixPath(value).parts if part not in ("", ".", "..")]
    path = Path(*parts) if parts else Path(fallback)
    return path if path.suffix else path.with_suffix(".ts")


def imported_identifiers(prompt: str) -> set[str]:
    imported = set()
    for pattern_index, pattern in enumerate(IMPORT_PATTERNS):
        for match in pattern.finditer(prompt):
            if pattern_index == 0:
                for part in match.group(1).split(","):
                    part = part.strip()
                    if not part:
                        continue
                    alias = re.split(r"\s+as\s+", part)[-1].strip()
                    if IDENTIFIER.fullmatch(alias):
                        imported.add(alias)
            else:
                imported.add(match.group(1))
    for match in re.finditer(
        r"(?:const|let|var)\s*\{([^}]+)\}\s*=\s*require\s*\(\s*['\"][^'\"]+['\"]\s*\)",
        prompt,
        re.DOTALL,
    ):
        for part in match.group(1).split(","):
            alias = re.split(r"\s*:\s*", part.strip())[-1].strip()
            if IDENTIFIER.fullmatch(alias):
                imported.add(alias)
    return imported


def candidate_chunks(variants: list[dict]) -> list[dict]:
    chunks = []
    seen = set()
    for variant in variants:
        context = variant.get("crossfile_context") or {}
        for item in context.get("list") or []:
            if not isinstance(item, dict):
                continue
            filename = str(item.get("filename", ""))
            chunk = str(item.get("retrieved_chunk", ""))
            key = (filename, chunk)
            if not filename or not chunk or key in seen:
                continue
            seen.add(key)
            chunks.append({"filename": filename, "chunk": chunk})
    return chunks


def definition_like(identifier: str, chunk: str) -> bool:
    escaped = re.escape(identifier)
    patterns = (
        rf"\b(?:class|interface|type|enum|function|const|let|var)\s+{escaped}\b",
        rf"\b{escaped}\s*[:=]\s*(?:async\s*)?(?:\([^)]*\)\s*=>|function\b)",
        rf"\b(?:get|set|async)\s+{escaped}\s*\(",
        rf"\b{escaped}\s*\([^)]*\)\s*\{{",
    )
    return any(re.search(pattern, chunk) for pattern in patterns)


def derive_gold(base: dict, chunks: list[dict]) -> tuple[list[str], dict[str, list[str]], str]:
    groundtruth = str(base.get("groundtruth", ""))
    identifiers = {
        identifier
        for identifier in IDENTIFIER.findall(groundtruth)
        if len(identifier) >= 3 and identifier not in TYPESCRIPT_WORDS
    }
    paths_by_identifier = {
        identifier: sorted({
            item["filename"]
            for item in chunks
            if re.search(rf"\b{re.escape(identifier)}\b", item["chunk"])
        })
        for identifier in identifiers
    }
    paths_by_identifier = {identifier: paths for identifier, paths in paths_by_identifier.items() if paths}
    imported = imported_identifiers(str(base.get("prompt", "")))
    gold = sorted(identifier for identifier in paths_by_identifier if identifier in imported)
    source = "referenced-import"
    if not gold:
        gold = sorted(
            identifier
            for identifier in paths_by_identifier
            if any(
                definition_like(identifier, item["chunk"])
                for item in chunks
                if item["filename"] in paths_by_identifier[identifier]
            )
        )
        source = "cross-file-definition"
    if not gold:
        gold = sorted(
            identifier
            for identifier in paths_by_identifier
            if identifier[0].isupper() or len(identifier) >= 5
        )
        source = "cross-file-reference"
    return gold, {identifier: paths_by_identifier[identifier] for identifier in gold}, source


def source_paths(data_root: Path) -> list[Path]:
    typescript = data_root / "typescript" if (data_root / "typescript").is_dir() else data_root
    paths = [typescript / name for name in CANDIDATE_FILES]
    missing = [str(path) for path in paths if not path.is_file()]
    if missing:
        raise SystemExit("missing CrossCodeEval TypeScript files: " + ", ".join(missing))
    return paths


def prepare(args: argparse.Namespace) -> None:
    data_root = args.data_root.resolve()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    paths = source_paths(data_root)
    typescript = paths[0].parent
    base_path = typescript / "line_completion.jsonl"
    if not base_path.is_file():
        raise SystemExit(f"missing CrossCodeEval TypeScript base file: {base_path}")
    database_path = output / ".crosscodeeval-prepare.sqlite"
    if database_path.exists():
        database_path.unlink()
    database = sqlite3.connect(database_path)
    database.execute("CREATE TABLE base (task_id TEXT PRIMARY KEY, row_number INTEGER, payload TEXT)")
    database.execute(
        "CREATE TABLE chunks (task_id TEXT, filename TEXT, chunk TEXT, "
        "UNIQUE(task_id, filename, chunk))"
    )
    with base_path.open(encoding="utf-8") as handle:
        for row, line in enumerate(handle):
            base = json.loads(line)
            task_id = str((base.get("metadata") or {}).get("task_id", ""))
            if not task_id:
                raise RuntimeError(f"CrossCodeEval base row {row} has no task_id")
            database.execute(
                "INSERT INTO base(task_id, row_number, payload) VALUES (?, ?, ?)",
                (task_id, row, json.dumps(base, ensure_ascii=False, separators=(",", ":"))),
            )
    for path in paths:
        with path.open(encoding="utf-8") as handle:
            for line_number, line in enumerate(handle, 1):
                variant = json.loads(line)
                task_id = str((variant.get("metadata") or {}).get("task_id", ""))
                if database.execute("SELECT 1 FROM base WHERE task_id = ?", (task_id,)).fetchone() is None:
                    raise RuntimeError(f"{path}:{line_number}: unknown task_id {task_id!r}")
                for item in candidate_chunks([variant]):
                    database.execute(
                        "INSERT OR IGNORE INTO chunks(task_id, filename, chunk) VALUES (?, ?, ?)",
                        (task_id, item["filename"], item["chunk"]),
                    )
        database.commit()
    cases = []
    skipped = 0
    source_counts: dict[str, int] = {}
    payloads_path = output / "payloads.jsonl"
    with payloads_path.open("w", encoding="utf-8") as payload_output:
        for _, task_id, payload in database.execute(
            "SELECT row_number, task_id, payload FROM base ORDER BY row_number"
        ):
            base = json.loads(payload)
            chunks = [
                {"filename": filename, "chunk": chunk}
                for filename, chunk in database.execute(
                    "SELECT filename, chunk FROM chunks WHERE task_id = ? ORDER BY filename, chunk",
                    (task_id,),
                )
            ]
            gold, identifier_paths, gold_source = derive_gold(base, chunks)
            if not gold:
                skipped += 1
                continue
            metadata = base.get("metadata") or {}
            source_counts[gold_source] = source_counts.get(gold_source, 0) + 1
            payload_row = len(cases)
            payload_output.write(json.dumps(
                {"base": base, "chunks": chunks}, ensure_ascii=False, separators=(",", ":")
            ) + "\n")
            cases.append({
                "id": stable_hash("crosscodeeval-typescript", task_id),
                "payloadRow": payload_row,
                "taskId": task_id,
                "repository": str(metadata.get("repository", "")),
                "file": str(metadata.get("file", "")),
                "goldIdentifiers": gold,
                "identifierPaths": identifier_paths,
                "goldSource": gold_source,
                "candidateChunks": len(chunks),
            })
    database.close()
    database_path.unlink()
    cases_path = output / "cases.jsonl"
    with cases_path.open("w", encoding="utf-8") as handle:
        for case in cases:
            handle.write(json.dumps(case, ensure_ascii=False, sort_keys=True) + "\n")
    manifest = {
        "version": 1,
        "benchmark": "CrossCodeEval TypeScript candidate-union retrieval compatibility",
        "adapterVersion": ADAPTER_VERSION,
        "commonAdapterVersion": COMMON_ADAPTER_VERSION,
        "officialMetric": False,
        "cases": len(cases),
        "skippedWithoutDerivableGold": skipped,
        "goldSources": source_counts,
        "casesSha256": sha256_file(cases_path),
        "payloadsSha256": sha256_file(payloads_path),
        "sources": [
            {"path": path.name, "bytes": path.stat().st_size, "sha256": sha256_file(path)}
            for path in (base_path, *paths)
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
    payloads_path = manifest_path.parent / "payloads.jsonl"
    cases = read_cases(cases_path)
    if manifest.get("version") != 1 or manifest.get("adapterVersion") != ADAPTER_VERSION:
        raise SystemExit("unsupported CrossCodeEval manifest version or adapter")
    if manifest.get("cases") != len(cases) or manifest.get("casesSha256") != sha256_file(cases_path):
        raise SystemExit("CrossCodeEval manifest count or cases hash mismatch")
    if manifest.get("payloadsSha256") != sha256_file(payloads_path):
        raise SystemExit("CrossCodeEval payloads hash mismatch")
    for case in cases:
        if not case.get("goldIdentifiers") or not case.get("identifierPaths"):
            raise SystemExit(f"case {case['id']} has no derived retrieval gold")
    print(f"Validated {len(cases)} TypeScript CrossCodeEval compatibility cases")


def materialize(repository: Path, base: dict, chunks: list[dict]) -> None:
    for index, item in enumerate(chunks):
        relative = safe_relative(item["filename"], f"snippet-{index}.ts")
        path = repository / "contexts" / f"{index:03d}" / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(item["chunk"].rstrip() + "\n", encoding="utf-8")
    metadata = base.get("metadata") or {}
    target = repository / "target" / safe_relative(str(metadata.get("file", "")), "target.ts")
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(str(base.get("prompt", "")).rstrip() + "\n", encoding="utf-8")


def query_text(base: dict) -> str:
    metadata = base.get("metadata") or {}
    prompt = str(base.get("prompt", ""))[-5000:]
    return (
        f"Which cross-file TypeScript definition is needed to complete the missing next line of "
        f"{metadata.get('file', '')}?\nCode immediately before the cursor:\n{prompt}"
    )


def run_case(args: argparse.Namespace, case: dict, payload: dict, temp_root: Path) -> dict:
    started = time.perf_counter()
    result = {
        "id": case["id"],
        "language": "typescript",
        "taskId": case["taskId"],
        "repository": case["repository"],
        "file": case["file"],
        "goldIdentifiers": case["goldIdentifiers"],
        "goldSource": case["goldSource"],
        "candidateChunks": case["candidateChunks"],
    }
    try:
        with tempfile.TemporaryDirectory(prefix=f"cceval-{case['id']}-", dir=temp_root) as temporary:
            root = Path(temporary)
            repository = root / "repo"
            repository.mkdir()
            base = payload["base"]
            chunks = payload["chunks"]
            materialize(repository, base, chunks)
            question = query_text(base)

            ravel_graph = root / "ravel"
            ravel_build_ms = build_ravel(args.ravel, repository, ravel_graph)
            ravel = ravel_result(args.ravel, ravel_graph, question, args.token_budget, args.ravel_profile)
            ravel.update(score_identifier_retrieval(
                ravel["items"], case["goldIdentifiers"], case["identifierPaths"]
            ))
            ravel.update({"buildMs": ravel_build_ms, "returned": len(ravel["items"])})
            ravel["items"] = ravel["items"][: args.keep_items]

            graphify_root = root / "graphify"
            graphify_graph, extract_ms, cluster_ms = build_graphify(
                args.graphify, repository, graphify_root
            )
            graphify = graphify_result(args.graphify, graphify_graph, question, args.token_budget)
            graphify.update(score_identifier_retrieval(
                graphify["items"], case["goldIdentifiers"], case["identifierPaths"]
            ))
            graphify.update({
                "buildMs": extract_ms + cluster_ms,
                "extractMs": extract_ms,
                "clusterMs": cluster_ms,
                "returned": len(graphify["items"]),
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


def summarize(results_path: Path) -> dict:
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
        "language": "typescript",
    }
    for tool in ("ravel", "graphify"):
        values = [row[tool] for row in ok]
        summary[tool] = {
            "recall": statistics.fmean(value["hit"] for value in values) if values else 0.0,
            "goldIdentifierRecall": statistics.fmean(value["goldIdentifierRecall"] for value in values) if values else 0.0,
            "mrr": statistics.fmean(value["reciprocalRank"] for value in values) if values else 0.0,
            "meanEstimatedTokens": statistics.fmean(value["estimatedTokens"] for value in values) if values else 0.0,
            "meanReturned": statistics.fmean(value["returned"] for value in values) if values else 0.0,
            "meanBuildMs": statistics.fmean(value["buildMs"] for value in values) if values else 0.0,
            "buildP95Ms": percentile([value["buildMs"] for value in values], 0.95),
            "buildP99Ms": percentile([value["buildMs"] for value in values], 0.99),
            "meanQueryMs": statistics.fmean(value["queryMs"] for value in values) if values else 0.0,
            "queryP95Ms": percentile([value["queryMs"] for value in values], 0.95),
            "queryP99Ms": percentile([value["queryMs"] for value in values], 0.99),
            "truncationRate": statistics.fmean(value["truncated"] for value in values) if values else 0.0,
        }
    return summary


def selected_rows(payloads_path: Path, cases: list[dict]) -> Iterator[tuple[dict, dict]]:
    wanted = {case["payloadRow"]: case for case in cases}
    with payloads_path.open(encoding="utf-8") as handle:
        for row, line in enumerate(handle):
            case = wanted.get(row)
            if case is not None:
                yield case, json.loads(line)


def execute(args: argparse.Namespace) -> None:
    manifest_path = args.manifest.resolve()
    check(argparse.Namespace(manifest=manifest_path))
    cases = read_cases(manifest_path.parent / "cases.jsonl")
    if args.limit:
        cases = cases[: args.limit]
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
    print(f"Running {len(pending)} pending of {len(cases)} TypeScript cases", flush=True)

    row_iter = selected_rows(manifest_path.parent / "payloads.jsonl", pending)
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
                        case, payload = next(row_iter)
                    except StopIteration:
                        return
                    future = pool.submit(run_case, args, case, payload, temp_root)
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
                        print(f"finished={finished}/{len(pending)} failures={failures} last={result['id']}", flush=True)
                fill()
    if submitted != len(pending):
        raise RuntimeError(f"loaded {submitted} pending rows, expected {len(pending)}")
    summary = summarize(results_path)
    summary.update({
        "manifestSha256": sha256_file(manifest_path),
        "resultsSha256": sha256_file(results_path),
        "runConfigSha256": sha256_file(run_config_path),
        "tokenBudget": args.token_budget,
        "workers": args.workers,
        "ravelProfile": args.ravel_profile,
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
    prepare_parser = subparsers.add_parser("prepare", help="derive and fingerprint TypeScript cases")
    prepare_parser.add_argument("--data-root", type=Path, required=True)
    prepare_parser.add_argument("--output", type=Path, required=True)
    prepare_parser.set_defaults(function=prepare)
    check_parser = subparsers.add_parser("check", help="validate a prepared manifest offline")
    check_parser.add_argument("--manifest", type=Path, required=True)
    check_parser.set_defaults(function=check)
    run_parser = subparsers.add_parser("run", help="run or resume the TypeScript comparison")
    run_parser.add_argument("--manifest", type=Path, required=True)
    run_parser.add_argument("--workspace", type=Path, required=True)
    run_parser.add_argument("--ravel", default="ravel")
    run_parser.add_argument("--graphify", default="graphify")
    run_parser.add_argument("--workers", type=int, default=2)
    run_parser.add_argument("--token-budget", type=int, default=2000)
    run_parser.add_argument("--ravel-profile", choices=sorted(RAVEL_PROFILES), default="broad")
    run_parser.add_argument("--keep-items", type=int, default=20)
    run_parser.add_argument("--limit", type=int)
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
