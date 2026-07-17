#!/usr/bin/env python3
"""Compare Ravel and Graphify on documented declarations in T3 Code.

The benchmark uses a pinned checkout of the official T3 Code repository. Both
tools receive byte-identical TypeScript-only corpora with JSDoc removed so the
query text cannot be found verbatim in source. Exact declaration path, symbol,
and line are the retrieval gold. This is not an official T3 Code metric.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import re

from polyglot_compare import (
    COMMON_ADAPTER_VERSION,
    RAVEL_EXECUTION_ADAPTER_VERSION,
    RAVEL_PROFILES,
    RavelBatchPool,
    executable_metadata,
    normalized,
    run_command,
    sha256_file,
    stable_hash,
)
import run_c_family as harness


ADAPTER_VERSION = "t3code-typescript-doc-declaration-v1"
QUERY_MODE = "typescript-jsdoc-symbol-redacted-v1"
T3CODE_URL = "https://github.com/pingdotgg/t3code"
SOURCE_ROOTS = ("apps", "infra", "oxlint-plugin-t3code", "packages", "scripts")
SOURCE_SUFFIXES = (".ts", ".tsx", ".mts", ".cts")
WORD = re.compile(r"[A-Za-z][A-Za-z0-9_-]+")
BLOCK_DOC = re.compile(r"^\s*/\*\*")
LINE_DOC = re.compile(r"^\s*///(?!\s*<reference\b)(?P<body>.*)$")
DECLARATION = re.compile(
    r"^\s*"
    r"(?:(?:export|default|declare|abstract|async|public|private|protected|static|"
    r"readonly|override|accessor)\s+)*"
    r"(?:(?P<kind>function|class|interface|type|enum|namespace|module)\s+"
    r"(?P<named>[A-Za-z_$][\w$]*)|"
    r"(?P<binding>const|let|var)\s+(?P<bound>[A-Za-z_$][\w$]*))"
)
METHOD = re.compile(
    r"^\s*"
    r"(?:(?:public|private|protected|static|readonly|abstract|async|override|accessor|"
    r"declare)\s+)*(?:(?:get|set)\s+)?(?P<name>[A-Za-z_$][\w$]*)\s*"
    r"(?:<[^>{};]*>)?\s*\("
)
SKIP_PREFIXES = (
    "import ", "export {", "export *", "return ", "if ", "for ", "while ",
    "switch ", "catch ", "throw ", "new ", "await ",
)


harness.CORPORA["typescript"] = {
    "name": "T3 Code",
    "url": T3CODE_URL,
    "release": None,
    "roots": SOURCE_ROOTS,
    "suffixes": SOURCE_SUFFIXES,
    "queryMode": QUERY_MODE,
}


def git_output(repository: Path, *arguments: str) -> str:
    return run_command(["git", *arguments], repository)[0].strip()


def tracked_typescript_files(repository: Path) -> list[Path]:
    output = git_output(repository, "ls-files")
    files = []
    for line in output.splitlines():
        relative = Path(line)
        if not relative.parts or relative.parts[0] not in SOURCE_ROOTS:
            continue
        if relative.suffix.lower() not in SOURCE_SUFFIXES:
            continue
        if (repository / relative).is_file():
            files.append(relative)
    return sorted(files)


def doc_block(lines: list[str], start: int) -> tuple[list[str], int] | None:
    if LINE_DOC.match(lines[start]):
        block = []
        index = start
        while index < len(lines) and LINE_DOC.match(lines[index]):
            block.append(lines[index])
            index += 1
        return block, index
    if not BLOCK_DOC.match(lines[start]):
        return None
    block = []
    index = start
    while index < len(lines):
        block.append(lines[index])
        index += 1
        if "*/" in block[-1]:
            return block, index
    return None


def clean_documentation(lines: list[str]) -> str:
    cleaned = []
    in_fence = False
    for line in lines:
        body = line.strip()
        if body.startswith("///"):
            body = body[3:].strip()
        else:
            body = re.sub(r"^/\*\*?", "", body)
            body = re.sub(r"\*/$", "", body)
            body = re.sub(r"^\*\s?", "", body).strip()
        if body.startswith("```") or body.startswith("~~~"):
            in_fence = not in_fence
            continue
        if in_fence or not body:
            continue
        if re.match(
            r"^@(param|typeParam|returns?|throws|example|see|since|deprecated|default|"
            r"template|internal|public|private|remarks?)\b",
            body,
            flags=re.IGNORECASE,
        ):
            continue
        body = re.sub(r"^[-*]\s+", "", body)
        cleaned.append(body)
    return " ".join(cleaned).strip()


def query_text(documentation: str, symbol: str) -> str:
    return harness.query_text(documentation, symbol, "typescript")


def declaration_after(lines: list[str], start: int) -> tuple[int, str, str] | None:
    for index in range(start, min(len(lines), start + 16)):
        stripped = lines[index].strip()
        if not stripped:
            continue
        if stripped.startswith("@"):
            continue
        if stripped.startswith(SKIP_PREFIXES):
            return None
        match = DECLARATION.match(lines[index])
        if match:
            kind = match.group("kind") or match.group("binding")
            symbol = match.group("named") or match.group("bound")
            return index + 1, kind, symbol
        method = METHOD.match(lines[index])
        if method:
            symbol = method.group("name")
            if symbol not in {"constructor", "if", "for", "while", "switch", "catch"}:
                return index + 1, "method", symbol
        return None
    return None


def cases_from_source(relative: Path, source: str, revision: str) -> list[dict]:
    lines = source.splitlines()
    cases = []
    index = 0
    while index < len(lines):
        block = doc_block(lines, index)
        if block is None:
            index += 1
            continue
        raw_documentation, next_index = block
        declaration = declaration_after(lines, next_index)
        doc_start = index + 1
        index = next_index
        if declaration is None:
            continue
        declaration_line, kind, symbol = declaration
        documentation = clean_documentation(raw_documentation)
        question = query_text(documentation, symbol)
        meaningful = [
            word for word in WORD.findall(question.split("\n", 1)[-1])
            if word.lower() not in {"redacted", "symbol"}
        ]
        if len(meaningful) < 5 or not normalized(symbol):
            continue
        case_id = stable_hash(
            "t3code-typescript", revision, relative.as_posix(), declaration_line,
            kind, symbol, documentation,
        )
        cases.append({
            "id": case_id,
            "selectionKey": stable_hash("t3code-typescript-order-v1", case_id, length=64),
            "language": "typescript",
            "goldPath": relative.as_posix(),
            "goldLine": declaration_line,
            "goldKind": kind,
            "goldSymbol": symbol,
            "documentationSha256": hashlib.sha256(documentation.encode()).hexdigest(),
            "querySha256": hashlib.sha256(question.encode()).hexdigest(),
            "documentation": documentation,
            "docStartLine": doc_start,
        })
    return cases


def discover_cases(repository: Path, files: list[Path], revision: str) -> list[dict]:
    cases = []
    for relative in files:
        source = (repository / relative).read_text(encoding="utf-8", errors="replace")
        cases.extend(cases_from_source(relative, source, revision))
    unique = {}
    for case in cases:
        key = (case["goldPath"], case["goldLine"], normalized(case["goldSymbol"]))
        unique.setdefault(key, case)
    return sorted(unique.values(), key=lambda case: (case["selectionKey"], case["id"]))


def prepare(args: argparse.Namespace) -> None:
    repository = args.repository.resolve()
    revision = git_output(repository, "rev-parse", "HEAD")
    files = tracked_typescript_files(repository)
    if not files:
        raise SystemExit(f"no tracked first-party TypeScript files: {repository}")
    cases = discover_cases(repository, files, revision)
    eligible = len(cases)
    if args.max_cases:
        cases = cases[: args.max_cases]
    if not cases:
        raise SystemExit("no eligible documented TypeScript declarations")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    cases_path = output / "cases.jsonl"
    with cases_path.open("w", encoding="utf-8") as handle:
        for case in cases:
            handle.write(json.dumps(case, ensure_ascii=False, sort_keys=True) + "\n")
    manifest = {
        "version": 1,
        "benchmark": "T3 Code TypeScript documented-declaration retrieval compatibility",
        "adapterVersion": ADAPTER_VERSION,
        "commonAdapterVersion": COMMON_ADAPTER_VERSION,
        "queryMode": QUERY_MODE,
        "language": "typescript",
        "officialMetric": False,
        "claimsAnswerCorrectness": False,
        "retrievalGold": "exact documented TypeScript declaration path, symbol, and line",
        "cases": len(cases),
        "eligibleCases": eligible,
        "casesSha256": sha256_file(cases_path),
        "source": {
            "name": "T3 Code",
            "url": T3CODE_URL,
            "revision": revision,
            "includedRoots": list(SOURCE_ROOTS),
            "excludedTrackedRoot": ".repos",
            "files": len(files),
            "lines": sum(
                len((repository / path).read_text(encoding="utf-8", errors="replace").splitlines())
                for path in files
            ),
            "sourceSha256": harness.source_fingerprint(repository, files),
        },
    }
    (output / "manifest.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
    print(json.dumps(manifest, indent=2, sort_keys=True))


def read_cases(path: Path) -> list[dict]:
    return harness.read_cases(path)


def check(args: argparse.Namespace) -> None:
    manifest_path = args.manifest.resolve()
    manifest = json.loads(manifest_path.read_text())
    cases_path = manifest_path.parent / "cases.jsonl"
    cases = read_cases(cases_path)
    if manifest.get("version") != 1 or manifest.get("adapterVersion") != ADAPTER_VERSION:
        raise SystemExit("unsupported T3 Code TypeScript manifest")
    if not cases or manifest.get("cases") != len(cases) or manifest.get("casesSha256") != sha256_file(cases_path):
        raise SystemExit("T3 Code case count or hash mismatch")
    for case in cases:
        if case.get("language") != "typescript":
            raise SystemExit(f"case {case.get('id')} language mismatch")
        expected = hashlib.sha256(query_text(case["documentation"], case["goldSymbol"]).encode()).hexdigest()
        if expected != case.get("querySha256"):
            raise SystemExit(f"case {case['id']} query hash mismatch")
    print(f"Validated {len(cases)} T3 Code TypeScript documented-declaration cases")


def verify_source(manifest: dict, repository: Path) -> list[Path]:
    files = tracked_typescript_files(repository)
    expected = manifest["source"]
    revision = git_output(repository, "rev-parse", "HEAD")
    if revision != expected["revision"]:
        raise SystemExit(f"source revision mismatch: expected {expected['revision']}, found {revision}")
    if len(files) != expected["files"] or harness.source_fingerprint(repository, files) != expected["sourceSha256"]:
        raise SystemExit("T3 Code TypeScript source fingerprint mismatch")
    return files


def ravel_gold_node_ids(graph: Path, cases: list[dict]) -> dict[str, list[str]]:
    items = harness.documented.graph_items(graph / "graph.json", "ravel")
    by_name: dict[str, list[dict]] = {}
    for item in items:
        by_name.setdefault(normalized(str(item.get("name", ""))), []).append(item)
    result = {}
    for case in cases:
        matches = [
            str(item.get("id", ""))
            for item in by_name.get(normalized(case["goldSymbol"]), [])
            if item.get("id") and harness.documented.score_declaration([item], case)["hit"]
        ]
        result[case["id"]] = matches
    return result


def execute(args: argparse.Namespace) -> None:
    manifest_path = args.manifest.resolve()
    check(argparse.Namespace(manifest=manifest_path))
    manifest = json.loads(manifest_path.read_text())
    source = args.repository.resolve()
    files = verify_source(manifest, source)
    all_cases = read_cases(manifest_path.parent / "cases.jsonl")
    cases = all_cases[: args.limit] if args.limit else all_cases
    workspace = args.workspace.resolve()
    workspace.mkdir(parents=True, exist_ok=True)
    results_path = workspace / "results.jsonl"
    config_path = workspace / "run-config.json"
    run_config = {
        "adapterVersion": ADAPTER_VERSION,
        "queryMode": QUERY_MODE,
        "manifestSha256": sha256_file(manifest_path),
        "sourceRevision": manifest["source"]["revision"],
        "sourceSha256": manifest["source"]["sourceSha256"],
        "cases": len(cases),
        "selectionSha256": stable_hash(*(case["id"] for case in cases), length=64),
        "buildOrder": "balanced-two-round",
        "queryOrder": "alternating-by-selection-key",
        "ravelProfile": args.ravel_profile,
        "ravelProfileArgs": list(RAVEL_PROFILES[args.ravel_profile]),
        "ravelExecutionAdapterVersion": RAVEL_EXECUTION_ADAPTER_VERSION,
        "ravelQueryMode": args.ravel_query_mode,
        "ravelQueryTimeoutSeconds": args.ravel_query_timeout,
        "tokenBudget": args.token_budget,
        "workers": args.workers,
        "keepItems": args.keep_items,
        "ravelExecutable": executable_metadata(args.ravel),
        "graphifyExecutable": executable_metadata(args.graphify),
    }
    if config_path.exists() and json.loads(config_path.read_text()) != run_config:
        raise SystemExit("workspace settings differ; use a new workspace")
    if not config_path.exists() and (results_path.exists() or (workspace / "build.json").exists()):
        raise SystemExit("legacy workspace lacks run-config.json; use a new workspace")
    config_path.write_text(json.dumps(run_config, indent=2, sort_keys=True) + "\n")
    build, graphs = harness.build_corpora_and_graphs(args, workspace, source, files, all_cases)
    completed = set()
    if results_path.exists():
        completed = {
            row.get("id") for line in results_path.read_text().splitlines() if line.strip()
            for row in (json.loads(line),) if row.get("status") == "ok"
        }
    pending = [case for case in cases if case["id"] not in completed]
    gold_node_ids = ravel_gold_node_ids(graphs["ravel"], cases)
    print(f"Running {len(pending)} pending of {len(cases)} TypeScript cases", flush=True)
    failures = 0
    execution_path = workspace / "ravel-execution.json"
    ravel_backend = None
    try:
        if args.ravel_query_mode == "batch" and pending:
            ravel_backend = RavelBatchPool(
                args.ravel,
                graphs["ravel"],
                args.token_budget,
                args.ravel_profile,
                args.workers,
                args.ravel_query_timeout,
            )
            execution_path.write_text(json.dumps(ravel_backend.metadata(), indent=2, sort_keys=True) + "\n")
        elif args.ravel_query_mode == "process":
            execution_path.write_text(json.dumps({
                "executionAdapterVersion": RAVEL_EXECUTION_ADAPTER_VERSION,
                "mode": "one-shot-process",
                "latencySemantics": {
                    "queryMs": "one-shot subprocess including graph load and index build",
                    "comparableToGraphifyQueryMs": True,
                },
                "sessions": [],
            }, indent=2, sort_keys=True) + "\n")
        elif not execution_path.exists():
            raise SystemExit("completed batch workspace lacks ravel-execution.json")
        with results_path.open("a", encoding="utf-8") as output:
            with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
                futures = [
                    pool.submit(
                        harness.run_case, args, case, graphs, ravel_backend,
                        gold_node_ids.get(case["id"]),
                    )
                    for case in pending
                ]
                for finished, future in enumerate(concurrent.futures.as_completed(futures), 1):
                    result = future.result()
                    failures += result.get("status") != "ok"
                    output.write(json.dumps(result, ensure_ascii=False, sort_keys=True) + "\n")
                    output.flush()
                    if finished <= 10 or finished % args.progress_every == 0:
                        print(f"finished={finished}/{len(pending)} failures={failures} last={result['id']}", flush=True)
    finally:
        if ravel_backend is not None:
            ravel_backend.close()
    summary = harness.summarize(results_path, build, "typescript", args.keep_items)
    summary.update({
        "adapterVersion": ADAPTER_VERSION,
        "benchmark": manifest["benchmark"],
        "queryMode": QUERY_MODE,
        "officialMetric": False,
        "claimsAnswerCorrectness": False,
        "manifestSha256": sha256_file(manifest_path),
        "resultsSha256": sha256_file(results_path),
        "runConfigSha256": sha256_file(config_path),
        "source": manifest["source"],
        "tokenBudget": args.token_budget,
        "workers": args.workers,
        "ravelProfile": args.ravel_profile,
        "ravelProfileArgs": list(RAVEL_PROFILES[args.ravel_profile]),
        "ravelExecution": json.loads(execution_path.read_text()),
        "ravelVersion": run_command([args.ravel, "version"], workspace)[0].strip(),
        "graphifyVersion": run_command([args.graphify, "--version"], workspace)[0].strip(),
        "ravelExecutable": executable_metadata(args.ravel),
        "graphifyExecutable": executable_metadata(args.graphify),
        "platform": {"os": os.uname().sysname, "arch": os.uname().machine},
    })
    (workspace / "summary.json").write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n")
    print(json.dumps(summary, indent=2, sort_keys=True))


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    prepare_parser = subparsers.add_parser("prepare")
    prepare_parser.add_argument("--repository", type=Path, required=True)
    prepare_parser.add_argument("--output", type=Path, required=True)
    prepare_parser.add_argument("--max-cases", type=int)
    prepare_parser.set_defaults(function=prepare)
    check_parser = subparsers.add_parser("check")
    check_parser.add_argument("--manifest", type=Path, required=True)
    check_parser.set_defaults(function=check)
    run_parser = subparsers.add_parser("run")
    run_parser.add_argument("--manifest", type=Path, required=True)
    run_parser.add_argument("--repository", type=Path, required=True)
    run_parser.add_argument("--workspace", type=Path, required=True)
    run_parser.add_argument("--ravel", default="ravel")
    run_parser.add_argument("--graphify", default="graphify")
    run_parser.add_argument("--workers", type=int, default=2)
    run_parser.add_argument("--token-budget", type=int, default=2000)
    run_parser.add_argument("--ravel-profile", choices=sorted(RAVEL_PROFILES), default="broad")
    run_parser.add_argument("--ravel-query-mode", choices=("batch", "process"), default="batch")
    run_parser.add_argument("--ravel-query-timeout", type=float, default=360)
    run_parser.add_argument("--keep-items", type=int, default=20)
    run_parser.add_argument("--limit", type=int)
    run_parser.add_argument("--progress-every", type=int, default=100)
    run_parser.set_defaults(function=execute)
    args = parser.parse_args()
    for field in ("workers", "limit", "max_cases"):
        value = getattr(args, field, None)
        if value is not None and value < 1:
            parser.error(f"--{field.replace('_', '-')} must be positive")
    if getattr(args, "token_budget", 64) < 64:
        parser.error("--token-budget must be at least 64")
    if getattr(args, "ravel_query_timeout", 1) <= 0:
        parser.error("--ravel-query-timeout must be positive")
    args.function(args)


if __name__ == "__main__":
    main()
