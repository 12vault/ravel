package treeanalyzer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/12ya/reporavel/internal/graph"
	"github.com/12ya/reporavel/internal/scan"
)

func BenchmarkAnalyzePythonHundredFunctions(b *testing.B) {
	root := b.TempDir()
	path := filepath.Join(root, "service.py")
	var source strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&source, "def function_%d():\n    return %d\n\n", i, i)
	}
	if err := os.WriteFile(path, []byte(source.String()), 0o644); err != nil {
		b.Fatal(err)
	}
	files := []scan.File{{Path: "service.py", AbsPath: path, Language: "python"}}
	analyzer := New("python")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := analyzer.Analyze(context.Background(), root, files); err != nil {
			b.Fatal(err)
		}
	}
}

func TestPythonExtractsDefinitionsCallsHeritageAndImports(t *testing.T) {
	result := analyzeSources(t, "python", map[string]string{
		"service.py": "import os\n\nclass Base:\n    pass\n\nclass Child(Base):\n    def run(self):\n        helper()\n\ndef helper():\n    return os.getcwd()\n",
	})

	base := nodeNamed(result.Nodes, "Base")
	child := nodeNamed(result.Nodes, "Child")
	run := nodeNamed(result.Nodes, "run")
	helper := nodeNamed(result.Nodes, "helper")
	if base == nil || child == nil || run == nil || helper == nil {
		t.Fatalf("missing Python definitions: %#v", result.Nodes)
	}
	if child.Kind != graph.NodeClass || base.Kind != graph.NodeClass || run.Kind != graph.NodeMethod {
		t.Fatalf("unexpected definition kinds: Base=%q Child=%q run=%q", base.Kind, child.Kind, run.Kind)
	}
	if !hasEdge(result.Edges, graph.EdgeCalls, run.ID, helper.ID, "true") {
		t.Fatalf("expected run -> helper resolved call, edges = %#v", result.Edges)
	}
	if !hasEdge(result.Edges, graph.EdgeInherits, child.ID, base.ID, "true") {
		t.Fatalf("expected Child -> Base inheritance, edges = %#v", result.Edges)
	}
	if !hasNamedImport(result.Nodes, result.Edges, "os") {
		t.Fatalf("expected extracted os import, nodes = %#v edges = %#v", result.Nodes, result.Edges)
	}
	for _, edge := range result.Edges {
		if edge.Kind != graph.EdgeCalls {
			continue
		}
		target := nodeByID(result.Nodes, edge.To)
		if target != nil && target.Name == "os" {
			t.Fatalf("attribute call was incorrectly reduced to its receiver: %#v", edge)
		}
	}
}

func TestJavaScriptAndRustExtractLanguageNeutralSymbols(t *testing.T) {
	for _, test := range []struct {
		name     string
		language string
		path     string
		source   string
		caller   string
		callee   string
	}{
		{name: "javascript", language: "javascript", path: "app.js", source: "import value from './dep.js';\nfunction helper() { return value; }\nfunction main() { return helper(); }\n", caller: "main", callee: "helper"},
		{name: "rust", language: "rust", path: "main.rs", source: "use crate::dep;\nfn helper() -> i32 { 1 }\nfn main() { let _ = helper(); }\n", caller: "main", callee: "helper"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := analyzeSources(t, test.language, map[string]string{test.path: test.source})
			caller := nodeNamed(result.Nodes, test.caller)
			callee := nodeNamed(result.Nodes, test.callee)
			if caller == nil || callee == nil {
				t.Fatalf("missing definitions: %#v diagnostics=%#v", result.Nodes, result.Diagnostics)
			}
			if !hasEdge(result.Edges, graph.EdgeCalls, caller.ID, callee.ID, "true") {
				t.Fatalf("missing resolved call: %#v", result.Edges)
			}
			wantImport := "./dep.js"
			if test.language == "rust" {
				wantImport = "crate::dep"
			}
			if !hasNamedImport(result.Nodes, result.Edges, wantImport) {
				t.Fatalf("missing %s import: nodes=%#v edges=%#v diagnostics=%#v", wantImport, result.Nodes, result.Edges, result.Diagnostics)
			}
		})
	}
}

func TestDuplicateNamesRemainUnresolvedInsteadOfGuessing(t *testing.T) {
	result := analyzeSources(t, "python", map[string]string{
		"a.py":   "def shared():\n    pass\n",
		"b.py":   "def shared():\n    pass\n",
		"use.py": "def run():\n    shared()\n",
	})
	run := nodeNamed(result.Nodes, "run")
	if run == nil {
		t.Fatalf("missing run definition: %#v", result.Nodes)
	}
	for _, edge := range result.Edges {
		if edge.Kind == graph.EdgeCalls && edge.From == run.ID {
			if edge.Meta["resolved"] != "false" {
				t.Fatalf("ambiguous shared call was guessed: %#v", edge)
			}
			if target := nodeByID(result.Nodes, edge.To); target == nil || target.Name != "shared" || target.Kind != graph.NodeFunction {
				t.Fatalf("unresolved occurrence node = %#v", target)
			}
			return
		}
	}
	t.Fatalf("missing unresolved shared call: %#v", result.Edges)
}

func TestRelativeImportResolvesToAuditedFile(t *testing.T) {
	result := analyzeSources(t, "javascript", map[string]string{
		"src/app.js": "import { helper } from './dep.js';\nexport function run() { return helper(); }\n",
		"src/dep.js": "export function helper() { return 1; }\n",
	})
	if !hasEdge(result.Edges, graph.EdgeImports, graph.FileID("src/app.js"), graph.FileID("src/dep.js"), "true") {
		t.Fatalf("relative import did not resolve to audited file: nodes=%#v edges=%#v diagnostics=%#v", result.Nodes, result.Edges, result.Diagnostics)
	}
	for _, edge := range result.Edges {
		if edge.Kind == graph.EdgeImports && edge.From == graph.FileID("src/app.js") && edge.To == graph.FileID("src/dep.js") {
			if edge.Meta["confidence"] != "inferred" || edge.Meta["rationale"] == "" {
				t.Fatalf("local import resolution lacks inferred provenance: %#v", edge)
			}
		}
	}
}

func TestSupportsRespectsSpecializedAnalyzerBoundaries(t *testing.T) {
	for _, language := range []string{"go", "markdown", "sql"} {
		if Supports(language, []scan.File{{Path: "sample." + language}}) {
			t.Fatalf("%s should remain owned by its specialized analyzer", language)
		}
	}
	if !Supports("python", []scan.File{{Path: "sample.py"}}) {
		t.Fatal("expected Python Tree-sitter support")
	}
}

func TestPackagedGrammarSetSupportsAdvertisedLanguages(t *testing.T) {
	tests := map[string]string{
		"javascript": "app.js", "typescript": "app.ts", "tsx": "app.tsx", "swift": "App.swift",
		"python": "app.py", "java": "App.java", "kotlin": "App.kt", "scala": "App.scala",
		"rust": "main.rs", "ruby": "app.rb", "php": "app.php", "c": "main.c", "cpp": "main.cpp",
		"csharp": "App.cs", "fsharp": "App.fs", "dart": "app.dart", "elixir": "app.ex",
		"erlang": "app.erl", "clojure": "app.clj", "lua": "app.lua", "r": "app.r",
		"objective-c": "App.m", "perl": "app.pl", "groovy": "App.groovy", "solidity": "App.sol",
		"shell": "app.sh", "powershell": "app.ps1", "terraform": "main.tf", "protobuf": "api.proto",
		"graphql": "schema.graphql",
	}
	for language, path := range tests {
		if !Supports(language, []scan.File{{Path: path, Language: language}}) {
			t.Errorf("packaged grammar set does not support %s (%s)", language, path)
		}
	}
	if entry := entryForFile("objective-c", "App.m"); entry == nil || entry.Name != "objc" {
		t.Fatalf("Objective-C .m resolved to %#v, want objc", entry)
	}
	if entry := entryForFile("typescript", "App.tsx"); entry == nil || entry.Name != "tsx" {
		t.Fatalf("TSX resolved to %#v, want tsx", entry)
	}
}

func TestSymbolIDsSurviveUnrelatedLineMovement(t *testing.T) {
	before := analyzeSources(t, "python", map[string]string{"app.py": "def stable():\n    pass\n"})
	after := analyzeSources(t, "python", map[string]string{"app.py": "# unrelated heading\n\n\ndef stable():\n    pass\n"})
	beforeNode := nodeNamed(before.Nodes, "stable")
	afterNode := nodeNamed(after.Nodes, "stable")
	if beforeNode == nil || afterNode == nil {
		t.Fatalf("stable definition missing: before=%#v after=%#v", before.Nodes, after.Nodes)
	}
	if beforeNode.ID != afterNode.ID {
		t.Fatalf("line movement changed stable symbol ID: %q != %q", beforeNode.ID, afterNode.ID)
	}
	if beforeNode.StartLine == afterNode.StartLine {
		t.Fatalf("test did not move source line: before=%d after=%d", beforeNode.StartLine, afterNode.StartLine)
	}
}

func analyzeSources(t *testing.T, language string, sources map[string]string) *structResult {
	t.Helper()
	root := t.TempDir()
	files := make([]scan.File, 0, len(sources))
	for path, source := range sources {
		absolute := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, scan.File{Path: path, AbsPath: absolute, Language: language})
	}
	result, err := New(language).Analyze(context.Background(), root, files)
	if err != nil {
		t.Fatal(err)
	}
	return &structResult{Nodes: result.Nodes, Edges: result.Edges, Diagnostics: result.Diagnostics}
}

type structResult struct {
	Nodes       []graph.Node
	Edges       []graph.Edge
	Diagnostics []graph.Diagnostic
}

func nodeNamed(nodes []graph.Node, name string) *graph.Node {
	for i := range nodes {
		if nodes[i].Name == name && graph.SymbolKind(nodes[i].Kind) && nodes[i].Meta["resolved"] != "false" {
			return &nodes[i]
		}
	}
	return nil
}

func nodeByID(nodes []graph.Node, id string) *graph.Node {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
	}
	return nil
}

func hasEdge(edges []graph.Edge, kind graph.EdgeKind, from, to, resolved string) bool {
	for _, edge := range edges {
		if edge.Kind == kind && edge.From == from && edge.To == to && edge.Meta["resolved"] == resolved {
			if resolved == "true" && (edge.Meta["confidence"] != "inferred" || edge.Meta["rationale"] == "") {
				return false
			}
			return true
		}
	}
	return false
}

func hasNamedImport(nodes []graph.Node, edges []graph.Edge, name string) bool {
	for _, node := range nodes {
		if node.Kind != graph.NodeImport || node.Name != name {
			continue
		}
		for _, edge := range edges {
			if edge.Kind == graph.EdgeImports && edge.To == node.ID {
				return true
			}
		}
	}
	return false
}
