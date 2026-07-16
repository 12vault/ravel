from __future__ import annotations

import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import polyglot_compare
import run_codesearchnet_go
import run_contextbench
import run_crosscodeeval_typescript
import run_ghostty_swift
import run_real_fim_scale


class PolyglotCompareTests(unittest.TestCase):
    def test_graphify_result_parses_paths_and_line_ranges(self) -> None:
        output = "\n".join((
            "Start: ['apiReport'] | budget=2000",
            "NODE apiReport [kind=function src=contexts/001/src/apiReport.ts loc=L10-L24]",
            "NODE Config [kind=class src=config/config.go loc=L17]",
        ))
        with mock.patch.object(polyglot_compare, "run_command", return_value=(output, 12.5)):
            result = polyglot_compare.graphify_result("graphify", Path("graph.json"), "query", 2000)
        self.assertEqual(result["queryMs"], 12.5)
        self.assertEqual(result["items"][1]["path"], "contexts/001/src/apiReport.ts")
        self.assertEqual(result["items"][1]["startLine"], 10)
        self.assertEqual(result["items"][1]["endLine"], 24)
        self.assertEqual(result["items"][2]["endLine"], 17)

    def test_identifier_score_accepts_name_or_materialized_path(self) -> None:
        items = [
            {"name": "other", "path": "contexts/003/src/apiReport.ts"},
            {"name": "saveRequestMock", "path": ""},
        ]
        score = polyglot_compare.score_identifier_retrieval(
            items,
            ["apiReport", "saveRequestMock"],
            {"apiReport": ["src/apiReport.ts"], "saveRequestMock": ["src/apiCache.ts"]},
        )
        self.assertTrue(score["hit"])
        self.assertEqual(score["rank"], 1)
        self.assertEqual(score["goldIdentifierRecall"], 1.0)

    def test_span_score_merges_overlapping_gold_and_prediction_ranges(self) -> None:
        gold = [
            {"file": "pkg/a.go", "start_line": 10, "end_line": 20},
            {"file": "pkg/a.go", "start_line": 15, "end_line": 25},
            {"file": "pkg/b.go", "start_line": 1, "end_line": 5},
        ]
        items = [
            {"path": "/tmp/repo/pkg/a.go", "startLine": 18, "endLine": 22},
            {"path": "wrong.go", "startLine": 1, "endLine": 2},
        ]
        score = polyglot_compare.score_span_retrieval(items, gold)
        self.assertEqual(score["fileRecall"], 0.5)
        self.assertEqual(score["filePrecision"], 0.5)
        self.assertEqual(score["overlappingLines"], 5)
        self.assertAlmostEqual(score["lineRecall"], 5 / 21)
        self.assertAlmostEqual(score["linePrecision"], 5 / 7)


class CrossCodeEvalTests(unittest.TestCase):
    def test_derives_gold_from_referenced_typescript_import(self) -> None:
        base = {
            "prompt": "import { apiReport } from './apiReport';\nasync function run() {",
            "groundtruth": "  await apiReport({ value });",
        }
        chunks = [{
            "filename": "src/apiReport.ts",
            "chunk": "export const apiReport = async (value: Input) => value;",
        }]
        gold, paths, source = run_crosscodeeval_typescript.derive_gold(base, chunks)
        self.assertEqual(gold, ["apiReport"])
        self.assertEqual(paths, {"apiReport": ["src/apiReport.ts"]})
        self.assertEqual(source, "referenced-import")

    def test_safe_relative_rejects_parent_traversal(self) -> None:
        self.assertEqual(
            run_crosscodeeval_typescript.safe_relative("../../src/value", "fallback.ts"),
            Path("src/value.ts"),
        )


class ContextBenchTests(unittest.TestCase):
    def test_parses_gold_context_json(self) -> None:
        raw = json.dumps([
            {"file": "config/config.go", "start_line": 17, "end_line": 20, "content": "type Config struct"}
        ])
        self.assertEqual(
            run_contextbench.parse_gold_context(raw),
            [{
                "file": "config/config.go",
                "start_line": 17,
                "end_line": 20,
                "content": "type Config struct",
            }],
        )

    def test_checkout_marker_lives_outside_repository(self) -> None:
        case = {"repoUrl": "https://example.test/repo.git", "graphKey": "abc"}
        with tempfile.TemporaryDirectory() as temporary:
            base, checkout = run_contextbench.repository_paths(Path(temporary), case)
            self.assertNotEqual(base, checkout)
            self.assertNotIn(".contextbench-checkout", checkout.as_posix())


class RealFIMScaleTests(unittest.TestCase):
    def test_safe_relative_rejects_parent_traversal(self) -> None:
        self.assertEqual(
            run_real_fim_scale.safe_relative("../../src/value", "fallback", "go"),
            Path("src/value.go"),
        )

    def test_add_materialization_and_query_exclude_hidden_solution(self) -> None:
        hidden = "return secretSolution()"
        row = {
            "prompt": "package sample\nfunc value() int {",
            "suffix": "}\n",
            "canonical_solution": hidden,
        }
        case = {"split": "add", "language": "go", "path": "sample.go"}
        source = run_real_fim_scale.materialized_source(row, "add", "go")
        query = run_real_fim_scale.query_text(case, row)
        self.assertIn("<REAL_FIM_HOLE>", source)
        self.assertNotIn(hidden, source)
        self.assertNotIn(hidden, query)

    def test_edit_materialization_includes_removed_code_not_hidden_solution(self) -> None:
        hidden = "const replacement = buildNewValue();"
        removed = "const oldValue = buildOldValue();"
        row = {
            "prompt": "export function value() {",
            "suffix": "}\n",
            "to_remove": removed,
            "canonical_solution": hidden,
        }
        case = {"split": "edit", "language": "typescript", "path": "src/value.ts"}
        source = run_real_fim_scale.materialized_source(row, "edit", "typescript")
        query = run_real_fim_scale.query_text(case, row)
        self.assertIn(removed, source)
        self.assertIn(removed, query)
        self.assertNotIn(hidden, source)
        self.assertNotIn(hidden, query)


class CodeSearchNetGoTests(unittest.TestCase):
    def test_extracts_function_and_method_symbols(self) -> None:
        self.assertEqual(
            run_codesearchnet_go.extracted_symbol("func Build(value string) error { return nil }"),
            "Build",
        )
        self.assertEqual(
            run_codesearchnet_go.extracted_symbol(
                "func (client *Client) Fetch(ctx context.Context) error { return nil }"
            ),
            "Fetch",
        )

    def test_query_removes_comment_markers_and_redacts_gold_symbol(self) -> None:
        query = run_codesearchnet_go.query_text(
            "// Fetch waits for the remote value.\n// It returns the decoded object.",
            "Fetch",
        )
        self.assertNotIn("Fetch", query)
        self.assertNotIn("//", query)
        self.assertIn("[redacted symbol] waits for the remote value", query)

    def test_score_path_requires_the_exact_paired_file(self) -> None:
        items = [
            {"path": "functions/wrong/snippet.go"},
            {"path": "/tmp/corpus/functions/gold/snippet.go"},
        ]
        score = run_codesearchnet_go.score_path(items, "functions/gold/snippet.go")
        self.assertTrue(score["hit"])
        self.assertEqual(score["rank"], 2)
        self.assertEqual(score["reciprocalRank"], 0.5)

    def test_materialized_code_does_not_include_documentation(self) -> None:
        case = {"id": "gold", "goldPath": "functions/gold/snippet.go"}
        row = {
            "func_code_string": "func Build() {}",
            "func_documentation_string": "// Build creates the value.",
        }
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            run_codesearchnet_go.materialize(root, [case], {"gold": row})
            source = (root / case["goldPath"]).read_text()
        self.assertIn("func Build()", source)
        self.assertNotIn("creates the value", source)

    def test_graph_node_count_detects_empty_extraction(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            graph = Path(temporary) / "graph.json"
            graph.write_text(json.dumps({"nodes": []}))
            self.assertEqual(run_codesearchnet_go.graph_node_count(graph), 0)
            graph.write_text(json.dumps({"nodes": [{"id": "function"}]}))
            self.assertEqual(run_codesearchnet_go.graph_node_count(graph), 1)


class GhosttySwiftTests(unittest.TestCase):
    def test_derives_documented_named_declaration_and_redacts_symbol(self) -> None:
        source = """/// Creates a Widget for the requested terminal.\n@MainActor\npublic final class Widget {\n}\n"""
        cases = run_ghostty_swift.cases_from_source(
            Path("macos/Sources/Widget.swift"), source, "revision"
        )
        self.assertEqual(len(cases), 1)
        self.assertEqual(cases[0]["goldKind"], "class")
        self.assertEqual(cases[0]["goldSymbol"], "Widget")
        self.assertEqual(cases[0]["goldLine"], 3)
        query = run_ghostty_swift.query_text(
            cases[0]["documentation"], cases[0]["goldSymbol"]
        )
        self.assertNotIn("Widget", query)
        self.assertIn("[redacted symbol]", query)

    def test_strips_documentation_without_changing_line_numbers(self) -> None:
        source = "/// First line.\n/// Second line.\nfunc build() {}\n"
        stripped = run_ghostty_swift.strip_documentation(source)
        self.assertEqual(stripped.count("\n"), source.count("\n"))
        self.assertNotIn("First line", stripped)
        self.assertEqual(stripped.splitlines()[2], "func build() {}")

    def test_score_requires_matching_path_symbol_and_declaration_line(self) -> None:
        case = {
            "goldPath": "macos/Sources/Widget.swift",
            "goldSymbol": "build",
            "goldLine": 42,
        }
        items = [
            {"path": case["goldPath"], "name": "wrong", "startLine": 42, "endLine": 42},
            {"path": case["goldPath"], "name": "build", "startLine": 7, "endLine": 7},
            {"path": f"/tmp/corpus/{case['goldPath']}", "name": "build", "startLine": 42, "endLine": 50},
        ]
        score = run_ghostty_swift.score_declaration(items, case)
        self.assertTrue(score["hit"])
        self.assertEqual(score["rank"], 3)
        self.assertEqual(score["reciprocalRank"], 1 / 3)

    def test_declaration_coverage_reports_each_swift_kind(self) -> None:
        cases = [
            {"goldPath": "A.swift", "goldSymbol": "A", "goldLine": 3, "goldKind": "struct"},
            {"goldPath": "A.swift", "goldSymbol": "run", "goldLine": 7, "goldKind": "func"},
        ]
        items = [{"path": "A.swift", "name": "A", "startLine": 3, "endLine": 10}]
        coverage = run_ghostty_swift.declaration_coverage(items, cases)
        self.assertEqual(coverage["covered"], 1)
        self.assertEqual(coverage["coverage"], 0.5)
        self.assertEqual(coverage["byKind"]["struct"]["coverage"], 1.0)
        self.assertEqual(coverage["byKind"]["func"]["coverage"], 0.0)

if __name__ == "__main__":
    unittest.main()
