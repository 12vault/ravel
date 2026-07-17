package treeanalyzer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/lang"
	"github.com/12vault/ravel/internal/scan"
	"github.com/odvcencio/gotreesitter/grammars"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == InternalWorkerCommand {
		if err := RunProcessWorker(context.Background(), os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if err := os.Setenv("RAVEL_TREE_WORKER_TEST_BINARY", "1"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

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

func BenchmarkAnalyzeTypeScriptFiles(b *testing.B) {
	root := b.TempDir()
	files := make([]scan.File, 64)
	var source strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&source, "export function function_%d(): number { return %d; }\n", i, i)
	}
	for i := range files {
		name := fmt.Sprintf("service_%02d.ts", i)
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(source.String()), 0o644); err != nil {
			b.Fatal(err)
		}
		files[i] = scan.File{Path: name, AbsPath: path, Language: "typescript"}
	}
	analyzer := New("typescript")
	cases := []struct {
		name    string
		workers int
	}{
		{name: "workers-1", workers: 1},
		{name: "workers-2", workers: 2},
		{name: "workers-4", workers: 4},
		{name: "workers-8", workers: 8},
		{name: "default", workers: runtime.GOMAXPROCS(0)},
	}
	for _, test := range cases {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := analyzer.analyzeWithWorkers(context.Background(), files, nil, test.workers); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestParallelAnalysisMatchesSerialAndPreservesProgressOrder(t *testing.T) {
	root := t.TempDir()
	files := make([]scan.File, 16)
	for i := range files {
		name := fmt.Sprintf("service_%02d.ts", i)
		path := filepath.Join(root, name)
		source := fmt.Sprintf("export function function_%02d(): number { return %d; }\n", i, i)
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		files[i] = scan.File{Path: name, AbsPath: path, Language: "typescript"}
	}

	analyzer := New("typescript")
	serial, err := analyzer.analyzeWithWorkers(context.Background(), files, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	type progressEvent struct {
		path      string
		completed int
	}
	var events []progressEvent
	parallel, err := analyzer.analyzeWithWorkers(context.Background(), files, func(path string, completed int) {
		events = append(events, progressEvent{path: path, completed: completed})
	}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parallel, serial) {
		t.Fatalf("parallel analysis differs from serial analysis\nparallel=%#v\nserial=%#v", parallel, serial)
	}
	if len(events) != len(files)+1 {
		t.Fatalf("progress events = %#v, want %d ordered events", events, len(files)+1)
	}
	for i, file := range files {
		if events[i].path != file.Path || events[i].completed != i {
			t.Fatalf("progress event %d = %#v, want path=%q completed=%d", i, events[i], file.Path, i)
		}
	}
	last := events[len(events)-1]
	if last.path != files[len(files)-1].Path || last.completed != len(files) {
		t.Fatalf("final progress event = %#v, want path=%q completed=%d", last, files[len(files)-1].Path, len(files))
	}
}

func TestParallelAnalysisHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	files := make([]scan.File, 8)
	for i := range files {
		name := fmt.Sprintf("service_%02d.ts", i)
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("export function run(): number { return 1; }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		files[i] = scan.File{Path: name, AbsPath: path, Language: "typescript"}
	}

	ctx, cancel := context.WithCancel(context.Background())
	_, err := New("typescript").analyzeWithWorkers(ctx, files, func(_ string, completed int) {
		if completed == 0 {
			cancel()
		}
	}, 4)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("parallel analysis error = %v, want context.Canceled", err)
	}
}

func TestAnalysisWorkerCountCapsAtProcsAndFiles(t *testing.T) {
	want := runtime.GOMAXPROCS(0)
	if want > 3 {
		want = 3
	}
	if got := analysisWorkerCount(8, 3); got != want {
		t.Fatalf("workers = %d, want %d", got, want)
	}
	if got := analysisWorkerCount(0, 3); got != 0 {
		t.Fatalf("empty input workers = %d, want 0", got)
	}
}

func TestSmallTreeSitterInputsStaySerial(t *testing.T) {
	if minProcessWorkerFiles != 20 {
		t.Fatalf("process worker threshold = %d, want Graphify-compatible threshold 20", minProcessWorkerFiles)
	}
	if got := isolatedWorkerCount(minProcessWorkerFiles-1, defaultMaxWorkers); got != 0 {
		t.Fatalf("small Tree-sitter process worker count = %d, want 0", got)
	}
	want := analysisWorkerCount(minProcessWorkerFiles, defaultMaxWorkers)
	if got := isolatedWorkerCount(minProcessWorkerFiles, defaultMaxWorkers); got != want {
		t.Fatalf("large Tree-sitter process worker count = %d, want %d", got, want)
	}
}

func TestFileCacheReparsesOnlyChangedFilesAndPreservesCrossFileResolution(t *testing.T) {
	root := t.TempDir()
	files := []scan.File{
		{Path: "src/app.ts", AbsPath: filepath.Join(root, "src", "app.ts"), Language: "typescript", Hash: "app-v1"},
		{Path: "src/helper.ts", AbsPath: filepath.Join(root, "src", "helper.ts"), Language: "typescript", Hash: "helper-v1"},
	}
	for index, source := range []string{
		"export function run(): number { return helper(); }\n",
		"export function helper(): number { return 1; }\n",
	} {
		if err := os.MkdirAll(filepath.Dir(files[index].AbsPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(files[index].AbsPath, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		files[index].Size = int64(len(source))
	}

	previousParse := parseSourceFile
	parseCalls := 0
	parseSourceFile = func(ctx context.Context, file scan.File, entry grammars.LangEntry, timeoutMicros uint64) (parsedFile, []graph.Diagnostic, error) {
		parseCalls++
		return previousParse(ctx, file, entry, timeoutMicros)
	}
	t.Cleanup(func() { parseSourceFile = previousParse })

	cache := &memoryFileAnalysisCache{entries: map[string][]byte{}}
	analyzer := NewWithJobs("typescript", 1)
	first, err := analyzer.AnalyzeWithFileCache(context.Background(), root, files, nil, cache)
	if err != nil {
		t.Fatal(err)
	}
	if parseCalls != 2 {
		t.Fatalf("cold parse calls = %d, want 2", parseCalls)
	}
	second, err := analyzer.AnalyzeWithFileCache(context.Background(), root, files, nil, cache)
	if err != nil {
		t.Fatal(err)
	}
	if parseCalls != 2 || !reflect.DeepEqual(first, second) {
		t.Fatalf("warm cache reparsed files or changed result: calls=%d", parseCalls)
	}

	changed := "export function helper(): number { return 2; }\n"
	if err := os.WriteFile(files[1].AbsPath, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	files[1].Hash = "helper-v2"
	files[1].Size = int64(len(changed))
	third, err := analyzer.AnalyzeWithFileCache(context.Background(), root, files, nil, cache)
	if err != nil {
		t.Fatal(err)
	}
	if parseCalls != 3 {
		t.Fatalf("changed parse calls = %d, want 3", parseCalls)
	}
	run := nodeNamedAtPath(third.Nodes, "run", "src/app.ts")
	helper := nodeNamedAtPath(third.Nodes, "helper", "src/helper.ts")
	if run == nil || helper == nil || !hasEdge(third.Edges, graph.EdgeCalls, run.ID, helper.ID, "true") {
		t.Fatalf("cached and changed files lost cross-file call resolution: nodes=%#v edges=%#v", third.Nodes, third.Edges)
	}
}

type memoryFileAnalysisCache struct {
	entries map[string][]byte
}

func (cache *memoryFileAnalysisCache) Load(file scan.File, destination any) bool {
	data, ok := cache.entries[file.Path+"\x00"+file.Hash]
	return ok && json.Unmarshal(data, destination) == nil
}

func (cache *memoryFileAnalysisCache) Store(file scan.File, value any) {
	data, err := json.Marshal(value)
	if err == nil {
		cache.entries[file.Path+"\x00"+file.Hash] = data
	}
}

func TestProcessWorkerTimeoutDoesNotScaleWithWorkerCount(t *testing.T) {
	for _, workers := range []int{1, 2, 8, 64} {
		if got := processWorkerTimeoutMicros(workers); got != parseTimeoutMicros {
			t.Fatalf("process timeout with %d workers = %d, want %d", workers, got, parseTimeoutMicros)
		}
	}
}

func TestPartialDefinitionsSurviveWorkerRoundTripAndAreMarked(t *testing.T) {
	original := parsedFile{
		file: scan.File{Path: "App.swift"}, language: "swift",
		definitions: []definition{{
			id: "swift-run", name: "run", kind: graph.NodeFunction,
			path: "App.swift", language: "swift", startLine: 7, endLine: 9, partial: true,
		}},
	}
	roundTrip := processParsedToParsedFile(parsedFileToProcessParsed(original))
	if len(roundTrip.definitions) != 1 || !roundTrip.definitions[0].partial {
		t.Fatalf("partial definition lost in worker round trip: %#v", roundTrip.definitions)
	}
	result := &lang.AnalysisResult{}
	emitDefinitions([]parsedFile{roundTrip}, result)
	if len(result.Nodes) != 1 || result.Nodes[0].Meta["partial"] != "true" || result.Nodes[0].Meta["parse_complete"] != "false" {
		t.Fatalf("partial definition lacks incomplete-parse metadata: %#v", result.Nodes)
	}
}

func TestProcessAnalysisMatchesSerialAndPreservesProgressOrder(t *testing.T) {
	root := t.TempDir()
	files := make([]scan.File, 16)
	for i := range files {
		name := fmt.Sprintf("service_%02d.js", i)
		path := filepath.Join(root, name)
		source := fmt.Sprintf("export function helper_%02d() { return %d; }\nexport function run_%02d() { return helper_%02d(); }\n", i, i, i, i)
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		files[i] = scan.File{Path: name, AbsPath: path, Language: "javascript", Size: int64(len(source))}
	}

	analyzer := New("javascript")
	serial, err := analyzer.analyzeWithWorkers(context.Background(), files, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	type progressEvent struct {
		path      string
		completed int
	}
	var events []progressEvent
	parallel, err := analyzer.analyzeWithProcessWorkers(context.Background(), files, func(path string, completed int) {
		events = append(events, progressEvent{path: path, completed: completed})
	}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parallel, serial) {
		t.Fatalf("process analysis differs from serial analysis\nparallel=%#v\nserial=%#v", parallel, serial)
	}
	if len(events) != len(files)+1 {
		t.Fatalf("progress events = %#v, want %d ordered events", events, len(files)+1)
	}
	for i, file := range files {
		if events[i].path != file.Path || events[i].completed != i {
			t.Fatalf("progress event %d = %#v, want path=%q completed=%d", i, events[i], file.Path, i)
		}
	}
	last := events[len(events)-1]
	if last.path != files[len(files)-1].Path || last.completed != len(files) {
		t.Fatalf("final progress event = %#v, want path=%q completed=%d", last, files[len(files)-1].Path, len(files))
	}
}

func TestProcessAnalysisHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	files := make([]scan.File, 12)
	for i := range files {
		name := fmt.Sprintf("service_%02d.py", i)
		path := filepath.Join(root, name)
		source := []byte(fmt.Sprintf("def function_%02d():\n    return %d\n", i, i))
		if err := os.WriteFile(path, source, 0o644); err != nil {
			t.Fatal(err)
		}
		files[i] = scan.File{Path: name, AbsPath: path, Language: "python", Size: int64(len(source))}
	}

	ctx, cancel := context.WithCancel(context.Background())
	_, err := New("python").analyzeWithProcessWorkers(ctx, files, func(_ string, completed int) {
		if completed == 0 {
			cancel()
		}
	}, 4)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("process analysis error = %v, want context.Canceled", err)
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

func TestDedupeReferencesDeterministicallyOrdersSameStart(t *testing.T) {
	outer := reference{name: "Service", kind: graph.EdgeCalls, path: "Config.ts", language: "typescript", startByte: 100, endByte: 200, startLine: 3, column: 3}
	inner := reference{name: "Service", kind: graph.EdgeCalls, path: "Config.ts", language: "typescript", startByte: 100, endByte: 150, startLine: 1, column: 20}
	for i := 0; i < 100; i++ {
		input := []reference{inner, outer}
		if i%2 == 0 {
			input = []reference{outer, inner}
		}
		if got := dedupeReferences(input); !reflect.DeepEqual(got, []reference{outer, inner}) {
			t.Fatalf("deduped references = %#v, want outer-before-inner deterministic order", got)
		}
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

func TestImportedScopeResolvesDuplicateCrossFileSymbols(t *testing.T) {
	result := analyzeSources(t, "typescript", map[string]string{
		"src/app.ts":    "import { Base, helper } from './chosen';\nexport class Child extends Base {}\nexport function run() { return helper(); }\n",
		"src/chosen.ts": "export class Base {}\nexport function helper() { return 1; }\n",
		"src/other.ts":  "export class Base {}\nexport function helper() { return 2; }\n",
	})
	run := nodeNamedAtPath(result.Nodes, "run", "src/app.ts")
	child := nodeNamedAtPath(result.Nodes, "Child", "src/app.ts")
	helper := nodeNamedAtPath(result.Nodes, "helper", "src/chosen.ts")
	base := nodeNamedAtPath(result.Nodes, "Base", "src/chosen.ts")
	if run == nil || child == nil || helper == nil || base == nil {
		t.Fatalf("missing scoped definitions: nodes=%#v diagnostics=%#v", result.Nodes, result.Diagnostics)
	}
	if !hasEdge(result.Edges, graph.EdgeCalls, run.ID, helper.ID, "true") {
		t.Fatalf("direct import did not disambiguate helper call: %#v", result.Edges)
	}
	if !hasEdge(result.Edges, graph.EdgeInherits, child.ID, base.ID, "true") {
		t.Fatalf("direct import did not disambiguate Base heritage: %#v", result.Edges)
	}
	for _, edge := range result.Edges {
		if (edge.Kind == graph.EdgeCalls && edge.From == run.ID) || (edge.Kind == graph.EdgeInherits && edge.From == child.ID) {
			if !strings.Contains(edge.Meta["rationale"], "directly imported file") {
				t.Fatalf("scoped resolution lacks import rationale: %#v", edge)
			}
		}
	}
}

func TestTypeScriptExtractsModuleBindingsAndComplexHeritageClasses(t *testing.T) {
	source := `
export const makeService = () => ({ ready: true });
export const serviceName: string = "api";
let retryCount = 0;
export class Service extends Context.Service<Service, Shape>()("service") {}
function run() {
  const localOnly = 1;
  return localOnly;
}
`
	result := analyzeSources(t, "typescript", map[string]string{
		"src/services.ts": source,
	})
	wants := map[string]graph.NodeKind{
		"makeService": graph.NodeFunction,
		"serviceName": graph.NodeVariable,
		"retryCount":  graph.NodeVariable,
		"Service":     graph.NodeClass,
	}
	for name, kind := range wants {
		node := nodeNamedAtPath(result.Nodes, name, "src/services.ts")
		if node == nil || node.Kind != kind {
			t.Fatalf("%s declaration = %#v, want kind %s; nodes=%#v diagnostics=%#v", name, node, kind, result.Nodes, result.Diagnostics)
		}
	}
	service := nodeNamedAtPath(result.Nodes, "Service", "src/services.ts")
	if service.Meta["partial"] != "true" || service.Meta["parse_complete"] != "false" {
		t.Fatalf("recovered class lacks partial provenance: %#v", service)
	}
	if nodeNamedAtPath(result.Nodes, "localOnly", "src/services.ts") != nil {
		t.Fatalf("function-local binding leaked into declaration graph: %#v", result.Nodes)
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

func TestAdvertisedLanguagesExtractNamedDeclarations(t *testing.T) {
	tests := []struct {
		language string
		path     string
		source   string
		want     string
	}{
		{language: "javascript", path: "app.js", source: "function run() {}\n", want: "run"},
		{language: "typescript", path: "app.ts", source: "function run(): void {}\n", want: "run"},
		{language: "typescript", path: "app.tsx", source: "function Run() { return <div />; }\n", want: "Run"},
		{language: "swift", path: "App.swift", source: "func run() {}\n", want: "run"},
		{language: "python", path: "app.py", source: "def run():\n    pass\n", want: "run"},
		{language: "java", path: "App.java", source: "class App { void run() {} }\n", want: "run"},
		{language: "kotlin", path: "App.kt", source: "fun run() {}\n", want: "run"},
		{language: "scala", path: "App.scala", source: "def run(): Int = 1\n", want: "run"},
		{language: "rust", path: "main.rs", source: "fn run() {}\n", want: "run"},
		{language: "ruby", path: "app.rb", source: "def run\nend\n", want: "run"},
		{language: "php", path: "app.php", source: "<?php function run() {}\n", want: "run"},
		{language: "c", path: "main.c", source: "void run(void) {}\n", want: "run"},
		{language: "cpp", path: "main.cpp", source: "void run() {}\n", want: "run"},
		{language: "csharp", path: "App.cs", source: "class App { void Run() {} }\n", want: "Run"},
		{language: "fsharp", path: "App.fs", source: "let run () = ()\n", want: "run"},
		{language: "dart", path: "app.dart", source: "void run() {}\n", want: "run"},
		{language: "elixir", path: "app.ex", source: "defmodule App do\n  def run, do: :ok\nend\n", want: "run"},
		{language: "erlang", path: "app.erl", source: "-module(app).\nrun() -> ok.\n", want: "run"},
		{language: "clojure", path: "app.clj", source: "(defn run [] nil)\n", want: "run"},
		{language: "lua", path: "app.lua", source: "function run() end\n", want: "run"},
		{language: "r", path: "app.r", source: "run <- function() {}\n", want: "run"},
		{language: "objective-c", path: "App.m", source: "void run(void) {}\n", want: "run"},
		{language: "perl", path: "app.pl", source: "sub run {}\n", want: "run"},
		{language: "groovy", path: "App.groovy", source: "def run() {}\n", want: "run"},
		{language: "solidity", path: "App.sol", source: "contract App { function run() public {} }\n", want: "run"},
		{language: "shell", path: "app.sh", source: "run() { :; }\n", want: "run"},
		{language: "powershell", path: "app.ps1", source: "function Run {}\n", want: "Run"},
		{language: "terraform", path: "main.tf", source: "resource \"thing\" \"run\" {}\n", want: "run"},
		{language: "protobuf", path: "api.proto", source: "syntax = \"proto3\"; message Run {}\n", want: "Run"},
		{language: "graphql", path: "schema.graphql", source: "type Run { value: String }\n", want: "Run"},
	}
	for _, test := range tests {
		t.Run(test.language+"/"+test.path, func(t *testing.T) {
			result := analyzeSources(t, test.language, map[string]string{test.path: test.source})
			if nodeNamed(result.Nodes, test.want) == nil {
				t.Fatalf("missing %q declaration: nodes=%#v diagnostics=%#v", test.want, result.Nodes, result.Diagnostics)
			}
			if got := countNamedSymbols(result.Nodes, test.want); got != 1 {
				t.Fatalf("%q declaration count = %d, want 1: nodes=%#v", test.want, got, result.Nodes)
			}
		})
	}
}

func TestSwiftExtractsFunctionsMethodsAndProtocolRequirements(t *testing.T) {
	result := analyzeSources(t, "swift", map[string]string{
		"App.swift": "func topLevel() {}\n\nstruct App {\n    func run() {}\n}\n\nprotocol Worker {\n    func work()\n}\n",
	})
	topLevel := nodeNamed(result.Nodes, "topLevel")
	run := nodeNamed(result.Nodes, "run")
	work := nodeNamed(result.Nodes, "work")
	if topLevel == nil || run == nil || work == nil {
		t.Fatalf("missing Swift declarations: nodes=%#v diagnostics=%#v", result.Nodes, result.Diagnostics)
	}
	if topLevel.Kind != graph.NodeFunction || run.Kind != graph.NodeMethod || work.Kind != graph.NodeMethod {
		t.Fatalf("unexpected Swift kinds: topLevel=%q run=%q work=%q", topLevel.Kind, run.Kind, work.Kind)
	}
	if topLevel.StartLine != 1 || run.StartLine != 4 || work.StartLine != 8 {
		t.Fatalf("unexpected Swift declaration lines: topLevel=%d run=%d work=%d", topLevel.StartLine, run.StartLine, work.StartLine)
	}
}

func TestChunkedSwiftDeclarationsPreserveOriginalLinesAndDeduplicateOverlap(t *testing.T) {
	var source strings.Builder
	wanted := map[string]int{}
	for line := 1; line <= 620; line++ {
		name := ""
		switch line {
		case 1:
			name = "first"
		case 240:
			name = "overlap"
		case 620:
			name = "last"
		}
		if name != "" {
			fmt.Fprintf(&source, "func %s() {}\n", name)
			wanted[name] = line
		} else {
			fmt.Fprintf(&source, "let value%d = %d\n", line, line)
		}
	}
	entry := entryForFile("swift", "App.swift")
	if entry == nil {
		t.Fatal("missing Swift grammar")
	}
	definitions := dedupeDefinitions(extractChunkedDeclarations(
		context.Background(), *entry, "App.swift", []byte(source.String()),
	))
	counts := map[string]int{}
	for _, definition := range definitions {
		if line, ok := wanted[definition.name]; ok {
			counts[definition.name]++
			if definition.startLine != line || !definition.partial {
				t.Fatalf("chunked %s = line %d partial=%v, want line %d partial", definition.name, definition.startLine, definition.partial, line)
			}
		}
	}
	for name := range wanted {
		if counts[name] != 1 {
			t.Fatalf("chunked %s count = %d, want 1; definitions=%#v", name, counts[name], definitions)
		}
	}
}

func TestDedupeDefinitionsPrefersCompleteFactOverPartialOverlap(t *testing.T) {
	partial := definition{name: "run", kind: graph.NodeFunction, startByte: 10, endByte: 90, partial: true}
	complete := definition{name: "run", kind: graph.NodeFunction, startByte: 10, endByte: 80}
	got := dedupeDefinitions([]definition{partial, complete})
	if len(got) != 1 || got[0].partial || got[0].endByte != complete.endByte {
		t.Fatalf("deduped definitions = %#v, want complete fact", got)
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

func nodeNamedAtPath(nodes []graph.Node, name, path string) *graph.Node {
	for i := range nodes {
		if nodes[i].Name == name && nodes[i].Path == path && graph.SymbolKind(nodes[i].Kind) && nodes[i].Meta["resolved"] != "false" {
			return &nodes[i]
		}
	}
	return nil
}

func countNamedSymbols(nodes []graph.Node, name string) int {
	count := 0
	for _, node := range nodes {
		if node.Name == name && graph.SymbolKind(node.Kind) && node.Meta["resolved"] != "false" {
			count++
		}
	}
	return count
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
