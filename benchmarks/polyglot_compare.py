#!/usr/bin/env python3
"""Shared Ravel/Graphify execution and scoring helpers for polyglot benchmarks."""

from __future__ import annotations

import ast
import hashlib
import json
import math
from pathlib import Path
import queue
import re
import select
import shutil
import subprocess
import tempfile
import time
from typing import Iterable


COMMON_ADAPTER_VERSION = "ravel-graphify-polyglot-v1"
RAVEL_EXECUTION_ADAPTER_VERSION = "ravel-context-batch-v2"
NORMALIZER = re.compile(r"[^a-z0-9]+")
RAVEL_PROFILES = {
    "compact": (),
    "broad": (
        "--seed-limit", "20",
        "--max-depth", "3",
        "--max-nodes", "10000",
        "--branch-fanout", "10000",
        "--candidate-shortlist",
    ),
}


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def stable_hash(*values: object, length: int = 24) -> str:
    payload = "\0".join(str(value) for value in values).encode("utf-8")
    return hashlib.sha256(payload).hexdigest()[:length]


def executable_metadata(command: str) -> dict:
    resolved = shutil.which(command) or command
    path = Path(resolved).resolve()
    value = {"path": str(path)}
    if path.is_file():
        value["sha256"] = sha256_file(path)
    return value


def normalized(value: str) -> str:
    return NORMALIZER.sub("", value.lower())


def canonical_path(value: str, root: Path | None = None) -> str:
    value = value.replace("\\", "/").strip()
    if value.startswith("file://"):
        value = value[7:]
    if root is not None and value:
        try:
            value = Path(value).resolve().relative_to(root.resolve()).as_posix()
        except (OSError, ValueError):
            pass
    while value.startswith("./"):
        value = value[2:]
    return value.lstrip("/")


def paths_match(predicted: str, gold: str) -> bool:
    predicted = canonical_path(predicted)
    gold = canonical_path(gold)
    return bool(predicted and gold) and (
        predicted == gold
        or predicted.endswith("/" + gold)
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


def ravel_result(
    binary: str,
    graph: Path,
    question: str,
    budget: int,
    profile: str,
) -> dict:
    output, query_ms = run_command(
        [
            binary,
            "context",
            "--json",
            "--out",
            str(graph),
            "--token-budget",
            str(budget),
            *RAVEL_PROFILES[profile],
            question,
        ],
        graph.parent,
    )
    return ravel_result_from_value(json.loads(output), query_ms, profile)


def ravel_result_from_value(
    value: dict,
    query_ms: float,
    profile: str,
    *,
    round_trip_ms: float | None = None,
    queue_wait_ms: float | None = None,
) -> dict:
    stats = value.get("stats") or {}
    items = []
    for node in value.get("nodes") or []:
        if not isinstance(node, dict):
            continue
        items.append({
            "id": str(node.get("id", "")),
            "name": str(node.get("name", "")),
            "path": str(node.get("path", "")),
            "startLine": int(node.get("startLine") or 0),
            "endLine": int(node.get("endLine") or node.get("startLine") or 0),
        })
    result = {
        "queryMs": query_ms,
        "profile": profile,
        "estimatedTokens": int(stats.get("estimatedTokens") or 0),
        "headerTokens": int(stats.get("headerTokens") or 0),
        "candidateTokens": int(stats.get("candidateTokens") or 0),
        "explanationTokens": int(stats.get("explanationTokens") or 0),
        "truncated": bool(stats.get("truncated")),
        "truncatedReasons": [str(reason) for reason in stats.get("truncatedReason") or []],
        "exploredNodes": int(stats.get("exploredNodes") or 0),
        "lexicalCandidates": int(stats.get("lexicalCandidates") or 0),
        "deduplicatedNodes": int(stats.get("deduplicatedNodes") or 0),
        "unselectedNodes": int(stats.get("unselectedNodes") or 0),
        "explanationEdgesOmitted": int(stats.get("explanationEdgesOmitted") or 0),
        "sameFileRescues": list(stats.get("sameFileRescues") or []),
        "affinityRescues": list(stats.get("affinityRescues") or []),
        "traceNodes": list(stats.get("traceNodes") or []),
        "items": items,
    }
    if round_trip_ms is not None:
        result["roundTripMs"] = round_trip_ms
    if queue_wait_ms is not None:
        result["queueWaitMs"] = queue_wait_ms
    return result


class RavelBatchSession:
    """One fixed-snapshot Ravel context-batch process."""

    def __init__(
        self,
        binary: str,
        graph: Path,
        budget: int,
        profile: str,
        timeout_seconds: float,
        session_id: int,
    ) -> None:
        self.profile = profile
        self.timeout_seconds = timeout_seconds
        self.session_id = session_id
        self.request_id = 0
        self.stderr = tempfile.TemporaryFile(mode="w+", encoding="utf-8")
        command = [
            binary,
            "context-batch",
            "--out",
            str(graph),
            "--token-budget",
            str(budget),
            *RAVEL_PROFILES[profile],
        ]
        started = time.perf_counter()
        self.process = subprocess.Popen(
            command,
            cwd=graph.parent,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=self.stderr,
            text=True,
            bufsize=1,
        )
        try:
            ready = self._read_message()
            self.startup_ms = (time.perf_counter() - started) * 1000
            if ready.get("type") != "ready" or ready.get("version") != 1:
                raise RuntimeError(f"invalid context-batch readiness message: {ready!r}")
            self.ready = ready
        except Exception:
            self.close()
            raise

    def _stderr_text(self) -> str:
        self.stderr.flush()
        self.stderr.seek(0)
        return self.stderr.read()[-4000:]

    def _read_message(self) -> dict:
        if self.process.stdout is None:
            raise RuntimeError("context-batch stdout is unavailable")
        readable, _, _ = select.select([self.process.stdout], [], [], self.timeout_seconds)
        if not readable:
            raise TimeoutError(f"context-batch timed out after {self.timeout_seconds:g}s")
        line = self.process.stdout.readline()
        if not line:
            code = self.process.poll()
            raise RuntimeError(
                f"context-batch exited before a response (code={code}): {self._stderr_text()}"
            )
        try:
            value = json.loads(line)
        except json.JSONDecodeError as error:
            raise RuntimeError(f"invalid context-batch JSON: {line[:500]!r}") from error
        if not isinstance(value, dict):
            raise RuntimeError(f"invalid context-batch message: {value!r}")
        return value

    def query(self, question: str, trace_node_ids: list[str] | None = None) -> dict:
        if self.process.stdin is None:
            raise RuntimeError("context-batch stdin is unavailable")
        self.request_id += 1
        request_id = f"{self.session_id}-{self.request_id}"
        started = time.perf_counter()
        request = {"id": request_id, "question": question}
        if trace_node_ids:
            request["traceNodeIds"] = trace_node_ids
        self.process.stdin.write(json.dumps(request) + "\n")
        self.process.stdin.flush()
        response = self._read_message()
        round_trip_ms = (time.perf_counter() - started) * 1000
        if response.get("id") != request_id:
            raise RuntimeError(
                f"context-batch response id mismatch: expected {request_id!r}, found {response.get('id')!r}"
            )
        if response.get("type") == "error":
            raise RuntimeError(f"context-batch query failed: {response.get('error', 'unknown error')}")
        retrieval = response.get("retrieval")
        if response.get("type") != "result" or not isinstance(retrieval, dict):
            raise RuntimeError(f"invalid context-batch result: {response!r}")
        return ravel_result_from_value(
            retrieval,
            float(response.get("queryMs") or 0),
            self.profile,
            round_trip_ms=round_trip_ms,
        )

    def metadata(self) -> dict:
        return {
            "session": self.session_id,
            "startupMs": self.startup_ms,
            "graphLoadMs": float(self.ready.get("graphLoadMs") or 0),
            "indexBuildMs": float(self.ready.get("indexBuildMs") or 0),
            "graphNodes": int(self.ready.get("graphNodes") or 0),
            "graphEdges": int(self.ready.get("graphEdges") or 0),
        }

    def close(self) -> None:
        process = getattr(self, "process", None)
        if process is not None and process.poll() is None:
            if process.stdin is not None:
                try:
                    process.stdin.close()
                except OSError:
                    pass
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
        if process is not None:
            for stream in (process.stdin, process.stdout):
                if stream is not None and not stream.closed:
                    stream.close()
        stderr = getattr(self, "stderr", None)
        if stderr is not None:
            stderr.close()


class RavelBatchPool:
    """A bounded pool; each session handles one request at a time."""

    def __init__(
        self,
        binary: str,
        graph: Path,
        budget: int,
        profile: str,
        sessions: int,
        timeout_seconds: float,
    ) -> None:
        self.sessions: list[RavelBatchSession] = []
        self.available: queue.Queue[RavelBatchSession] = queue.Queue()
        try:
            for session_id in range(1, sessions + 1):
                session = RavelBatchSession(
                    binary, graph, budget, profile, timeout_seconds, session_id
                )
                self.sessions.append(session)
                self.available.put(session)
        except Exception:
            self.close()
            raise

    def query(self, question: str, trace_node_ids: list[str] | None = None) -> dict:
        waiting = time.perf_counter()
        session = self.available.get()
        queue_wait_ms = (time.perf_counter() - waiting) * 1000
        try:
            result = session.query(question, trace_node_ids)
            result["queueWaitMs"] = queue_wait_ms
            return result
        finally:
            self.available.put(session)

    def metadata(self) -> dict:
        values = [session.metadata() for session in self.sessions]
        return {
            "executionAdapterVersion": RAVEL_EXECUTION_ADAPTER_VERSION,
            "mode": "context-batch-jsonl",
            "latencySemantics": {
                "queryMs": "server retrieval on a warm reusable index",
                "roundTripMs": "JSONL write through response read; excludes pool wait",
                "queueWaitMs": "wait for an available Ravel session",
                "comparableToGraphifyQueryMs": False,
            },
            "sessions": values,
        }

    def close(self) -> None:
        for session in self.sessions:
            session.close()


def graphify_result(binary: str, graph: Path, question: str, budget: int) -> dict:
    output, query_ms = run_command(
        [binary, "query", question, "--budget", str(budget), "--graph", str(graph)],
        graph.parent,
    )
    items = []
    lines = output.splitlines()
    first_line = lines[0] if lines else ""
    if "Start: " in first_line:
        raw = first_line.split("Start: ", 1)[1].split(" |", 1)[0]
        try:
            starts = ast.literal_eval(raw)
        except (SyntaxError, ValueError):
            starts = []
        if isinstance(starts, list):
            items.extend({"name": str(label), "path": "", "startLine": 0, "endLine": 0} for label in starts)
    node_pattern = re.compile(r"\[.*?src=(.+?)\s+loc=L(\d+)(?:-L?(\d+))?")
    for line in lines:
        if not line.startswith("NODE "):
            continue
        name = line[5:].split(" [", 1)[0]
        match = node_pattern.search(line)
        start_line = int(match.group(2)) if match else 0
        end_line = int(match.group(3) or match.group(2)) if match else 0
        items.append({
            "name": name,
            "path": match.group(1) if match else "",
            "startLine": start_line,
            "endLine": end_line,
        })
    deduplicated = []
    seen = set()
    for item in items:
        key = (normalized(item["name"]), item["path"], item["startLine"], item["endLine"])
        if key in seen:
            continue
        seen.add(key)
        deduplicated.append(item)
    return {
        "queryMs": query_ms,
        "estimatedTokens": math.ceil(len(output.encode("utf-8")) / 3),
        "truncated": "truncated" in output.lower(),
        "items": deduplicated,
    }


def build_ravel(binary: str, repository: Path, output: Path) -> float:
    output.parent.mkdir(parents=True, exist_ok=True)
    _, elapsed_ms = run_command(
        [binary, "build", "--out", str(output), str(repository)],
        output.parent,
    )
    return elapsed_ms


def build_graphify(binary: str, repository: Path, output: Path) -> tuple[Path, float, float]:
    output.mkdir(parents=True, exist_ok=True)
    _, extract_ms = run_command(
        [
            binary,
            "extract",
            str(repository),
            "--code-only",
            "--no-cluster",
            "--max-workers",
            "1",
            "--out",
            str(output),
        ],
        output.parent,
    )
    graph = output / "graphify-out" / "graph.json"
    _, cluster_ms = run_command(
        [
            binary,
            "cluster-only",
            str(output),
            "--graph",
            str(graph),
            "--no-label",
            "--no-viz",
        ],
        output.parent,
    )
    return graph, extract_ms, cluster_ms


def score_identifier_retrieval(
    items: list[dict],
    gold_identifiers: Iterable[str],
    identifier_paths: dict[str, list[str]],
) -> dict:
    gold = [value for value in dict.fromkeys(gold_identifiers) if normalized(value)]
    matched: set[str] = set()
    first_rank = None
    for rank, item in enumerate(items, 1):
        item_name = normalized(str(item.get("name", "")))
        item_path = str(item.get("path", ""))
        item_hits = {
            identifier
            for identifier in gold
            if item_name == normalized(identifier)
            or any(paths_match(item_path, path) for path in identifier_paths.get(identifier, []))
        }
        if item_hits and first_rank is None:
            first_rank = rank
        matched.update(item_hits)
    recall = len(matched) / len(gold) if gold else 0.0
    return {
        "hit": bool(matched),
        "rank": first_rank,
        "reciprocalRank": 0.0 if first_rank is None else 1.0 / first_rank,
        "goldIdentifierRecall": recall,
        "matchedGoldIdentifiers": sorted(matched),
    }


def _merge_intervals(intervals: Iterable[tuple[int, int]]) -> list[tuple[int, int]]:
    ordered = sorted((max(1, start), max(max(1, start), end)) for start, end in intervals if end > 0)
    merged: list[list[int]] = []
    for start, end in ordered:
        if not merged or start > merged[-1][1] + 1:
            merged.append([start, end])
        else:
            merged[-1][1] = max(merged[-1][1], end)
    return [(start, end) for start, end in merged]


def _interval_size(intervals: Iterable[tuple[int, int]]) -> int:
    return sum(end - start + 1 for start, end in intervals)


def _overlap_size(left: list[tuple[int, int]], right: list[tuple[int, int]]) -> int:
    total = 0
    left_index = 0
    right_index = 0
    while left_index < len(left) and right_index < len(right):
        left_start, left_end = left[left_index]
        right_start, right_end = right[right_index]
        total += max(0, min(left_end, right_end) - max(left_start, right_start) + 1)
        if left_end < right_end:
            left_index += 1
        else:
            right_index += 1
    return total


def score_span_retrieval(items: list[dict], gold_spans: list[dict]) -> dict:
    gold_by_path: dict[str, list[tuple[int, int]]] = {}
    for span in gold_spans:
        path = canonical_path(str(span.get("file", "")))
        if not path:
            continue
        gold_by_path.setdefault(path, []).append(
            (int(span.get("start_line") or 0), int(span.get("end_line") or 0))
        )
    gold_by_path = {path: _merge_intervals(intervals) for path, intervals in gold_by_path.items()}

    predicted_by_path: dict[str, list[tuple[int, int]]] = {}
    first_rank = None
    for rank, item in enumerate(items, 1):
        predicted = canonical_path(str(item.get("path", "")))
        if not predicted:
            continue
        matched_path = next((path for path in gold_by_path if paths_match(predicted, path)), None)
        key = matched_path or predicted
        predicted_by_path.setdefault(key, []).append(
            (int(item.get("startLine") or 0), int(item.get("endLine") or 0))
        )
        if matched_path is not None and first_rank is None:
            first_rank = rank
    predicted_by_path = {
        path: _merge_intervals(intervals) for path, intervals in predicted_by_path.items()
    }

    gold_files = set(gold_by_path)
    predicted_files = set(predicted_by_path)
    matching_files = gold_files & predicted_files
    file_recall = len(matching_files) / len(gold_files) if gold_files else 0.0
    file_precision = len(matching_files) / len(predicted_files) if predicted_files else 0.0
    file_f1 = (
        2 * file_precision * file_recall / (file_precision + file_recall)
        if file_precision + file_recall
        else 0.0
    )

    gold_lines = sum(_interval_size(intervals) for intervals in gold_by_path.values())
    predicted_lines = sum(_interval_size(intervals) for intervals in predicted_by_path.values())
    overlapping_lines = sum(
        _overlap_size(predicted_by_path.get(path, []), intervals)
        for path, intervals in gold_by_path.items()
    )
    return {
        "hit": bool(matching_files),
        "rank": first_rank,
        "reciprocalRank": 0.0 if first_rank is None else 1.0 / first_rank,
        "fileRecall": file_recall,
        "filePrecision": file_precision,
        "fileF1": file_f1,
        "lineRecall": overlapping_lines / gold_lines if gold_lines else 0.0,
        "linePrecision": overlapping_lines / predicted_lines if predicted_lines else 0.0,
        "goldFiles": len(gold_files),
        "returnedFiles": len(predicted_files),
        "overlappingLines": overlapping_lines,
    }
