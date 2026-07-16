#!/usr/bin/env python3
"""Run the pinned multi-repository retrieval quality gate."""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import tempfile
from pathlib import Path


REVISION = re.compile(r"^[0-9a-f]{40}$")


def load_json(path: Path) -> dict:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"{path} must contain a JSON object")
    return value


def load_cases(path: Path) -> list[dict]:
    cases: list[dict] = []
    seen: set[str] = set()
    for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if not line.strip():
            continue
        item = json.loads(line)
        case_id = item.get("id")
        if not isinstance(case_id, str) or not case_id:
            raise ValueError(f"{path}:{line_number} has no case id")
        if case_id in seen:
            raise ValueError(f"{path}:{line_number} duplicates case id {case_id!r}")
        seen.add(case_id)
        cases.append(item)
    if not cases:
        raise ValueError(f"{path} contains no cases")
    return cases


def resolve_repo_file(root: Path, value: object, label: str) -> Path:
    if not isinstance(value, str) or not value:
        raise ValueError(f"{label} must be a non-empty repository-relative path")
    path = (root / value).resolve()
    try:
        path.relative_to(root)
    except ValueError as exc:
        raise ValueError(f"{label} escapes the repository: {value}") from exc
    if not path.is_file():
        raise ValueError(f"{label} does not exist: {value}")
    return path


def validate_manifest(root: Path, manifest_path: Path) -> tuple[dict, list[tuple[dict, Path, int]], Path]:
    manifest = load_json(manifest_path)
    allowed = {
        "version",
        "minimumTotalCases",
        "retriever",
        "topK",
        "tokenBudget",
        "qualityGate",
        "repositories",
    }
    unknown = sorted(set(manifest) - allowed)
    if unknown:
        raise ValueError(f"manifest has unknown fields: {', '.join(unknown)}")
    if manifest.get("version") != 1:
        raise ValueError("manifest version must be 1")
    minimum_total = manifest.get("minimumTotalCases")
    if not isinstance(minimum_total, int) or minimum_total < 50:
        raise ValueError("minimumTotalCases must be at least 50")
    if manifest.get("retriever") not in {"context", "flat"}:
        raise ValueError("retriever must be context or flat")
    for field in ("topK", "tokenBudget"):
        if not isinstance(manifest.get(field), int) or manifest[field] < 1:
            raise ValueError(f"{field} must be positive")

    gate_path = resolve_repo_file(root, manifest.get("qualityGate"), "qualityGate")
    gate = load_json(gate_path)
    if gate.get("version") != 1 or gate.get("requireFreshExpectations") is not True:
        raise ValueError("quality gate must be version 1 and require fresh expectations")

    repositories = manifest.get("repositories")
    if not isinstance(repositories, list) or len(repositories) < 2:
        raise ValueError("repositories must contain at least two pinned repositories")
    seen_names: set[str] = set()
    seen_cases: set[str] = set()
    validated: list[tuple[dict, Path, int]] = []
    total_cases = 0
    for item in repositories:
        if not isinstance(item, dict):
            raise ValueError("each repository entry must be an object")
        required = {"name", "url", "revision", "dataset", "datasetRevision"}
        if set(item) != required:
            raise ValueError(f"repository fields must be exactly: {', '.join(sorted(required))}")
        name = item["name"]
        if not isinstance(name, str) or not re.fullmatch(r"[a-z0-9-]+", name) or name in seen_names:
            raise ValueError(f"invalid or duplicate repository name: {name!r}")
        seen_names.add(name)
        if not isinstance(item["url"], str) or not item["url"].startswith("https://github.com/"):
            raise ValueError(f"repository {name} must use an HTTPS GitHub URL")
        if not isinstance(item["revision"], str) or not REVISION.fullmatch(item["revision"]):
            raise ValueError(f"repository {name} revision must be a full lowercase commit SHA")
        if not isinstance(item["datasetRevision"], str) or not item["datasetRevision"]:
            raise ValueError(f"repository {name} datasetRevision must be non-empty")
        dataset_path = resolve_repo_file(root, item["dataset"], f"repository {name} dataset")
        cases = load_cases(dataset_path)
        for case in cases:
            if case["id"] in seen_cases:
                raise ValueError(f"duplicate cross-repository case id: {case['id']}")
            seen_cases.add(case["id"])
        total_cases += len(cases)
        validated.append((item, dataset_path, len(cases)))
    if total_cases < minimum_total:
        raise ValueError(f"suite has {total_cases} cases, below minimum {minimum_total}")
    return manifest, validated, gate_path


def run(command: list[str], cwd: Path | None = None) -> None:
    subprocess.run(command, cwd=cwd, check=True)


def checkout_repository(item: dict, destination: Path) -> None:
    destination.mkdir(parents=True)
    run(["git", "init", "--quiet"], destination)
    run(["git", "remote", "add", "origin", item["url"]], destination)
    run(["git", "fetch", "--quiet", "--depth", "1", "origin", item["revision"]], destination)
    run(["git", "checkout", "--quiet", "--detach", "FETCH_HEAD"], destination)
    actual = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=destination, text=True).strip()
    if actual != item["revision"]:
        raise RuntimeError(f"repository {item['name']} resolved to {actual}, expected {item['revision']}")


def execute_suite(
    root: Path,
    ravel: Path,
    workspace: Path,
    manifest: dict,
    repositories: list[tuple[dict, Path, int]],
    gate_path: Path,
) -> None:
    workspace.mkdir(parents=True, exist_ok=True)
    repos_dir = workspace / "repositories"
    graphs_dir = workspace / "graphs"
    results_dir = workspace / "results"
    for directory in (repos_dir, graphs_dir, results_dir):
        directory.mkdir(exist_ok=True)

    for item, dataset_path, case_count in repositories:
        name = item["name"]
        repo_dir = repos_dir / name
        graph_dir = graphs_dir / name
        result_path = results_dir / f"{name}.json"
        if repo_dir.exists() and any(repo_dir.iterdir()):
            raise RuntimeError(f"workspace repository directory is not empty: {repo_dir}")
        print(f"[{name}] checkout {item['revision']} ({case_count} cases)", flush=True)
        checkout_repository(item, repo_dir)
        run([str(ravel), "audit", str(repo_dir)], root)
        run([str(ravel), "build", "--out", str(graph_dir), str(repo_dir)], root)
        run(
            [
                str(ravel),
                "benchmark",
                "--graph",
                str(graph_dir),
                "--dataset",
                str(dataset_path),
                "--gate",
                str(gate_path),
                "--retriever",
                manifest["retriever"],
                "--top-k",
                str(manifest["topK"]),
                "--token-budget",
                str(manifest["tokenBudget"]),
                "--dataset-revision",
                item["datasetRevision"],
                "--graph-revision",
                item["revision"],
                "--out",
                str(result_path),
            ],
            root,
        )
    print(f"Retrieval quality gate passed. Results: {results_dir}")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", default="benchmarks/external/suite.json")
    parser.add_argument("--ravel", help="Ravel binary used for graph builds and benchmarks")
    parser.add_argument("--workspace", help="persistent working directory; defaults to a temporary directory")
    parser.add_argument("--check", action="store_true", help="validate the suite without cloning or running Ravel")
    args = parser.parse_args()

    root = Path(__file__).resolve().parent.parent
    manifest_path = resolve_repo_file(root, args.manifest, "manifest")
    manifest, repositories, gate_path = validate_manifest(root, manifest_path)
    total = sum(count for _, _, count in repositories)
    print(f"Validated {len(repositories)} pinned repositories and {total} retrieval cases.")
    if args.check:
        return
    if not args.ravel:
        parser.error("--ravel is required unless --check is used")
    ravel = Path(args.ravel).resolve()
    if not ravel.is_file():
        parser.error(f"Ravel binary does not exist: {ravel}")
    if args.workspace:
        execute_suite(root, ravel, Path(args.workspace).resolve(), manifest, repositories, gate_path)
        return
    with tempfile.TemporaryDirectory(prefix="ravel-retrieval-quality-") as temporary:
        execute_suite(root, ravel, Path(temporary), manifest, repositories, gate_path)


if __name__ == "__main__":
    main()
