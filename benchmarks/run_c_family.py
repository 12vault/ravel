#!/usr/bin/env python3
"""Compare Ravel and Graphify on documented C or C++ declarations.

C uses pinned libgit2 sources. C++ uses pinned nlohmann/json sources. Both
tools receive byte-identical, documentation-stripped corpora in separate
directories. Query order alternates deterministically by case ID.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import statistics

from polyglot_compare import (
    COMMON_ADAPTER_VERSION,
    RAVEL_EXECUTION_ADAPTER_VERSION,
    RAVEL_PROFILES,
    RavelBatchPool,
    executable_metadata,
    graphify_result,
    normalized,
    ravel_result,
    run_command,
    sha256_file,
    stable_hash,
)
import run_ghostty_swift as documented


ADAPTER_VERSION = "c-family-doc-declaration-v1"
WORD = re.compile(r"[A-Za-z][A-Za-z0-9_-]+")
LINE_DOC = re.compile(r"^\s*///(?!/)(?P<body>.*)$")
BLOCK_DOC = re.compile(r"^\s*/\*\*")
C_FUNCTION = re.compile(r"\b(?P<name>git_[A-Za-z_]\w*)\s*\(")
CPP_TYPE = re.compile(r"\b(?P<kind>class|struct|enum(?:\s+class)?)\s+(?P<name>[A-Za-z_]\w*)")
CPP_CALLABLE = re.compile(
    r"(?P<name>~?[A-Za-z_]\w*|operator\s*(?:\[\]|\(\)|[^\s(]+))\s*\("
)
CPP_EXCLUDED = {
    "alignas", "decltype", "defined", "enable_if", "if", "noexcept",
    "requires", "sizeof", "static_assert", "while",
}

CORPORA = {
    "c": {
        "name": "libgit2",
        "url": "https://github.com/libgit2/libgit2",
        "release": "v1.9.3",
        "roots": ("include", "src"),
        "suffixes": (".c", ".h"),
        "queryMode": "c-doxygen-symbol-redacted-v1",
    },
    "cpp": {
        "name": "nlohmann/json",
        "url": "https://github.com/nlohmann/json",
        "release": "v3.12.0",
        "roots": ("include",),
        "suffixes": (".hpp", ".cpp", ".cc"),
        "queryMode": "cpp-doxygen-symbol-redacted-v1",
    },
}


def git_output(repository: Path, *arguments: str) -> str:
    return run_command(["git", *arguments], repository)[0].strip()


def tracked_files(repository: Path, language: str) -> list[Path]:
    config = CORPORA[language]
    output = git_output(repository, "ls-files")
    files = []
    for line in output.splitlines():
        relative = Path(line)
        if not relative.parts or relative.parts[0] not in config["roots"]:
            continue
        if relative.suffix.lower() not in config["suffixes"]:
            continue
        if (repository / relative).is_file():
            files.append(relative)
    return sorted(files)


def source_fingerprint(repository: Path, files: list[Path]) -> str:
    digest = hashlib.sha256()
    for relative in files:
        digest.update(relative.as_posix().encode("utf-8"))
        digest.update(b"\0")
        digest.update((repository / relative).read_bytes())
        digest.update(b"\0")
    return digest.hexdigest()


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
        if body.startswith("```") or body in {"@code", "@endcode"}:
            in_fence = not in_fence
            continue
        if in_fence or not body:
            continue
        if re.match(r"^@(param|tparam|return|returns|retval|sa|see|since|deprecated|warning)\b", body):
            continue
        body = re.sub(r"^@(brief|details?)\s+", "", body)
        body = re.sub(r"^[-*]\s+", "", body)
        cleaned.append(body)
    return " ".join(cleaned).strip()


def query_text(documentation: str, symbol: str, language: str) -> str:
    description = re.sub(
        rf"(?<![A-Za-z0-9_])`?{re.escape(symbol)}`?(?![A-Za-z0-9_])",
        "[redacted symbol]",
        documentation,
        flags=re.IGNORECASE,
    )
    label = {"c": "C", "cpp": "C++", "typescript": "TypeScript"}.get(language, language)
    return f"Find the {label} declaration that best matches this description:\n{description}"


def c_declaration_after(lines: list[str], start: int) -> tuple[int, str, str] | None:
    selected = []
    for index in range(start, min(len(lines), start + 32)):
        stripped = lines[index].strip()
        if not stripped:
            continue
        if stripped.startswith("#"):
            continue
        selected.append((index, lines[index]))
        joined = " ".join(line.strip() for _, line in selected)
        if ";" not in joined and "{" not in joined:
            continue
        if joined.startswith("typedef") or "(*" in joined:
            return None
        match = C_FUNCTION.search(joined)
        if match is None:
            return None
        symbol = match.group("name")
        line = next((line_index + 1 for line_index, text in selected if symbol in text), selected[0][0] + 1)
        return line, "function", symbol
    return None


def cpp_declaration_after(lines: list[str], start: int) -> tuple[int, str, str] | None:
    selected = []
    for index in range(start, min(len(lines), start + 40)):
        stripped = lines[index].strip()
        if not stripped:
            continue
        if stripped.startswith("#") or stripped.startswith("template") or stripped.startswith("requires"):
            continue
        type_match = CPP_TYPE.search(stripped)
        if type_match and not stripped.startswith(("using ", "typedef ")):
            kind = "enum" if type_match.group("kind").startswith("enum") else type_match.group("kind")
            return index + 1, kind, type_match.group("name")
        selected.append((index, lines[index]))
        joined = " ".join(line.strip() for _, line in selected)
        if "(" not in joined:
            if ";" in joined or "{" in joined:
                return None
            continue
        if ";" not in joined and "{" not in joined and len(selected) < 12:
            continue
        candidates = []
        for match in CPP_CALLABLE.finditer(joined):
            name = re.sub(r"\s+", "", match.group("name"))
            if name in CPP_EXCLUDED or name.isupper():
                continue
            candidates.append(name)
        if not candidates:
            return None
        symbol = candidates[0]
        line = next((line_index + 1 for line_index, text in selected if symbol in re.sub(r"\s+", "", text)), selected[0][0] + 1)
        return line, "function", symbol
    return None


def cases_from_source(relative: Path, source: str, revision: str, language: str) -> list[dict]:
    lines = source.splitlines()
    cases = []
    index = 0
    while index < len(lines):
        block = doc_block(lines, index)
        if block is None:
            index += 1
            continue
        raw_documentation, next_index = block
        declaration = (
            c_declaration_after(lines, next_index)
            if language == "c"
            else cpp_declaration_after(lines, next_index)
        )
        doc_start = index + 1
        index = next_index
        if declaration is None:
            continue
        declaration_line, kind, symbol = declaration
        documentation = clean_documentation(raw_documentation)
        question = query_text(documentation, symbol, language)
        meaningful = [
            word for word in WORD.findall(question.split("\n", 1)[-1])
            if word.lower() not in {"redacted", "symbol"}
        ]
        if len(meaningful) < 5 or not normalized(symbol):
            continue
        case_id = stable_hash(
            CORPORA[language]["name"], language, revision, relative.as_posix(),
            declaration_line, kind, symbol, documentation,
        )
        cases.append({
            "id": case_id,
            "selectionKey": stable_hash(f"{language}-documented-order-v1", case_id, length=64),
            "language": language,
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


def discover_cases(repository: Path, files: list[Path], revision: str, language: str) -> list[dict]:
    cases = []
    for relative in files:
        source = (repository / relative).read_text(encoding="utf-8", errors="replace")
        cases.extend(cases_from_source(relative, source, revision, language))
    unique = {}
    for case in cases:
        key = (case["goldPath"], case["goldLine"], normalized(case["goldSymbol"]))
        unique.setdefault(key, case)
    return sorted(unique.values(), key=lambda case: (case["selectionKey"], case["id"]))


def prepare(args: argparse.Namespace) -> None:
    repository = args.repository.resolve()
    language = args.language
    config = CORPORA[language]
    revision = git_output(repository, "rev-parse", "HEAD")
    files = tracked_files(repository, language)
    if not files:
        raise SystemExit(f"no tracked {language} files in configured roots: {repository}")
    cases = discover_cases(repository, files, revision, language)
    eligible = len(cases)
    if args.max_cases:
        cases = cases[: args.max_cases]
    if not cases:
        raise SystemExit(f"no eligible documented {language} declarations")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    cases_path = output / "cases.jsonl"
    with cases_path.open("w", encoding="utf-8") as handle:
        for case in cases:
            handle.write(json.dumps(case, ensure_ascii=False, sort_keys=True) + "\n")
    manifest = {
        "version": 1,
        "benchmark": f"{config['name']} {language} documented-declaration retrieval compatibility",
        "adapterVersion": ADAPTER_VERSION,
        "commonAdapterVersion": COMMON_ADAPTER_VERSION,
        "queryMode": config["queryMode"],
        "language": language,
        "officialMetric": False,
        "claimsAnswerCorrectness": False,
        "retrievalGold": f"exact documented {language} declaration path, symbol, and line",
        "cases": len(cases),
        "eligibleCases": eligible,
        "casesSha256": sha256_file(cases_path),
        "source": {
            "name": config["name"], "url": config["url"], "release": config["release"],
            "revision": revision, "files": len(files),
            "lines": sum(len((repository / path).read_text(encoding="utf-8", errors="replace").splitlines()) for path in files),
            "sourceSha256": source_fingerprint(repository, files),
        },
    }
    (output / "manifest.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
    print(json.dumps(manifest, indent=2, sort_keys=True))


def read_cases(path: Path) -> list[dict]:
    return documented.read_cases(path)


def check(args: argparse.Namespace) -> None:
    manifest_path = args.manifest.resolve()
    manifest = json.loads(manifest_path.read_text())
    cases_path = manifest_path.parent / "cases.jsonl"
    cases = read_cases(cases_path)
    language = manifest.get("language")
    if manifest.get("version") != 1 or manifest.get("adapterVersion") != ADAPTER_VERSION or language not in CORPORA:
        raise SystemExit("unsupported C-family manifest version, adapter, or language")
    if not cases or manifest.get("cases") != len(cases) or manifest.get("casesSha256") != sha256_file(cases_path):
        raise SystemExit("C-family case count or hash mismatch")
    for case in cases:
        if case.get("language") != language:
            raise SystemExit(f"case {case.get('id')} language mismatch")
        question_hash = hashlib.sha256(
            query_text(case["documentation"], case["goldSymbol"], language).encode()
        ).hexdigest()
        if question_hash != case.get("querySha256"):
            raise SystemExit(f"case {case['id']} query hash mismatch")
    print(f"Validated {len(cases)} {language} documented-declaration cases")


def verify_source(manifest: dict, repository: Path) -> list[Path]:
    language = manifest["language"]
    files = tracked_files(repository, language)
    expected = manifest["source"]
    revision = git_output(repository, "rev-parse", "HEAD")
    if revision != expected["revision"]:
        raise SystemExit(f"source revision mismatch: expected {expected['revision']}, found {revision}")
    if len(files) != expected["files"] or source_fingerprint(repository, files) != expected["sourceSha256"]:
        raise SystemExit("C-family source fingerprint mismatch")
    return files


def strip_documentation(source: str) -> str:
    lines = source.splitlines(keepends=True)
    output = []
    in_block = False
    for line in lines:
        stripped = line.lstrip()
        reference_directive = stripped.startswith("/// <reference")
        is_doc = in_block or stripped.startswith("/**") or (
            stripped.startswith("///") and not reference_directive
        )
        if is_doc:
            ending = "\r\n" if line.endswith("\r\n") else "\n" if line.endswith("\n") else ""
            output.append(ending)
            if stripped.startswith("/**") or in_block:
                in_block = "*/" not in line
        else:
            output.append(line)
    return "".join(output)


def materialize(repository: Path, source: Path, files: list[Path]) -> None:
    for relative in files:
        destination = repository / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text(
            strip_documentation((source / relative).read_text(encoding="utf-8", errors="replace")),
            encoding="utf-8",
        )


def validate_graph_paths(graph: Path, tool: str, corpus: Path, files: list[Path]) -> None:
    value = json.loads(graph.read_text())
    allowed = {path.as_posix() for path in files}
    path_key = "path" if tool == "ravel" else "source_file"
    invalid = []
    for node in value.get("nodes") or []:
        raw = str(node.get(path_key) or "")
        if not raw:
            continue
        raw = raw[7:] if raw.startswith("file://") else raw
        candidate = Path(raw)
        source_suffixes = tuple(
            suffix for config in CORPORA.values() for suffix in config["suffixes"]
        )
        if candidate.suffix.lower() not in source_suffixes:
            continue
        if candidate.is_absolute():
            try:
                relative = candidate.resolve().relative_to(corpus.resolve()).as_posix()
            except ValueError:
                invalid.append(raw)
                continue
        else:
            relative = candidate.as_posix().lstrip("./")
        if relative not in allowed:
            invalid.append(raw)
    if invalid:
        raise RuntimeError(f"{tool} graph contains source paths outside its corpus: {invalid[:5]}")


def run_build(tool: str, args: argparse.Namespace, corpus: Path, output: Path) -> tuple[Path, float, float]:
    if tool == "ravel":
        elapsed = documented.build_ravel(args.ravel, corpus, output)
        return output / "graph.json", elapsed, 0.0
    graph, extract_ms, cluster_ms = documented.build_graphify(args.graphify, corpus, output)
    return graph, extract_ms + cluster_ms, extract_ms


def build_corpora_and_graphs(
    args: argparse.Namespace, workspace: Path, source: Path, files: list[Path], cases: list[dict]
) -> tuple[dict, dict[str, Path]]:
    build_path = workspace / "build.json"
    if build_path.exists():
        build = json.loads(build_path.read_text())
        graphs = {tool: Path(build["queryGraphs"][tool]) for tool in ("ravel", "graphify")}
        expected_graph_files = (graphs["ravel"] / "graph.json", graphs["graphify"])
        if any(not path.is_file() for path in expected_graph_files):
            raise SystemExit("workspace build metadata exists but a graph is missing; use a new workspace")
        return build, graphs
    corpora = {tool: workspace / f"{tool}-corpus" for tool in ("ravel", "graphify")}
    for corpus in corpora.values():
        if corpus.exists():
            shutil.rmtree(corpus)
        corpus.mkdir(parents=True)
        materialize(corpus, source, files)
    corpus_hashes = {tool: source_fingerprint(corpus, files) for tool, corpus in corpora.items()}
    if len(set(corpus_hashes.values())) != 1:
        raise RuntimeError("tool corpora are not byte-identical")
    trials = {"ravel": [], "graphify": []}
    query_graphs = {}
    orders = (("ravel", "graphify"), ("graphify", "ravel"))
    for round_number, order in enumerate(orders, 1):
        for tool in order:
            output = workspace / f"{tool}-graph-{round_number}"
            if output.exists():
                shutil.rmtree(output)
            graph, elapsed, extract_ms = run_build(tool, args, corpora[tool], output)
            if documented.graph_node_count(graph) < 1:
                raise RuntimeError(f"{tool} produced an empty graph")
            if source_fingerprint(corpora[tool], files) != corpus_hashes[tool]:
                raise RuntimeError(f"{tool} modified its input corpus")
            validate_graph_paths(graph, tool, corpora[tool], files)
            trials[tool].append({"order": order.index(tool) + 1, "round": round_number, "ms": elapsed, "extractMs": extract_ms})
            query_graphs[tool] = graph.parent if tool == "ravel" else graph
    ravel_graph = query_graphs["ravel"] / "graph.json"
    graphify_graph = query_graphs["graphify"]
    build = {
        "version": 1,
        "corpusFiles": len(files),
        "corpusSha256": next(iter(corpus_hashes.values())),
        "corporaByteIdentical": True,
        "corporaUnchangedAfterBuild": True,
        "buildTrials": trials,
        "ravelBuildMs": statistics.fmean(trial["ms"] for trial in trials["ravel"]),
        "graphifyBuildMs": statistics.fmean(trial["ms"] for trial in trials["graphify"]),
        "ravelGraphNodes": documented.graph_node_count(ravel_graph),
        "ravelGraphEdges": documented.graph_edge_count(ravel_graph),
        "graphifyGraphNodes": documented.graph_node_count(graphify_graph),
        "graphifyGraphEdges": documented.graph_edge_count(graphify_graph),
        "ravelDeclarationCoverage": documented.declaration_coverage(documented.graph_items(ravel_graph, "ravel"), cases),
        "graphifyDeclarationCoverage": documented.declaration_coverage(documented.graph_items(graphify_graph, "graphify"), cases),
        "queryGraphs": {tool: str(path) for tool, path in query_graphs.items()},
    }
    build_path.write_text(json.dumps(build, indent=2, sort_keys=True) + "\n")
    return build, query_graphs


def run_case(
    args: argparse.Namespace,
    case: dict,
    graphs: dict[str, Path],
    ravel_backend: RavelBatchPool | None = None,
    ravel_trace_ids: list[str] | None = None,
) -> dict:
    question = query_text(case["documentation"], case["goldSymbol"], case["language"])
    first = "ravel" if int(case["selectionKey"][:8], 16) % 2 == 0 else "graphify"
    order = (first, "graphify" if first == "ravel" else "ravel")
    values = {}
    result = {
        "id": case["id"], "language": case["language"], "firstTool": first,
        "goldPath": case["goldPath"], "goldLine": case["goldLine"],
        "goldKind": case["goldKind"], "goldSymbol": case["goldSymbol"],
    }
    try:
        for tool in order:
            value = (
                (
                    ravel_backend.query(question, ravel_trace_ids)
                    if ravel_backend is not None
                    else ravel_result(args.ravel, graphs[tool], question, args.token_budget, args.ravel_profile)
                )
                if tool == "ravel"
                else graphify_result(args.graphify, graphs[tool], question, args.token_budget)
            )
            value.update(documented.score_declaration(value["items"], case))
            if tool == "ravel" and ravel_trace_ids is not None:
                value["funnel"] = retrieval_funnel(ravel_trace_ids, value.get("traceNodes", []))
            value["returned"] = len(value["items"])
            value["items"] = value["items"][: args.keep_items]
            values[tool] = value
        result.update({"status": "ok", **values})
    except Exception as error:
        result.update({"status": "error", "error": str(error)})
    return result


def retrieval_funnel(trace_ids: list[str] | None, traces: list[dict]) -> dict:
    trace_ids = trace_ids or []
    return {
        "available": not trace_ids or bool(traces),
        "indexed": bool(trace_ids),
        "ranked": any(int(trace.get("lexicalRank") or 0) > 0 for trace in traces),
        "seeded": any(bool(trace.get("seeded")) for trace in traces),
        "traversed": any(bool(trace.get("traversed")) for trace in traces),
        "walked": any(int(trace.get("walkRank") or 0) > 0 for trace in traces),
        "candidate": any(int(trace.get("candidateRank") or 0) > 0 for trace in traces),
        "returned": any(int(trace.get("returnedRank") or 0) > 0 for trace in traces),
        "droppedReasons": sorted({
            str(trace.get("droppedReason")) for trace in traces if trace.get("droppedReason")
        }) or (["trace_unavailable"] if trace_ids else ["not_indexed"]),
    }


def summarize(results_path: Path, build: dict, language: str, item_limit: int) -> dict:
    latest = {}
    for line in results_path.read_text().splitlines():
        if line.strip():
            row = json.loads(line)
            latest[row["id"]] = row
    rows = list(latest.values())
    ok = [row for row in rows if row.get("status") == "ok"]

    def symbol_only(tool: str) -> dict:
        reciprocal_ranks = []
        for row in ok:
            gold = normalized(row["goldSymbol"])
            names = [normalized(str(item.get("name", ""))) for item in row[tool].get("items", [])]
            rank = names.index(gold) + 1 if gold in names else None
            reciprocal_ranks.append(0.0 if rank is None else 1.0 / rank)
        hits = sum(value > 0 for value in reciprocal_ranks)
        return {
            "hits": hits,
            "recall": hits / len(ok) if ok else 0.0,
            "mrr": statistics.fmean(reciprocal_ranks) if reciprocal_ranks else 0.0,
            "limit": item_limit,
        }

    def funnel_summary() -> dict:
        stages = ("indexed", "ranked", "seeded", "traversed", "walked", "candidate", "returned")
        funnels = [row["ravel"]["funnel"] for row in ok if "funnel" in row["ravel"]]
        dropped = {}
        for funnel in funnels:
            for reason in funnel.get("droppedReasons", []):
                dropped[reason] = dropped.get(reason, 0) + 1
        return {
            "cases": len(funnels),
            "stages": {
                stage: {
                    "cases": sum(bool(funnel.get(stage)) for funnel in funnels),
                    "rate": (
                        sum(bool(funnel.get(stage)) for funnel in funnels) / len(funnels)
                        if funnels else 0.0
                    ),
                }
                for stage in stages
            },
            "droppedReasons": dict(sorted(dropped.items())),
        }

    return {
        "version": 1, "adapterVersion": ADAPTER_VERSION, "language": language,
        "cases": len(rows), "successfulCases": len(ok), "failedCases": len(rows) - len(ok),
        "sharedCorpusFiles": build["corpusFiles"], "corporaByteIdentical": build["corporaByteIdentical"],
        "corporaUnchangedAfterBuild": build["corporaUnchangedAfterBuild"], "buildTrials": build["buildTrials"],
        "queryOrder": {
            "ravelFirst": sum(row["firstTool"] == "ravel" for row in ok),
            "graphifyFirst": sum(row["firstTool"] == "graphify" for row in ok),
        },
        "ravel": {
            "buildMs": build["ravelBuildMs"], "graphNodes": build["ravelGraphNodes"],
            "graphEdges": build["ravelGraphEdges"], "declarationCoverage": build["ravelDeclarationCoverage"],
            "top20SymbolOnly": symbol_only("ravel"),
            "funnel": funnel_summary(),
            **documented.tool_summary([row["ravel"] for row in ok]),
        },
        "graphify": {
            "buildMs": build["graphifyBuildMs"], "graphNodes": build["graphifyGraphNodes"],
            "graphEdges": build["graphifyGraphEdges"], "declarationCoverage": build["graphifyDeclarationCoverage"],
            "top20SymbolOnly": symbol_only("graphify"),
            **documented.tool_summary([row["graphify"] for row in ok]),
        },
        "pairwise": {
            "bothHit": sum(row["ravel"]["hit"] and row["graphify"]["hit"] for row in ok),
            "ravelOnlyHit": sum(row["ravel"]["hit"] and not row["graphify"]["hit"] for row in ok),
            "graphifyOnlyHit": sum(not row["ravel"]["hit"] and row["graphify"]["hit"] for row in ok),
            "bothMiss": sum(not row["ravel"]["hit"] and not row["graphify"]["hit"] for row in ok),
        },
    }


def execute(args: argparse.Namespace) -> None:
    manifest_path = args.manifest.resolve()
    check(argparse.Namespace(manifest=manifest_path))
    manifest = json.loads(manifest_path.read_text())
    language = manifest["language"]
    source = args.repository.resolve()
    files = verify_source(manifest, source)
    all_cases = read_cases(manifest_path.parent / "cases.jsonl")
    cases = all_cases[: args.limit] if args.limit else all_cases
    workspace = args.workspace.resolve()
    workspace.mkdir(parents=True, exist_ok=True)
    results_path = workspace / "results.jsonl"
    config_path = workspace / "run-config.json"
    run_config = {
        "adapterVersion": ADAPTER_VERSION, "queryMode": manifest["queryMode"],
        "manifestSha256": sha256_file(manifest_path), "sourceRevision": manifest["source"]["revision"],
        "sourceSha256": manifest["source"]["sourceSha256"], "cases": len(cases),
        "selectionSha256": stable_hash(*(case["id"] for case in cases), length=64),
        "buildOrder": "balanced-two-round", "queryOrder": "alternating-by-selection-key",
        "ravelProfile": args.ravel_profile, "ravelProfileArgs": list(RAVEL_PROFILES[args.ravel_profile]),
        "ravelExecutionAdapterVersion": RAVEL_EXECUTION_ADAPTER_VERSION,
        "ravelQueryMode": args.ravel_query_mode,
        "ravelQueryTimeoutSeconds": args.ravel_query_timeout,
        "tokenBudget": args.token_budget, "workers": args.workers,
        "keepItems": args.keep_items,
        "ravelExecutable": executable_metadata(args.ravel), "graphifyExecutable": executable_metadata(args.graphify),
    }
    if config_path.exists() and json.loads(config_path.read_text()) != run_config:
        raise SystemExit("workspace settings differ; use a new workspace")
    if not config_path.exists() and (results_path.exists() or (workspace / "build.json").exists()):
        raise SystemExit("legacy workspace lacks run-config.json; use a new workspace")
    config_path.write_text(json.dumps(run_config, indent=2, sort_keys=True) + "\n")
    build, graphs = build_corpora_and_graphs(args, workspace, source, files, all_cases)
    completed = set()
    if results_path.exists():
        completed = {
            row.get("id") for line in results_path.read_text().splitlines() if line.strip()
            for row in (json.loads(line),) if row.get("status") == "ok"
        }
    pending = [case for case in cases if case["id"] not in completed]
    print(f"Running {len(pending)} pending of {len(cases)} {language} cases", flush=True)
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
                futures = [pool.submit(run_case, args, case, graphs, ravel_backend) for case in pending]
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
    summary = summarize(results_path, build, language, args.keep_items)
    summary.update({
        "benchmark": manifest["benchmark"], "queryMode": manifest["queryMode"],
        "officialMetric": False, "claimsAnswerCorrectness": False,
        "manifestSha256": sha256_file(manifest_path), "resultsSha256": sha256_file(results_path),
        "runConfigSha256": sha256_file(config_path), "source": manifest["source"],
        "tokenBudget": args.token_budget, "workers": args.workers,
        "ravelProfile": args.ravel_profile, "ravelProfileArgs": list(RAVEL_PROFILES[args.ravel_profile]),
        "ravelExecution": json.loads(execution_path.read_text()),
        "ravelVersion": run_command([args.ravel, "version"], workspace)[0].strip(),
        "graphifyVersion": run_command([args.graphify, "--version"], workspace)[0].strip(),
        "ravelExecutable": executable_metadata(args.ravel), "graphifyExecutable": executable_metadata(args.graphify),
        "platform": {"os": os.uname().sysname, "arch": os.uname().machine},
    })
    (workspace / "summary.json").write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n")
    print(json.dumps(summary, indent=2, sort_keys=True))


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    prepare_parser = subparsers.add_parser("prepare")
    prepare_parser.add_argument("--language", choices=sorted(CORPORA), required=True)
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
