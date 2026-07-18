package build

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/12vault/ravel/internal/config"
	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/scan"
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
		if node.Kind == graph.NodeFile || node.Kind == graph.NodePackage || graph.SymbolKind(node.Kind) {
			t.Fatalf("found semantic node %s with Go analysis disabled", node.ID)
		}
	}
}

func TestRunSkipsUnsupportedAndZeroContributionFiles(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"main.go":      "package main\nfunc main() {}\n",
		"empty.py":     "# comments only\n",
		"LICENSE":      "plain text without a supported analyzer\n",
		"payload.json": "{\"name\": \"data, not code\"}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := Run(context.Background(), root, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if !hasNode(result.Graph, graph.FileID("main.go")) {
		t.Fatal("graphifiable Go file was omitted")
	}
	for _, path := range []string{"empty.py", "LICENSE", "payload.json"} {
		if hasNode(result.Graph, graph.FileID(path)) {
			t.Errorf("non-contributing file %q was graphified", path)
		}
	}
	if len(result.Skipped) != 2 {
		t.Fatalf("Skipped = %#v, want 2 analyzer-skipped files; LICENSE is rejected during scan", result.Skipped)
	}
}

func TestRunRetainsIncompleteGoFilesAsPartialTopology(t *testing.T) {
	root := t.TempDir()
	for name, source := range map[string]string{
		"missing-package.go": "func Broken() {\n",
		"partial-package.go": "package sample\nfunc Recovered() {\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := Run(context.Background(), root, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skipped) != 0 {
		t.Fatalf("incomplete Go files were skipped: %#v", result.Skipped)
	}
	for _, path := range []string{"missing-package.go", "partial-package.go"} {
		var file graph.Node
		for _, node := range result.Graph.Nodes {
			if node.ID == graph.FileID(path) {
				file = node
				break
			}
		}
		if file.ID == "" || file.Meta["partial"] != "true" || file.Meta["parse_complete"] != "false" {
			t.Fatalf("partial file %q = %#v", path, file)
		}
	}
	recovered := false
	for _, node := range result.Graph.Nodes {
		if node.Name == "Recovered" && node.Kind == graph.NodeFunction {
			recovered = true
			if node.Meta["partial"] != "true" || node.Meta["parse_complete"] != "false" {
				t.Fatalf("recovered declaration lacks partial provenance: %#v", node)
			}
		}
	}
	if !recovered {
		t.Fatalf("missing recovered declaration: %#v", result.Graph.Nodes)
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

func TestRunWithCacheReportsGraphFinalizationProgress(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	_, stages := runCachedBuild(t, root, cfg, CacheOptions{OutputDir: cfg.Output.Dir, Version: "test-v1"})

	building := stageIndex(stages, "Building graph")
	cleaning := stageIndex(stages, "Cleaning cache")
	if building < 0 || cleaning < 0 || building >= cleaning {
		t.Fatalf("finalization stages = %v, want Building graph before Cleaning cache", stages)
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

func TestRunWithCachePersistsStatIndexAndForceRehashesAndReanalyzes(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "README.md")
	baseline := time.Unix(1_700_000_000, 123_456_789)
	if err := os.WriteFile(sourcePath, []byte("# One\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sourcePath, baseline, baseline); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Analysis.Go = false
	cfg.Analysis.Polyglot = false
	cfg.Analysis.Schemas = false
	cache := CacheOptions{OutputDir: cfg.Output.Dir, Version: "test-v1"}
	first, _ := runCachedBuild(t, root, cfg, cache)
	statIndex := filepath.Join(root, cfg.Output.Dir, ".state", "cache", "stat-index-v1.json")
	if info, err := os.Stat(statIndex); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("stat index info = %#v, err = %v", info, err)
	}

	_, warmStages := runCachedBuild(t, root, cfg, cache)
	if countStage(warmStages, "Cached markdown") != 1 {
		t.Fatalf("warm stages = %v, want markdown cache hit", warmStages)
	}

	// Deliberately preserve both stat keys to exercise the explicit escape hatch
	// for generated files or tools that restore timestamps.
	if err := os.WriteFile(sourcePath, []byte("# Two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sourcePath, baseline, baseline); err != nil {
		t.Fatal(err)
	}
	forced := cache
	forced.Force = true
	second, forcedStages := runCachedBuild(t, root, cfg, forced)
	if countStage(forcedStages, "Analyzing markdown") != 1 || countStage(forcedStages, "Cached markdown") != 0 {
		t.Fatalf("forced stages = %v, want fresh markdown analysis", forcedStages)
	}
	if first.Scan.Files[0].Hash == second.Scan.Files[0].Hash {
		t.Fatalf("forced build reused stale hash %q", second.Scan.Files[0].Hash)
	}
}

func TestWriteCacheAtomicReplacesEntryAndCleansTemporaryFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "entry.json")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := writeCacheAtomic(path, []byte("new\n")); err != nil {
		t.Fatalf("writeCacheAtomic() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "new\n" {
		t.Fatalf("cache entry = %q, want %q", got, "new\n")
	}
	temporary, err := filepath.Glob(filepath.Join(directory, ".analysis-cache-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary cache files remain: %v", temporary)
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

func TestRunWithCacheInvalidatesPolyglotFilesIndependently(t *testing.T) {
	root := t.TempDir()
	sources := map[string]string{
		"src/app.ts":    "export function run(): number { return helper(); }\n",
		"src/helper.ts": "export function helper(): number { return 1; }\n",
	}
	for path, source := range sources {
		absolute := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Analysis.Go = false
	cfg.Analysis.Documents = false
	cfg.Analysis.Schemas = false
	cache := CacheOptions{OutputDir: cfg.Output.Dir, Version: "test-v1"}

	first, coldStages := runCachedBuild(t, root, cfg, cache)
	if countStage(coldStages, "Analyzing typescript") == 0 {
		t.Fatalf("cold TypeScript stages = %v, want analysis", coldStages)
	}
	entries := polyglotCacheEntries(t, root, cfg.Output.Dir)
	if len(entries) != 2 {
		t.Fatalf("per-file TypeScript cache entries = %#v, want 2", entries)
	}
	baseline := time.Unix(946684800, 0)
	for _, path := range entries {
		if err := os.Chtimes(path, baseline, baseline); err != nil {
			t.Fatal(err)
		}
	}

	second, warmStages := runCachedBuild(t, root, cfg, cache)
	if countStage(warmStages, "Cached typescript") != 1 {
		t.Fatalf("warm TypeScript stages = %v, want cache hit", warmStages)
	}
	if !reflect.DeepEqual(first.Graph.Nodes, second.Graph.Nodes) || !reflect.DeepEqual(first.Graph.Edges, second.Graph.Edges) {
		t.Fatal("warm per-file cache changed the graph")
	}
	for source, path := range entries {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(baseline) {
			t.Fatalf("warm cache rewrote %s at %s: modTime=%v", source, path, info.ModTime())
		}
	}

	changed := "export function helper(): number { return 2; }\n"
	if err := os.WriteFile(filepath.Join(root, "src", "helper.ts"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	third, changedStages := runCachedBuild(t, root, cfg, cache)
	if countStage(changedStages, "Analyzing typescript") == 0 {
		t.Fatalf("changed TypeScript stages = %v, want partial analysis", changedStages)
	}
	entries = polyglotCacheEntries(t, root, cfg.Output.Dir)
	for source, path := range entries {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if source == "src/app.ts" && !info.ModTime().Equal(baseline) {
			t.Fatalf("unchanged TypeScript cache was rewritten: %s", path)
		}
		if source == "src/helper.ts" && info.ModTime().Equal(baseline) {
			t.Fatalf("changed TypeScript cache was not rewritten: %s", path)
		}
	}
	var runID, helperID string
	for _, node := range third.Graph.Nodes {
		if node.Path == "src/app.ts" && node.Name == "run" {
			runID = node.ID
		}
		if node.Path == "src/helper.ts" && node.Name == "helper" {
			helperID = node.ID
		}
	}
	if runID == "" || helperID == "" || !hasEdge(third.Graph, graph.EdgeCalls, runID, helperID) {
		t.Fatalf("mixed cached and changed parses lost cross-file call: run=%q helper=%q", runID, helperID)
	}
}

func polyglotCacheEntries(t *testing.T, root, outputDir string) map[string]string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, outputDir, ".state", "cache", "analysis-v1", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]string{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(data, &envelope) != nil || len(envelope.Value) == 0 {
			continue
		}
		var cached struct {
			Parsed struct {
				File scan.File `json:"file"`
			} `json:"parsed"`
		}
		if json.Unmarshal(envelope.Value, &cached) == nil && cached.Parsed.File.Path != "" {
			entries[cached.Parsed.File.Path] = path
		}
	}
	return entries
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

func stageIndex(stages []string, want string) int {
	for i, stage := range stages {
		if stage == want {
			return i
		}
	}
	return -1
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
