#!/usr/bin/env python3
"""Compare Ravel and Graphify on documented declarations in Ghostty's Swift code.

This is a deterministic retrieval compatibility benchmark, not an official
Ghostty or Swift metric. Both tools receive the same Swift-only repository.
Queries are derived from declaration documentation with the gold symbol
redacted, while Swift documentation comments are removed from the corpus.
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
    normalized,
    paths_match,
    ravel_result,
    run_command,
    sha256_file,
    stable_hash,
)


ADAPTER_VERSION = "ghostty-swift-doc-declaration-v1"
QUERY_MODE = "swift-documentation-symbol-redacted-v1"
GHOSTTY_URL = "https://github.com/ghostty-org/ghostty"
WORD = re.compile(r"[A-Za-z][A-Za-z0-9_-]+")
DOC_LINE = re.compile(r"^(?P<indent>\s*)///(?P<body>.*)$")
DECLARATION = re.compile(
    r"^\s*"
    r"(?:(?:@[A-Za-z_]\w*(?:\([^)]*\))?|public|private|fileprivate|internal|open|final|"
    r"static|class|mutating|nonmutating|required|convenience|override|nonisolated|"
    r"distributed|indirect|prefix|postfix|infix|lazy|weak|unowned)\s+)*"
    r"(?P<kind>class|struct|enum|protocol|actor|func)\s+"
    r"(?P<name>`[^`]+`|[A-Za-z_]\w*|[^\s(<{]+)"
)


def git_output(repository: Path, *arguments: str) -> str:
    return run_command(["git", *arguments], repository)[0].strip()


def tracked_swift_files(repository: Path) -> list[Path]:
    output = git_output(repository, "ls-files", "--", "macos/**/*.swift")
    files = [Path(line) for line in output.splitlines() if line.strip()]
    return sorted(path for path in files if (repository / path).is_file())


def source_fingerprint(repository: Path, files: list[Path]) -> str:
    digest = hashlib.sha256()
    for relative in files:
        digest.update(relative.as_posix().encode("utf-8"))
        digest.update(b"\0")
        digest.update((repository / relative).read_bytes())
        digest.update(b"\0")
    return digest.hexdigest()


def clean_documentation(lines: list[str]) -> str:
    cleaned = []
    in_fence = False
    for line in lines:
        match = DOC_LINE.match(line)
        body = (match.group("body") if match else line).strip()
        if body.startswith("```"):
            in_fence = not in_fence
            continue
        if in_fence or not body:
            continue
        body = re.sub(r"^[-*]\s+(?:Parameters?|Returns?|Throws?|Note|Important|Warning):?\s*", "", body)
        body = re.sub(r"^[-*]\s+", "", body)
        cleaned.append(body)
    return " ".join(cleaned).strip()


def query_text(documentation: str, symbol: str) -> str:
    description = documentation
    if symbol:
        description = re.sub(
            rf"(?<![A-Za-z0-9_])`?{re.escape(symbol)}`?(?![A-Za-z0-9_])",
            "[redacted symbol]",
            description,
            flags=re.IGNORECASE,
        )
    return f"Find the Swift declaration that best matches this description:\n{description}"


def declaration_after(lines: list[str], start: int) -> tuple[int, str, str] | None:
    """Return one nearby named declaration after a documentation block."""
    for index in range(start, min(len(lines), start + 12)):
        stripped = lines[index].strip()
        if not stripped:
            continue
        if stripped.startswith("@") and not DECLARATION.match(lines[index]):
            continue
        match = DECLARATION.match(lines[index])
        if match:
            return index + 1, match.group("kind"), match.group("name").strip("`")
        return None
    return None


def cases_from_source(relative: Path, source: str, revision: str) -> list[dict]:
    lines = source.splitlines()
    cases = []
    index = 0
    while index < len(lines):
        if not DOC_LINE.match(lines[index]):
            index += 1
            continue
        doc_start = index
        block = []
        while index < len(lines) and DOC_LINE.match(lines[index]):
            block.append(lines[index])
            index += 1
        declaration = declaration_after(lines, index)
        if declaration is None:
            continue
        declaration_line, kind, symbol = declaration
        documentation = clean_documentation(block)
        question = query_text(documentation, symbol)
        meaningful = [
            word for word in WORD.findall(question.split("\n", 1)[-1])
            if word.lower() not in {"redacted", "symbol"}
        ]
        if len(meaningful) < 5 or not normalized(symbol):
            continue
        case_id = stable_hash(
            "ghostty-swift", revision, relative.as_posix(), declaration_line,
            kind, symbol, documentation,
        )
        cases.append({
            "id": case_id,
            "selectionKey": stable_hash("ghostty-swift-order-v1", case_id, length=64),
            "goldPath": relative.as_posix(),
            "goldLine": declaration_line,
            "goldKind": kind,
            "goldSymbol": symbol,
            "documentationSha256": hashlib.sha256(documentation.encode("utf-8")).hexdigest(),
            "querySha256": hashlib.sha256(question.encode("utf-8")).hexdigest(),
            "documentation": documentation,
            "docStartLine": doc_start + 1,
        })
    return cases


def discover_cases(repository: Path, files: list[Path], revision: str) -> list[dict]:
    cases = []
    for relative in files:
        source = (repository / relative).read_text(encoding="utf-8")
        cases.extend(cases_from_source(relative, source, revision))
    unique = {}
    for case in cases:
        key = (case["goldPath"], case["goldLine"], normalized(case["goldSymbol"]))
        unique.setdefault(key, case)
    return sorted(unique.values(), key=lambda case: (case["selectionKey"], case["id"]))


def prepare(args: argparse.Namespace) -> None:
    repository = args.repository.resolve()
    revision = git_output(repository, "rev-parse", "HEAD")
    files = tracked_swift_files(repository)
    if not files:
        raise SystemExit(f"no tracked Swift files under macos/: {repository}")
    cases = discover_cases(repository, files, revision)
    eligible = len(cases)
    if args.max_cases:
        cases = cases[: args.max_cases]
    if not cases:
        raise SystemExit("no eligible documented Swift declarations")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    cases_path = output / "cases.jsonl"
    with cases_path.open("w", encoding="utf-8") as handle:
        for case in cases:
            handle.write(json.dumps(case, ensure_ascii=False, sort_keys=True) + "\n")
    manifest = {
        "version": 1,
        "benchmark": "Ghostty Swift documented-declaration retrieval compatibility",
        "adapterVersion": ADAPTER_VERSION,
        "commonAdapterVersion": COMMON_ADAPTER_VERSION,
        "queryMode": QUERY_MODE,
        "officialMetric": False,
        "claimsAnswerCorrectness": False,
        "retrievalGold": "exact documented Swift declaration path, symbol, and line",
        "cases": len(cases),
        "eligibleCases": eligible,
        "casesSha256": sha256_file(cases_path),
        "source": {
            "url": GHOSTTY_URL,
            "revision": revision,
            "swiftRoot": "macos",
            "swiftFiles": len(files),
            "swiftLines": sum(len((repository / path).read_text(encoding="utf-8").splitlines()) for path in files),
            "swiftSourceSha256": source_fingerprint(repository, files),
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
        raise SystemExit("unsupported Ghostty Swift manifest version or adapter")
    if not cases or manifest.get("cases") != len(cases):
        raise SystemExit("Ghostty Swift case count mismatch")
    if manifest.get("casesSha256") != sha256_file(cases_path):
        raise SystemExit("Ghostty Swift cases hash mismatch")
    for case in cases:
        required = ("goldPath", "goldLine", "goldKind", "goldSymbol", "documentation", "querySha256")
        if any(not case.get(field) for field in required):
            raise SystemExit(f"case {case.get('id')} lacks retrieval gold or query data")
        if hashlib.sha256(query_text(case["documentation"], case["goldSymbol"]).encode()).hexdigest() != case["querySha256"]:
            raise SystemExit(f"case {case['id']} query hash mismatch")
    print(f"Validated {len(cases)} Ghostty Swift paired retrieval cases")


def verify_source(manifest: dict, repository: Path) -> list[Path]:
    expected = manifest.get("source") or {}
    revision = git_output(repository, "rev-parse", "HEAD")
    if revision != expected.get("revision"):
        raise SystemExit(f"Ghostty revision mismatch: expected {expected.get('revision')}, found {revision}")
    files = tracked_swift_files(repository)
    if len(files) != expected.get("swiftFiles") or source_fingerprint(repository, files) != expected.get("swiftSourceSha256"):
        raise SystemExit("Ghostty Swift source fingerprint mismatch")
    return files


def strip_documentation(source: str) -> str:
    output = []
    for line in source.splitlines(keepends=True):
        if DOC_LINE.match(line.rstrip("\r\n")):
            ending = "\r\n" if line.endswith("\r\n") else "\n" if line.endswith("\n") else ""
            output.append(ending)
        else:
            output.append(line)
    return "".join(output)


def materialize(repository: Path, source: Path, files: list[Path]) -> None:
    for relative in files:
        destination = repository / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text(
            strip_documentation((source / relative).read_text(encoding="utf-8")),
            encoding="utf-8",
        )


def score_declaration(items: list[dict], case: dict) -> dict:
    rank = None
    for candidate_rank, item in enumerate(items, 1):
        if not paths_match(str(item.get("path", "")), case["goldPath"]):
            continue
        if normalized(str(item.get("name", ""))) != normalized(case["goldSymbol"]):
            continue
        start = int(item.get("startLine") or 0)
        end = int(item.get("endLine") or start)
        if start and start <= case["goldLine"] <= max(start, end):
            rank = candidate_rank
            break
    return {
        "hit": rank is not None,
        "rank": rank,
        "reciprocalRank": 0.0 if rank is None else 1.0 / rank,
    }


def graph_node_count(path: Path) -> int:
    value = json.loads(path.read_text(encoding="utf-8"))
    nodes = value.get("nodes")
    return len(nodes) if isinstance(nodes, list) else 0


def graph_edge_count(path: Path) -> int:
    value = json.loads(path.read_text(encoding="utf-8"))
    edges = value.get("edges") if "edges" in value else value.get("links")
    return len(edges) if isinstance(edges, list) else 0


def graph_items(path: Path, tool: str) -> list[dict]:
    value = json.loads(path.read_text(encoding="utf-8"))
    items = []
    for node in value.get("nodes") or []:
        if tool == "ravel":
            items.append({
                "id": str(node.get("id", "")),
                "name": str(node.get("name", "")),
                "path": str(node.get("path", "")),
                "startLine": int(node.get("startLine") or 0),
                "endLine": int(node.get("endLine") or node.get("startLine") or 0),
            })
            continue
        location = re.search(r"L(\d+)(?:-L?(\d+))?", str(node.get("source_location", "")))
        start = int(location.group(1)) if location else 0
        end = int(location.group(2) or location.group(1)) if location else 0
        items.append({
            "name": str(node.get("label", "")),
            "path": str(node.get("source_file", "")),
            "startLine": start,
            "endLine": end,
        })
    return items


def declaration_coverage(items: list[dict], cases: list[dict]) -> dict:
    by_kind: dict[str, dict[str, int | float]] = {}
    hits = 0
    for case in cases:
        hit = score_declaration(items, case)["hit"]
        hits += hit
        bucket = by_kind.setdefault(case["goldKind"], {"cases": 0, "covered": 0})
        bucket["cases"] += 1
        bucket["covered"] += hit
    for bucket in by_kind.values():
        bucket["coverage"] = bucket["covered"] / bucket["cases"] if bucket["cases"] else 0.0
    return {
        "cases": len(cases),
        "covered": hits,
        "coverage": hits / len(cases) if cases else 0.0,
        "byKind": dict(sorted(by_kind.items())),
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
        "language": "swift",
        "sharedCorpusFiles": build["corpusFiles"],
        "ravel": {
            "buildMs": build["ravelBuildMs"],
            "graphNodes": build["ravelGraphNodes"],
            "graphEdges": build["ravelGraphEdges"],
            "declarationCoverage": build["ravelDeclarationCoverage"],
            **tool_summary([row["ravel"] for row in ok]),
        },
        "graphify": {
            "buildMs": build["graphifyBuildMs"],
            "extractMs": build["graphifyExtractMs"],
            "clusterMs": build["graphifyClusterMs"],
            "graphNodes": build["graphifyGraphNodes"],
            "graphEdges": build["graphifyGraphEdges"],
            "declarationCoverage": build["graphifyDeclarationCoverage"],
            **tool_summary([row["graphify"] for row in ok]),
        },
        "pairwise": {
            "bothHit": sum(row["ravel"]["hit"] and row["graphify"]["hit"] for row in ok),
            "ravelOnlyHit": sum(row["ravel"]["hit"] and not row["graphify"]["hit"] for row in ok),
            "graphifyOnlyHit": sum(not row["ravel"]["hit"] and row["graphify"]["hit"] for row in ok),
            "bothMiss": sum(not row["ravel"]["hit"] and not row["graphify"]["hit"] for row in ok),
            "ravelQueryWins": sum(row["ravel"]["queryMs"] < row["graphify"]["queryMs"] for row in ok),
        },
    }
    return summary


def run_case(args: argparse.Namespace, case: dict, graphs: dict[str, Path]) -> dict:
    result = {
        "id": case["id"],
        "language": "swift",
        "goldPath": case["goldPath"],
        "goldLine": case["goldLine"],
        "goldKind": case["goldKind"],
        "goldSymbol": case["goldSymbol"],
    }
    try:
        question = query_text(case["documentation"], case["goldSymbol"])
        ravel = ravel_result(args.ravel, graphs["ravel"], question, args.token_budget, args.ravel_profile)
        ravel.update(score_declaration(ravel["items"], case))
        ravel["returned"] = len(ravel["items"])
        ravel["items"] = ravel["items"][: args.keep_items]
        graphify = graphify_result(args.graphify, graphs["graphify"], question, args.token_budget)
        graphify.update(score_declaration(graphify["items"], case))
        graphify["returned"] = len(graphify["items"])
        graphify["items"] = graphify["items"][: args.keep_items]
        result.update({"status": "ok", "ravel": ravel, "graphify": graphify})
    except Exception as error:
        result.update({"status": "error", "error": str(error)})
    return result


def build_corpus_and_graphs(
    args: argparse.Namespace,
    workspace: Path,
    source: Path,
    files: list[Path],
    coverage_cases: list[dict],
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
    materialize(repository, source, files)
    ravel_build_ms = build_ravel(args.ravel, repository, ravel_graph)
    graphify_graph, extract_ms, cluster_ms = build_graphify(args.graphify, repository, graphify_root)
    ravel_nodes = graph_node_count(ravel_graph / "graph.json")
    graphify_nodes = graph_node_count(graphify_graph)
    if ravel_nodes < 1 or graphify_nodes < 1:
        raise RuntimeError(f"invalid empty graph: ravel nodes={ravel_nodes}, graphify nodes={graphify_nodes}")
    build = {
        "version": 1,
        "corpusFiles": len(files),
        "ravelGraphNodes": ravel_nodes,
        "ravelGraphEdges": graph_edge_count(ravel_graph / "graph.json"),
        "ravelDeclarationCoverage": declaration_coverage(
            graph_items(ravel_graph / "graph.json", "ravel"), coverage_cases
        ),
        "graphifyGraphNodes": graphify_nodes,
        "graphifyGraphEdges": graph_edge_count(graphify_graph),
        "graphifyDeclarationCoverage": declaration_coverage(
            graph_items(graphify_graph, "graphify"), coverage_cases
        ),
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
    source = args.repository.resolve()
    files = verify_source(manifest, source)
    all_cases = read_cases(manifest_path.parent / "cases.jsonl")
    cases = all_cases
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
        "sourceRevision": manifest["source"]["revision"],
        "sourceSha256": manifest["source"]["swiftSourceSha256"],
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
    build, graphs = build_corpus_and_graphs(args, workspace, source, files, all_cases)
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
    print(f"Running {len(pending)} pending of {len(cases)} Ghostty Swift cases", flush=True)
    finished = 0
    failures = 0
    with results_path.open("a", encoding="utf-8") as output:
        with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
            futures = {pool.submit(run_case, args, case, graphs): case["id"] for case in pending}
            for future in concurrent.futures.as_completed(futures):
                result = future.result()
                failures += result.get("status") != "ok"
                output.write(json.dumps(result, ensure_ascii=False, sort_keys=True) + "\n")
                output.flush()
                finished += 1
                if finished <= 10 or finished % args.progress_every == 0:
                    print(f"finished={finished}/{len(pending)} failures={failures} last={result['id']}", flush=True)
    summary = summarize(results_path, build)
    summary.update({
        "benchmark": "Ghostty Swift documented-declaration retrieval compatibility",
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
    prepare_parser = subparsers.add_parser("prepare", help="derive and fingerprint Ghostty Swift cases")
    prepare_parser.add_argument("--repository", type=Path, required=True)
    prepare_parser.add_argument("--output", type=Path, required=True)
    prepare_parser.add_argument("--max-cases", type=int)
    prepare_parser.set_defaults(function=prepare)
    check_parser = subparsers.add_parser("check", help="validate a prepared manifest offline")
    check_parser.add_argument("--manifest", type=Path, required=True)
    check_parser.set_defaults(function=check)
    run_parser = subparsers.add_parser("run", help="build the shared Swift corpus and run or resume")
    run_parser.add_argument("--manifest", type=Path, required=True)
    run_parser.add_argument("--repository", type=Path, required=True)
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
    for field in ("workers", "limit", "max_cases"):
        value = getattr(args, field, None)
        if value is not None and value < 1:
            parser.error(f"--{field.replace('_', '-')} must be positive")
    if getattr(args, "token_budget", 64) < 64:
        parser.error("--token-budget must be at least 64")
    args.function(args)


if __name__ == "__main__":
    main()
