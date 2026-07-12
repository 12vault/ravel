#!/usr/bin/env python3
"""Compare compact query payload sizes for Ravel and Graphify."""

from __future__ import annotations

import argparse
from datetime import date
import json
from pathlib import Path
import subprocess


def run(command: list[str]) -> str:
    result = subprocess.run(command, capture_output=True, text=True, check=False)
    if result.returncode != 0:
        raise SystemExit(
            f"command failed ({result.returncode}): {' '.join(command)}\n"
            f"{result.stdout}{result.stderr}"
        )
    return result.stdout


def estimated_tokens(value: str) -> int:
    return (len(value.encode("utf-8")) + 2) // 3


def graph_size(path: Path) -> tuple[int, int]:
    graph = json.loads(path.read_text())
    return len(graph.get("nodes", [])), len(graph.get("edges", graph.get("links", [])))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--ravel", default="ravel")
    parser.add_argument("--ravel-graph", type=Path, required=True)
    parser.add_argument("--graphify", default="graphify")
    parser.add_argument("--graphify-graph", type=Path, required=True)
    parser.add_argument("--questions", type=Path, required=True)
    parser.add_argument("--token-budget", type=int, default=800)
    parser.add_argument("--repository")
    parser.add_argument("--revision")
    parser.add_argument("--out", type=Path)
    args = parser.parse_args()

    questions = json.loads(args.questions.read_text())
    if not isinstance(questions, list) or not questions:
        raise SystemExit("questions must be a non-empty JSON array")

    rows = []
    for item in questions:
        case_id, question = item.get("id"), item.get("question")
        if not isinstance(case_id, str) or not isinstance(question, str):
            raise SystemExit("every question requires string id and question fields")
        ravel_output = run([
            args.ravel, "context", "--out", str(args.ravel_graph),
            "--token-budget", str(args.token_budget), question,
        ])
        graphify_output = run([
            args.graphify, "query", question, "--graph", str(args.graphify_graph),
            "--budget", str(args.token_budget),
        ])
        rows.append({
            "id": case_id,
            "question": question,
            "ravelBytes": len(ravel_output.encode("utf-8")),
            "ravelEstimatedTokens": estimated_tokens(ravel_output),
            "graphifyBytes": len(graphify_output.encode("utf-8")),
            "graphifyEstimatedTokens": estimated_tokens(graphify_output),
        })

    ravel_nodes, ravel_edges = graph_size(args.ravel_graph / "graph.json")
    graphify_nodes, graphify_edges = graph_size(args.graphify_graph)
    output = {
        "version": 1,
        "measuredAt": date.today().isoformat(),
        "metric": "compact_context_payload_estimated_tokens",
        "tokenEstimate": "ceil(UTF-8 bytes / 3)",
        "tokenBudget": args.token_budget,
        "cases": len(rows),
        "repository": args.repository,
        "revision": args.revision,
        "ravelVersion": run([args.ravel, "version"]).strip(),
        "graphifyVersion": run([args.graphify, "--version"]).strip(),
        "ravelGraphNodes": ravel_nodes,
        "ravelGraphEdges": ravel_edges,
        "graphifyGraphNodes": graphify_nodes,
        "graphifyGraphEdges": graphify_edges,
        "ravelEstimatedTokens": sum(row["ravelEstimatedTokens"] for row in rows),
        "graphifyEstimatedTokens": sum(row["graphifyEstimatedTokens"] for row in rows),
        "limitations": "Payload size only; this does not measure answer correctness or model billing.",
        "results": rows,
    }
    rendered = json.dumps(output, indent=2) + "\n"
    if args.out:
        args.out.write_text(rendered)
        print(f"Wrote comparison to {args.out}")
    else:
        print(rendered, end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
