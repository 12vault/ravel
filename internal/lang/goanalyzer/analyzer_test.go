package goanalyzer

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/lang"
	"github.com/12vault/ravel/internal/scan"
)

func TestCallGraphSuppressesBuiltinsAndUsesExternalFunctionNodes(t *testing.T) {
	result, _ := analyzeGoSources(t, map[string]string{
		"pkg/main.go": `package sample

import output "example.com/acme/log"

func Run(values []int) {
	_ = len(values)
	values = append(values, 1)
	println(values)
	clear(values)
	_ = max(1, 2)
	_ = string('x')
	_ = []byte("x")
	output.Write(values)
}
`,
	})

	calls := edgesOfKind(result, graph.EdgeCalls)
	if len(calls) != 1 {
		t.Fatalf("call edges = %#v, want only the external output.Write call", calls)
	}
	wantTarget := graph.ExternalFunctionID("example.com/acme/log", "Write")
	if got := calls[0].To; got != wantTarget {
		t.Fatalf("call target = %q, want %q", got, wantTarget)
	}
	if calls[0].To == graph.ImportID("example.com/acme/log") {
		t.Fatal("selector call targets an import node")
	}
	for _, edge := range calls {
		if target, ok := nodeByID(result, edge.To); ok && (target.Kind == graph.NodeImport || target.Kind == graph.NodePackage) {
			t.Fatalf("call targets dependency container node: %#v", target)
		}
	}
	if calls[0].Meta["confidence"] != "inferred" || calls[0].Meta["resolved"] != "true" || calls[0].Meta["evidence"] == "" {
		t.Fatalf("external call metadata = %#v", calls[0].Meta)
	}

	external, ok := nodeByID(result, wantTarget)
	if !ok {
		t.Fatalf("missing external function node %q", wantTarget)
	}
	if external.Kind != graph.NodeFunction || external.Name != "Write" || external.Package != "example.com/acme/log" {
		t.Fatalf("external node = %#v", external)
	}
	if external.Meta["external"] != "true" || external.Meta["confidence"] != "inferred" || external.Meta["resolved"] != "true" {
		t.Fatalf("external node metadata = %#v", external.Meta)
	}

	builtins := map[string]bool{"len": true, "append": true, "println": true, "clear": true, "max": true, "string": true}
	for _, edge := range calls {
		if builtins[edge.Meta["name"]] {
			t.Fatalf("builtin %q leaked into call graph", edge.Meta["name"])
		}
	}
	for _, node := range result.Nodes {
		if builtins[node.Name] || strings.HasPrefix(node.ID, "unresolved-call://") {
			t.Fatalf("builtin or legacy unresolved hub leaked into nodes: %#v", node)
		}
	}
}

func TestCallGraphUsesStableExternalMethodNodesForImportedReceiverTypes(t *testing.T) {
	result, _ := analyzeGoSources(t, map[string]string{
		"pkg/main.go": `package sample

import "testing"

func Run(t *testing.T) { t.Fatal("stop") }
`,
	})

	calls := callsFrom(result, graph.FunctionID("pkg", "Run"))
	want := graph.ExternalFunctionID("testing", "T.Fatal")
	if len(calls) != 1 || calls[0].To != want || calls[0].Meta["external"] != "true" || calls[0].Meta["resolved"] != "true" {
		t.Fatalf("external method calls = %#v, want target %q", calls, want)
	}
	node, ok := nodeByID(result, want)
	if !ok || node.Name != "T.Fatal" || node.Package != "testing" {
		t.Fatalf("external method node = %#v, found = %v", node, ok)
	}
}

func TestCallGraphResolvesImportedFunctionsInLocalModules(t *testing.T) {
	result, _ := analyzeGoSources(t, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.24\n",
		"internal/caller/caller.go": `package caller

import "example.com/app/internal/worker"

func Run() { worker.Work() }
`,
		"internal/worker/worker.go": `package worker

func Work() {}
`,
	})

	calls := callsFrom(result, graph.FunctionID("internal/caller", "Run"))
	want := graph.FunctionID("internal/worker", "Work")
	if len(calls) != 1 || calls[0].To != want || calls[0].Meta["resolved"] != "true" {
		t.Fatalf("local imported function calls = %#v, want target %q", calls, want)
	}
	if calls[0].Meta["external"] == "true" {
		t.Fatalf("local imported function marked external: %#v", calls[0])
	}
	if _, ok := nodeByID(result, graph.ExternalFunctionID("example.com/app/internal/worker", "Work")); ok {
		t.Fatal("local imported function also emitted as a duplicate external node")
	}
}

func TestCallGraphResolvesMethodsOnLocalConstructorResults(t *testing.T) {
	result, _ := analyzeGoSources(t, map[string]string{
		"pkg/index.go": `package sample

type Index struct{}

func NewIndex() *Index { return &Index{} }
func (*Index) Search() {}

func Direct() { NewIndex().Search() }
func Assigned() {
	index := NewIndex()
	index.Search()
}
`,
	})

	wantMethod := graph.MethodID("pkg", "(*Index)", "Search")
	for _, caller := range []string{"Direct", "Assigned"} {
		calls := callsFrom(result, graph.FunctionID("pkg", caller))
		found := false
		for _, call := range calls {
			if call.To == wantMethod && call.Meta["resolved"] == "true" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s calls = %#v, want resolved constructor-result method %q", caller, calls, wantMethod)
		}
	}
}

func TestCallGraphResolvesMethodsOnImportedLocalTypes(t *testing.T) {
	result, _ := analyzeGoSources(t, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.24\n",
		"caller/caller.go": `package caller

import "example.com/app/worker"

func Direct() { worker.NewClient().Work() }
func Assigned() {
	client := worker.NewClient()
	client.Work()
}
func Typed(client worker.Client) { client.Work() }
`,
		"worker/worker.go": `package worker

type Client struct{}

func NewClient() *Client { return &Client{} }
func (*Client) Work() {}
`,
	})

	wantMethod := graph.MethodID("worker", "(*Client)", "Work")
	for _, caller := range []string{"Direct", "Assigned", "Typed"} {
		calls := callsFrom(result, graph.FunctionID("caller", caller))
		found := false
		for _, call := range calls {
			if call.To == wantMethod && call.Meta["resolved"] == "true" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s calls = %#v, want resolved imported method %q", caller, calls, wantMethod)
		}
	}
}

func TestShadowedPredeclaredNameRemainsExplicitlyUnresolved(t *testing.T) {
	result, _ := analyzeGoSources(t, map[string]string{
		"pkg/shadow.go": `package sample

var len = func([]int) int { return 0 }

func Run(values []int) { _ = len(values) }
`,
	})

	calls := callsFrom(result, graph.FunctionID("pkg", "Run"))
	if len(calls) != 1 {
		t.Fatalf("shadowed len calls = %#v, want one explicit call", calls)
	}
	if calls[0].Meta["resolved"] != "false" || !strings.HasPrefix(calls[0].To, "unresolved-callsite://") {
		t.Fatalf("shadowed len call = %#v, want unresolved rather than suppressed as a builtin", calls[0])
	}
}

func TestCallGraphResolvesOnlyUniquelyTypedMethodReceivers(t *testing.T) {
	result, _ := analyzeGoSources(t, map[string]string{
		"pkg/methods.go": `package sample

type Alpha struct{}
type Beta struct{}
type Runner interface { Run() }

func (Alpha) Run() {}
func (Beta) Run() {}
func (*Alpha) Stop() {}

func Exact(value Alpha) { value.Run() }
func Ambiguous(value Runner) { value.Run() }
func PointerExpression(value *Alpha) { (*Alpha).Stop(value) }
`,
	})

	exact := callsFrom(result, graph.FunctionID("pkg", "Exact"))
	if len(exact) != 1 || exact[0].To != graph.MethodID("pkg", "Alpha", "Run") || exact[0].Meta["resolved"] != "true" {
		t.Fatalf("exact receiver calls = %#v", exact)
	}

	ambiguous := callsFrom(result, graph.FunctionID("pkg", "Ambiguous"))
	if len(ambiguous) != 1 {
		t.Fatalf("ambiguous receiver calls = %#v", ambiguous)
	}
	if ambiguous[0].To == graph.MethodID("pkg", "Alpha", "Run") || ambiguous[0].To == graph.MethodID("pkg", "Beta", "Run") {
		t.Fatalf("ambiguous receiver guessed a concrete method: %#v", ambiguous[0])
	}
	if !strings.HasPrefix(ambiguous[0].To, "unresolved-callsite://") || ambiguous[0].Meta["resolved"] != "false" {
		t.Fatalf("ambiguous receiver is not explicitly unresolved: %#v", ambiguous[0])
	}

	pointerExpression := callsFrom(result, graph.FunctionID("pkg", "PointerExpression"))
	if len(pointerExpression) != 1 || pointerExpression[0].To != graph.MethodID("pkg", "(*Alpha)", "Stop") {
		t.Fatalf("pointer method expression calls = %#v", pointerExpression)
	}
}

func TestUnresolvedCallsAreScopedToEachCallsite(t *testing.T) {
	result, _ := analyzeGoSources(t, map[string]string{
		"pkg/calls.go": `package sample

func First(value any) { value.Missing() }
func Second(value any) { value.Missing() }
`,
	})

	first := callsFrom(result, graph.FunctionID("pkg", "First"))
	second := callsFrom(result, graph.FunctionID("pkg", "Second"))
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("First calls = %#v, Second calls = %#v", first, second)
	}
	if first[0].To == second[0].To {
		t.Fatalf("separate unresolved callsites collapsed into hub %q", first[0].To)
	}
	for _, edge := range []graph.Edge{first[0], second[0]} {
		if !strings.HasPrefix(edge.To, "unresolved-callsite://") || edge.Meta["resolved"] != "false" || edge.Meta["confidence"] != "inferred" || edge.Meta["evidence"] == "" {
			t.Fatalf("unresolved edge metadata = %#v", edge)
		}
		node, ok := nodeByID(result, edge.To)
		if !ok || node.Meta["resolved"] != "false" || node.Meta["confidence"] != "inferred" || node.Meta["rationale"] == "" {
			t.Fatalf("unresolved node = %#v, found = %v", node, ok)
		}
	}
}

func TestGoAnalysisIsStableAcrossInputOrder(t *testing.T) {
	_, files := analyzeGoSources(t, map[string]string{
		"pkg/a.go": `package sample

type Alpha struct{}
func (Alpha) Work() {}
func CallAlpha(value Alpha) { value.Work() }
`,
		"pkg/b.go": `package sample

type Beta struct{}
func (Beta) Work() {}
func CallUnknown(value any) { value.Work() }
`,
	})

	forward := analyzeGoFiles(t, files)
	reversedFiles := append([]scan.File(nil), files...)
	for left, right := 0, len(reversedFiles)-1; left < right; left, right = left+1, right-1 {
		reversedFiles[left], reversedFiles[right] = reversedFiles[right], reversedFiles[left]
	}
	reversed := analyzeGoFiles(t, reversedFiles)
	if !reflect.DeepEqual(forward, reversed) {
		t.Fatalf("analysis changed with input order\nforward: %#v\nreverse: %#v", forward, reversed)
	}
}

func TestGoDeclarationsAndRelationshipsCarryHonestEvidence(t *testing.T) {
	result, _ := analyzeGoSources(t, map[string]string{
		"pkg/main.go": `package sample

func Helper() {}
func Run() { Helper() }
`,
	})

	run, ok := nodeByID(result, graph.FunctionID("pkg", "Run"))
	if !ok || run.Meta["confidence"] != "extracted" || run.Meta["evidence"] == "" {
		t.Fatalf("Run declaration metadata = %#v, found = %v", run, ok)
	}
	for _, edge := range result.Edges {
		if edge.Kind == graph.EdgeDefines && edge.To == run.ID {
			if edge.Meta["confidence"] != "extracted" || edge.Meta["evidence"] == "" {
				t.Fatalf("definition edge metadata = %#v", edge.Meta)
			}
		}
	}
	calls := callsFrom(result, run.ID)
	if len(calls) != 1 || calls[0].Meta["confidence"] != "inferred" || calls[0].Meta["confidence"] == "extracted" || calls[0].Meta["evidence"] == "" {
		t.Fatalf("call resolution metadata = %#v", calls)
	}
}

func TestGoSemanticEdgesResolveTypesValuesAndImplementationAssertions(t *testing.T) {
	result, _ := analyzeGoSources(t, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.26\n",
		"api/api.go": `package api

type Payload struct{}
type Runner interface { Run(Payload) error }

var Default Payload
func Make(Payload) Payload { return Payload{} }
`,
		"worker/worker.go": `package worker

import "example.com/app/api"

type Worker struct { Payload api.Payload }

var Shared api.Payload
var Alias = api.Default
var Callback = api.Make

func (*Worker) Run(value api.Payload) error {
	_ = api.Default
	var local api.Payload
	_ = local
	_ = value
	return nil
}

var _ api.Runner = (*Worker)(nil)
`,
	})

	workerID := graph.TypeID("worker", "Worker")
	payloadID := graph.TypeID("api", "Payload")
	runnerID := graph.TypeID("api", "Runner")
	defaultID := graph.TypeID("api", "Default")
	makeID := graph.FunctionID("api", "Make")
	runID := graph.MethodID("worker", "(*Worker)", "Run")

	assertSemanticEdge(t, result, graph.EdgeUsesType, workerID, payloadID)
	assertSemanticEdge(t, result, graph.EdgeUsesType, graph.TypeID("worker", "Shared"), payloadID)
	assertSemanticEdge(t, result, graph.EdgeUsesType, runID, payloadID)
	assertSemanticEdge(t, result, graph.EdgeReferences, graph.TypeID("worker", "Alias"), defaultID)
	assertSemanticEdge(t, result, graph.EdgeReferences, graph.TypeID("worker", "Callback"), makeID)
	assertSemanticEdge(t, result, graph.EdgeReferences, runID, defaultID)

	implements := assertSemanticEdge(t, result, graph.EdgeImplements, workerID, runnerID)
	if implements.Meta["implementation_evidence"] != "compile_time_assertion" {
		t.Fatalf("implementation metadata = %#v", implements.Meta)
	}
}

func TestGoSemanticEdgesUseTypeCheckedBindingsAndDoNotGuessShadowedValues(t *testing.T) {
	result, _ := analyzeGoSources(t, map[string]string{
		"pkg/main.go": `package sample

type Value struct{}
var Global Value

func UsesGlobal() Value { return Global }
func ShadowsGlobal() {
	Global := Value{}
	_ = Global
}
`,
	})

	globalID := graph.TypeID("pkg", "Global")
	valueID := graph.TypeID("pkg", "Value")
	assertSemanticEdge(t, result, graph.EdgeReferences, graph.FunctionID("pkg", "UsesGlobal"), globalID)
	assertSemanticEdge(t, result, graph.EdgeUsesType, graph.FunctionID("pkg", "UsesGlobal"), valueID)
	if edge, ok := semanticEdge(result, graph.EdgeReferences, graph.FunctionID("pkg", "ShadowsGlobal"), globalID); ok {
		t.Fatalf("shadowed package variable was guessed as a reference: %#v", edge)
	}
}

func TestGoSemanticEdgesInferOnlyTypeCheckedNonEmptyInterfaceImplementations(t *testing.T) {
	result, _ := analyzeGoSources(t, map[string]string{
		"pkg/main.go": `package sample

type Runner interface { Run() }
type Empty interface{}
type Worker struct{}
type Wrong struct{}

func (*Worker) Run() {}
func (*Wrong) Run(int) {}
`,
	})

	workerID := graph.TypeID("pkg", "Worker")
	runnerID := graph.TypeID("pkg", "Runner")
	implements := assertSemanticEdge(t, result, graph.EdgeImplements, workerID, runnerID)
	if implements.Meta["implementation"] != "pointer" || implements.Meta["implementation_evidence"] != "method_set" {
		t.Fatalf("implicit implementation metadata = %#v", implements.Meta)
	}
	for _, pair := range [][2]string{
		{workerID, graph.TypeID("pkg", "Empty")},
		{graph.TypeID("pkg", "Wrong"), runnerID},
	} {
		if edge, ok := semanticEdge(result, graph.EdgeImplements, pair[0], pair[1]); ok {
			t.Fatalf("invented implementation edge: %#v", edge)
		}
	}
}

func TestGoAnalyzerSeparatesSameDirectoryExternalTestPackage(t *testing.T) {
	result, _ := analyzeGoSources(t, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.26\n",
		"pkg/base.go": `package pkg

type Shared struct{}
func Same() {}
`,
		"pkg/internal_test.go": `package pkg

func InternalOnly() { Same() }
`,
		"pkg/external_test.go": `package pkg_test

import base "example.com/app/pkg"

func Same() {}
func Check() {
	Same()
	base.Same()
	_ = base.Shared{}
}
`,
	})

	externalQualifier := "pkg#package=pkg_test"
	baseSame := graph.FunctionID("pkg", "Same")
	externalSame := graph.FunctionID(externalQualifier, "Same")
	checkID := graph.FunctionID(externalQualifier, "Check")
	if baseSame == externalSame {
		t.Fatal("base and external-test function IDs collide")
	}
	for _, id := range []string{baseSame, externalSame, checkID, graph.FunctionID("pkg", "InternalOnly")} {
		if _, ok := nodeByID(result, id); !ok {
			t.Fatalf("missing separated symbol node %q", id)
		}
	}

	calls := callsFrom(result, checkID)
	if !hasTarget(calls, externalSame) || !hasTarget(calls, baseSame) {
		t.Fatalf("external test calls = %#v, want local %q and imported base %q", calls, externalSame, baseSame)
	}
	assertSemanticEdge(t, result, graph.EdgeUsesType, checkID, graph.TypeID("pkg", "Shared"))

	basePackage := graph.PackageID("pkg")
	externalPackage := graph.PackageID(externalQualifier)
	if basePackage == externalPackage {
		t.Fatal("base and external-test package IDs collide")
	}
	if !hasGraphEdge(result, graph.EdgeContains, basePackage, graph.FileID("pkg/base.go")) ||
		!hasGraphEdge(result, graph.EdgeContains, externalPackage, graph.FileID("pkg/external_test.go")) {
		t.Fatalf("package containment did not preserve package separation: %#v", edgesOfKind(result, graph.EdgeContains))
	}
}

func TestCallGraphDoesNotMisclassifyImportedTypeConversionsAsFunctions(t *testing.T) {
	result, _ := analyzeGoSources(t, map[string]string{
		"pkg/main.go": `package sample

import (
	"fmt"
	"time"
)

func Convert(value int64) time.Duration {
	_ = fmt.Sprintf("%d", value)
	return time.Duration(value)
}
`,
	})

	calls := callsFrom(result, graph.FunctionID("pkg", "Convert"))
	if len(calls) != 1 || calls[0].To != graph.ExternalFunctionID("fmt", "Sprintf") {
		t.Fatalf("calls = %#v, want fmt.Sprintf only", calls)
	}
	if _, ok := nodeByID(result, graph.ExternalFunctionID("time", "Duration")); ok {
		t.Fatal("imported time.Duration conversion emitted as an external function")
	}
}

func TestPartialGoFileRecoversDeclarationsWithoutCrossFileSemantics(t *testing.T) {
	result, _ := analyzeGoSourcesAllowDiagnostics(t, map[string]string{
		"pkg/complete.go": `package sample

func Complete() { Recovered() }
`,
		"pkg/partial.go": `package sample

import "fmt"

type RecoveredType struct{}

func Recovered() {
	fmt.Println("partial")
	Complete()
`,
	})

	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Level != "warning" ||
		!strings.Contains(result.Diagnostics[0].Message, "without cross-file semantics") {
		t.Fatalf("partial parse diagnostics = %#v", result.Diagnostics)
	}

	recoveredIDs := map[string]bool{
		graph.FunctionID("pkg", "Recovered"): true,
		graph.TypeID("pkg", "RecoveredType"): true,
	}
	for id := range recoveredIDs {
		node, ok := nodeByID(result, id)
		if !ok {
			t.Fatalf("missing recovered declaration %q; nodes = %#v", id, result.Nodes)
		}
		if node.Meta["partial"] != "true" || node.Meta["parse_complete"] != "false" {
			t.Fatalf("recovered declaration metadata = %#v", node.Meta)
		}
	}
	complete, ok := nodeByID(result, graph.FunctionID("pkg", "Complete"))
	if !ok || complete.Meta["partial"] != "" || complete.Meta["parse_complete"] != "" {
		t.Fatalf("complete declaration was marked partial: %#v", complete)
	}
	packageNode, ok := nodeByID(result, graph.PackageID("pkg"))
	if !ok || packageNode.Meta["partial"] != "true" || packageNode.Meta["parse_complete"] != "false" {
		t.Fatalf("package with a partial file lacks provenance: %#v", packageNode)
	}

	partialFileID := graph.FileID("pkg/partial.go")
	partialFile, ok := nodeByID(result, partialFileID)
	if !ok || partialFile.Meta["partial"] != "true" || partialFile.Meta["parse_complete"] != "false" {
		t.Fatalf("partial file lacks parse provenance: %#v", partialFile)
	}
	containsPartialFile := false
	for _, edge := range result.Edges {
		if edge.Kind == graph.EdgeContains && edge.To == partialFileID {
			containsPartialFile = edge.Meta["partial"] == "true" && edge.Meta["parse_complete"] == "false"
		}
		if edge.From == graph.FunctionID("pkg", "Recovered") && edge.Kind != graph.EdgeDefines {
			t.Fatalf("partial function emitted a semantic edge: %#v", edge)
		}
		if recoveredIDs[edge.To] && edge.Kind != graph.EdgeDefines {
			t.Fatalf("complete code resolved across a partial declaration: %#v", edge)
		}
	}
	if !containsPartialFile {
		t.Fatalf("missing partial package-to-file relationship: %#v", result.Edges)
	}
	if _, ok := nodeByID(result, graph.ImportID("fmt")); ok {
		t.Fatalf("partial import escaped declaration-only recovery: %#v", result.Nodes)
	}
}

func TestPartialGoFileWithoutPackageClauseRemainsRejected(t *testing.T) {
	result, _ := analyzeGoSourcesAllowDiagnostics(t, map[string]string{
		"broken.go": "func Broken() {\n",
	})
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Level != "error" {
		t.Fatalf("unrecoverable parse diagnostics = %#v", result.Diagnostics)
	}
	file, ok := nodeByID(result, graph.FileID("broken.go"))
	if !ok || file.Kind != graph.NodeFile || file.Meta["partial"] != "true" || file.Meta["parse_complete"] != "false" {
		t.Fatalf("unrecoverable file lacks safe structural recovery: %#v", result.Nodes)
	}
	if len(result.Nodes) != 1 || len(result.Edges) != 0 {
		t.Fatalf("unrecoverable file emitted non-structural facts: nodes=%#v edges=%#v", result.Nodes, result.Edges)
	}
}

func analyzeGoSources(t *testing.T, sources map[string]string) (*lang.AnalysisResult, []scan.File) {
	t.Helper()
	result, files := analyzeGoSourcesAllowDiagnostics(t, sources)
	if len(result.Diagnostics) > 0 {
		t.Fatalf("Analyze() diagnostics = %#v", result.Diagnostics)
	}
	return result, files
}

func analyzeGoSourcesAllowDiagnostics(t *testing.T, sources map[string]string) (*lang.AnalysisResult, []scan.File) {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, 0, len(sources))
	for path := range sources {
		paths = append(paths, path)
	}
	// Deliberately leave caller order irrelevant; the analyzer owns stable ordering.
	files := make([]scan.File, 0, len(paths))
	for _, path := range paths {
		absolute := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", filepath.Dir(absolute), err)
		}
		if err := os.WriteFile(absolute, []byte(sources[path]), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", absolute, err)
		}
		files = append(files, scan.File{Path: path, AbsPath: absolute, Language: "go"})
	}
	result, err := New(true).Analyze(context.Background(), root, files)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	return result, files
}

func analyzeGoFiles(t *testing.T, files []scan.File) *lang.AnalysisResult {
	t.Helper()
	result, err := New(true).Analyze(context.Background(), t.TempDir(), files)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if len(result.Diagnostics) > 0 {
		t.Fatalf("Analyze() diagnostics = %#v", result.Diagnostics)
	}
	return result
}

func edgesOfKind(result *lang.AnalysisResult, kind graph.EdgeKind) []graph.Edge {
	var edges []graph.Edge
	for _, edge := range result.Edges {
		if edge.Kind == kind {
			edges = append(edges, edge)
		}
	}
	return edges
}

func callsFrom(result *lang.AnalysisResult, from string) []graph.Edge {
	var edges []graph.Edge
	for _, edge := range result.Edges {
		if edge.Kind == graph.EdgeCalls && edge.From == from {
			edges = append(edges, edge)
		}
	}
	return edges
}

func nodeByID(result *lang.AnalysisResult, id string) (graph.Node, bool) {
	for _, node := range result.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return graph.Node{}, false
}

func semanticEdge(result *lang.AnalysisResult, kind graph.EdgeKind, from, to string) (graph.Edge, bool) {
	for _, edge := range result.Edges {
		if edge.Kind == kind && edge.From == from && edge.To == to {
			return edge, true
		}
	}
	return graph.Edge{}, false
}

func assertSemanticEdge(t *testing.T, result *lang.AnalysisResult, kind graph.EdgeKind, from, to string) graph.Edge {
	t.Helper()
	edge, ok := semanticEdge(result, kind, from, to)
	if !ok {
		t.Fatalf("missing %s edge %q -> %q; edges = %#v", kind, from, to, result.Edges)
	}
	if edge.Meta["confidence"] != "extracted" || edge.Meta["resolved"] != "true" || edge.Meta["evidence"] == "" || edge.Meta["rationale"] == "" {
		t.Fatalf("semantic edge metadata = %#v", edge.Meta)
	}
	return edge
}

func hasTarget(edges []graph.Edge, target string) bool {
	for _, edge := range edges {
		if edge.To == target {
			return true
		}
	}
	return false
}

func hasGraphEdge(result *lang.AnalysisResult, kind graph.EdgeKind, from, to string) bool {
	for _, edge := range result.Edges {
		if edge.Kind == kind && edge.From == from && edge.To == to {
			return true
		}
	}
	return false
}
