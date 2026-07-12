package build

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/config"
	"github.com/12vault/ravel/internal/graph"
)

func TestRunBuildsGoGraph(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "simple-go-service")
	result, err := Run(context.Background(), root, config.Default())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	wantNodes := []string{
		"file://cmd/api/main.go",
		"go://cmd/api.main",
		"go://internal/auth.(*SessionManager).CreateSession",
		"go://internal/auth.saveSession",
		"go://internal/db.SessionStore",
	}
	for _, id := range wantNodes {
		if !hasNode(result.Graph, id) {
			t.Fatalf("missing node %s", id)
		}
	}
	if !hasEdge(result.Graph, graph.EdgeCalls, "go://internal/auth.(*SessionManager).CreateSession", "go://internal/auth.saveSession") {
		t.Fatalf("missing call edge from CreateSession to saveSession")
	}
}

func TestRunCanDisableGoAnalysis(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "simple-go-service")
	cfg := config.Default()
	cfg.Analysis.Go = false
	result, err := Run(context.Background(), root, cfg)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, node := range result.Graph.Nodes {
		if node.Kind == graph.NodePackage || graph.SymbolKind(node.Kind) {
			t.Fatalf("found semantic node %s with Go analysis disabled", node.ID)
		}
	}
}

func TestRunBuildsAndCanDisablePolyglotGraph(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "service.py")
	if err := os.WriteFile(path, []byte("def helper():\n    pass\n\ndef run():\n    helper()\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Run(context.Background(), root, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	var helper, run string
	for _, node := range result.Graph.Nodes {
		switch node.Name {
		case "helper":
			helper = node.ID
		case "run":
			run = node.ID
		}
	}
	if helper == "" || run == "" || !hasEdge(result.Graph, graph.EdgeCalls, run, helper) {
		t.Fatalf("polyglot graph missing definitions/call: nodes=%#v edges=%#v", result.Graph.Nodes, result.Graph.Edges)
	}

	cfg := config.Default()
	cfg.Analysis.Polyglot = false
	result, err = Run(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range result.Graph.Nodes {
		if graph.SymbolKind(node.Kind) {
			t.Fatalf("found semantic node %s with polyglot analysis disabled", node.ID)
		}
	}
}

func TestRunReportsPerFilePolyglotProgress(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"App.swift", "Feature.swift"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("func run() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Analysis.Go = false
	cfg.Analysis.Documents = false
	cfg.Analysis.Schemas = false
	var events []Progress
	_, err := RunWithProgress(context.Background(), root, cfg, func(event Progress) {
		if event.Stage == "Analyzing swift" {
			events = append(events, event)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 3 {
		t.Fatalf("Swift progress events = %#v, want start, per-file, and completion events", events)
	}
	want := []struct {
		path      string
		completed int
	}{
		{path: "App.swift", completed: 0},
		{path: "Feature.swift", completed: 1},
		{path: "Feature.swift", completed: 2},
	}
	for _, expected := range want {
		found := false
		for _, event := range events {
			if event.Path == expected.path && event.Completed == expected.completed && event.Total == 2 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing progress path=%q completed=%d in %#v", expected.path, expected.completed, events)
		}
	}
}

func TestRunWithCacheReusesAndInvalidatesMarkdownFilesIndependently(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"one.md": "# One\n",
		"two.md": "# Two\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Analysis.Go = false
	cfg.Analysis.Polyglot = false
	cfg.Analysis.Schemas = false
	cache := CacheOptions{OutputDir: cfg.Output.Dir, Version: "test-v1"}

	first, firstStages := runCachedBuild(t, root, cfg, cache)
	if got := countStage(firstStages, "Analyzing markdown"); got != 2 {
		t.Fatalf("cold analysis stages = %v, want two markdown analyses", firstStages)
	}

	second, secondStages := runCachedBuild(t, root, cfg, cache)
	if got := countStage(secondStages, "Cached markdown"); got != 2 {
		t.Fatalf("warm analysis stages = %v, want two markdown cache hits", secondStages)
	}
	if !reflect.DeepEqual(first.Graph.Nodes, second.Graph.Nodes) || !reflect.DeepEqual(first.Graph.Edges, second.Graph.Edges) || !reflect.DeepEqual(first.Graph.Diagnostics, second.Graph.Diagnostics) {
		t.Fatal("warm cached graph differs from cold graph")
	}

	if err := os.WriteFile(filepath.Join(root, "one.md"), []byte("# One changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, changedStages := runCachedBuild(t, root, cfg, cache)
	if got := countStage(changedStages, "Analyzing markdown"); got != 1 {
		t.Fatalf("changed analysis stages = %v, want one cache miss", changedStages)
	}
	if got := countStage(changedStages, "Cached markdown"); got != 1 {
		t.Fatalf("changed analysis stages = %v, want one cache hit", changedStages)
	}
	if err := os.Remove(filepath.Join(root, "two.md")); err != nil {
		t.Fatal(err)
	}
	_, _ = runCachedBuild(t, root, cfg, cache)
	entries, err := filepath.Glob(filepath.Join(root, cfg.Output.Dir, ".state", "cache", "analysis-v1", "*.json"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("pruned cache entries = %v, err = %v", entries, err)
	}
}

func TestRunWithCacheRepairsCorruptEntriesAndInvalidatesVersions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Cached\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Analysis.Go = false
	cfg.Analysis.Polyglot = false
	cfg.Analysis.Schemas = false
	cache := CacheOptions{OutputDir: cfg.Output.Dir, Version: "test-v1"}
	_, _ = runCachedBuild(t, root, cfg, cache)

	entries, err := filepath.Glob(filepath.Join(root, cfg.Output.Dir, ".state", "cache", "analysis-v1", "*.json"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache entries = %v, err = %v", entries, err)
	}
	if err := os.WriteFile(entries[0], []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, repairedStages := runCachedBuild(t, root, cfg, cache)
	if countStage(repairedStages, "Analyzing markdown") != 1 {
		t.Fatalf("corrupt cache stages = %v, want analysis fallback", repairedStages)
	}
	if data, err := os.ReadFile(entries[0]); err != nil || !strings.Contains(string(data), `"schema":1`) {
		t.Fatalf("cache was not repaired: %q, %v", data, err)
	}

	_, versionStages := runCachedBuild(t, root, cfg, CacheOptions{OutputDir: cfg.Output.Dir, Version: "test-v2"})
	if countStage(versionStages, "Analyzing markdown") != 1 {
		t.Fatalf("new-version stages = %v, want cache invalidation", versionStages)
	}
}

func TestRunWithCacheInvalidatesGoAnalyzerSettings(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc helper() {}\nfunc main() { helper() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Analysis.Documents = false
	cfg.Analysis.Polyglot = false
	cfg.Analysis.Schemas = false
	cache := CacheOptions{OutputDir: cfg.Output.Dir, Version: "test-v1"}
	_, coldStages := runCachedBuild(t, root, cfg, cache)
	if countStage(coldStages, "Analyzing go") != 1 {
		t.Fatalf("cold Go stages = %v, want analysis", coldStages)
	}
	_, warmStages := runCachedBuild(t, root, cfg, cache)
	if countStage(warmStages, "Cached go") != 1 {
		t.Fatalf("warm Go stages = %v, want cache hit", warmStages)
	}
	cfg.Analysis.CallGraph = false
	_, changedStages := runCachedBuild(t, root, cfg, cache)
	if countStage(changedStages, "Analyzing go") != 1 {
		t.Fatalf("changed Go settings stages = %v, want invalidation", changedStages)
	}
}

func runCachedBuild(t *testing.T, root string, cfg config.Config, cache CacheOptions) (Result, []string) {
	t.Helper()
	var stages []string
	result, err := RunWithCache(context.Background(), root, cfg, func(progress Progress) {
		if progress.Stage != "Scanning" {
			stages = append(stages, progress.Stage)
		}
	}, cache)
	if err != nil {
		t.Fatalf("RunWithCache() error = %v", err)
	}
	return result, stages
}

func countStage(stages []string, want string) int {
	count := 0
	for _, stage := range stages {
		if stage == want {
			count++
		}
	}
	return count
}

func hasNode(g graph.Graph, id string) bool {
	for _, n := range g.Nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

func hasEdge(g graph.Graph, kind graph.EdgeKind, from, to string) bool {
	for _, e := range g.Edges {
		if e.Kind == kind && e.From == from && e.To == to {
			return true
		}
	}
	return false
}
