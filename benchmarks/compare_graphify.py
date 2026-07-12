#!/usr/bin/env python3
"""Compare Ravel and Graphify symbol-name recall on one Ravel JSONL suite.

This compatibility adapter intentionally measures only normalized expected
symbol names. It does not compare evidence IDs (the products use different ID
schemes) or final model-answer quality.
"""

from __future__ import annotations

import argparse
import ast
import json
from pathlib import Path
import re
import subprocess
import sys


def normalized(value: str) -> str:
    return re.sub(r"[^a-z0-9]+", "", value.lower())


def load_json(path: Path) -> dict:
    value = json.loads(path.read_text())
    if not isinstance(value, dict):
        raise SystemExit(f"{path}: expected a JSON object")
    return value


def load_cases(path: Path) -> list[dict]:
    cases: list[dict] = []
    for line_number, line in enumerate(path.read_text().splitlines(), 1):
        if not line.strip():
            continue
        value = json.loads(line)
        if not isinstance(value, dict):
            raise SystemExit(f"{path}:{line_number}: expected a JSON object")
        if not isinstance(value.get("id"), str) or not isinstance(value.get("question"), str):
            raise SystemExit(f"{path}:{line_number}: id and question must be strings")
        if not isinstance(value.get("expectedNodeIds"), list) or not value["expectedNodeIds"]:
            raise SystemExit(f"{path}:{line_number}: expectedNodeIds must be a non-empty list")
        cases.append(value)
    if not cases:
        raise SystemExit(f"{path}: no benchmark cases")
    return cases


def run(command: list[str]) -> str:
    result = subprocess.run(command, capture_output=True, text=True, check=False)
    if result.returncode != 0:
        raise SystemExit(f"command failed ({result.returncode}): {' '.join(command)}\n{result.stdout}{result.stderr}")
    return result.stdout


def ravel_names(binary: str, graph_dir: Path, question: str, budget: int) -> set[str]:
    output = run([
        binary,
        "context",
        "--json",
        "--out",
        str(graph_dir),
        "--token-budget",
        str(budget),
        question,
    ])
    value = json.loads(output)
    return {normalized(node["name"]) for node in value.get("nodes", []) if isinstance(node.get("name"), str)}


def graphify_names(binary: str, graph_path: Path, question: str, budget: int) -> set[str]:
    output = run([
        binary,
        "query",
        question,
        "--graph",
        str(graph_path),
        "--budget",
        str(budget),
    ])
    labels = [
        line[5:].split(" [", 1)[0]
        for line in output.splitlines()
        if line.startswith("NODE ")
    ]
    first_line = output.splitlines()[0] if output else ""
    marker = "Start: "
    if marker in first_line:
        raw = first_line.split(marker, 1)[1].split(" |", 1)[0]
        try:
            starts = ast.literal_eval(raw)
        except (SyntaxError, ValueError) as error:
            raise SystemExit(f"cannot parse Graphify start labels: {error}") from error
        if isinstance(starts, list):
            labels.extend(str(value) for value in starts)
    return {normalized(label) for label in labels}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--ravel", default="ravel", help="Ravel binary")
    parser.add_argument("--ravel-graph", type=Path, required=True, help="directory containing Ravel graph.json")
    parser.add_argument("--graphify", default="graphify", help="Graphify binary")
    parser.add_argument("--graphify-graph", type=Path, required=True, help="clustered Graphify graph.json")
    parser.add_argument("--dataset", type=Path, required=True, help="Ravel repository-question JSONL")
    parser.add_argument("--token-budget", type=int, default=800)
    parser.add_argument("--out", type=Path)
    args = parser.parse_args()
    if args.token_budget < 64:
        raise SystemExit("--token-budget must be at least 64")

    ravel_graph = load_json(args.ravel_graph / "graph.json")
    graphify_graph = load_json(args.graphify_graph)
    if "links" not in graphify_graph:
        raise SystemExit("Graphify graph must be clustered node-link JSON with a links field; run graphify cluster-only first")
    nodes_by_id = {node.get("id"): node for node in ravel_graph.get("nodes", []) if isinstance(node, dict)}
    cases = load_cases(args.dataset)

    results = []
    for case in cases:
        expected = []
        for node_id in case["expectedNodeIds"]:
            node = nodes_by_id.get(node_id)
            if not isinstance(node, dict) or not isinstance(node.get("name"), str):
                raise SystemExit(f"{case['id']}: expected node missing from Ravel graph: {node_id}")
            expected.append(node["name"])
        expected_normalized = [normalized(name) for name in expected]
        ravel_returned = ravel_names(args.ravel, args.ravel_graph, case["question"], args.token_budget)
        graphify_returned = graphify_names(args.graphify, args.graphify_graph, case["question"], args.token_budget)
        results.append({
            "id": case["id"],
            "expectedNames": expected,
            "ravelNameRecall": sum(name in ravel_returned for name in expected_normalized) / len(expected_normalized),
            "graphifyNameRecall": sum(name in graphify_returned for name in expected_normalized) / len(expected_normalized),
        })

    output = {
        "version": 2,
        "metric": "normalized_expected_symbol_name_recall",
        "tokenBudget": args.token_budget,
        "cases": len(results),
        "ravelVersion": run([args.ravel, "version"]).strip(),
        "graphifyVersion": run([args.graphify, "--version"]).strip(),
        "ravelGraphNodes": len(ravel_graph.get("nodes", [])),
        "ravelGraphEdges": len(ravel_graph.get("edges", [])),
        "graphifyGraphNodes": len(graphify_graph.get("nodes", [])),
        "graphifyGraphEdges": len(graphify_graph.get("links", [])),
        "ravelMeanNameRecall": sum(row["ravelNameRecall"] for row in results) / len(results),
        "graphifyMeanNameRecall": sum(row["graphifyNameRecall"] for row in results) / len(results),
        "limitations": "Compatibility metric only: different graph IDs prevent evidence-ID comparison; no model answers or judge are used.",
        "results": results,
    }
    rendered = json.dumps(output, indent=2) + "\n"
    if args.out:
        args.out.write_text(rendered)
        print(f"Wrote comparison to {args.out}")
    else:
        sys.stdout.write(rendered)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
