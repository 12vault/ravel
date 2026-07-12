package query

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/graph"
)

func TestResolveTargetRejectsDuplicateExactNames(t *testing.T) {
	idx := NewIndex(graph.Graph{Nodes: []graph.Node{
		{ID: "go://b.Handler", Kind: graph.NodeFunction, Name: "Handler", Path: "b.go"},
		{ID: "go://a.Handler", Kind: graph.NodeFunction, Name: "Handler", Path: "a.go"},
	}})
	_, err := idx.ResolveTarget("Handler")
	if !IsTargetError(err, TargetAmbiguous) {
		t.Fatalf("ResolveTarget duplicate name error = %v", err)
	}
	targetErr := err.(*TargetError)
	got := []string{targetErr.Candidates[0].ID, targetErr.Candidates[1].ID}
	want := []string{"go://a.Handler", "go://b.Handler"}
	if !reflect.DeepEqual(got, want) || !strings.Contains(err.Error(), "use an exact node ID") {
		t.Fatalf("ambiguous candidates = %v, error=%q", got, err)
	}
	if _, ok := idx.Explain("Handler"); ok {
		t.Fatal("legacy Explain must not silently select an ambiguous exact name")
	}
	if _, err := idx.ExplainResolved("Handler"); !IsTargetError(err, TargetAmbiguous) {
		t.Fatalf("ExplainResolved ambiguity = %v", err)
	}
}

func TestResolveTargetPrefersCanonicalFileForSharedExactPath(t *testing.T) {
	idx := NewIndex(graph.Graph{Nodes: []graph.Node{
		{ID: "go://pkg.Run", Kind: graph.NodeFunction, Name: "Run", Path: "pkg/run.go"},
		{ID: "file://pkg/run.go", Kind: graph.NodeFile, Name: "run.go", Path: "pkg/run.go"},
	}})
	node, err := idx.ResolveTarget("pkg/run.go")
	if err != nil || node.ID != "file://pkg/run.go" {
		t.Fatalf("ResolveTarget(path) = %#v, %v", node, err)
	}
}

func TestResolveTargetPrefersPackageOverDirectoryForSharedPath(t *testing.T) {
	idx := NewIndex(graph.Graph{Nodes: []graph.Node{
		{ID: "dir://pkg", Kind: graph.NodeDir, Name: "pkg", Path: "pkg"},
		{ID: "go-package://pkg", Kind: graph.NodePackage, Name: "pkg", Path: "pkg"},
	}})
	node, err := idx.ResolveTarget("pkg")
	if err != nil || node.ID != "go-package://pkg" {
		t.Fatalf("ResolveTarget(package path) = %#v, %v", node, err)
	}
}

func TestShortestPathResultLabelsReverseFallbackAndEdgeOrientation(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "a", Kind: graph.NodeFunction, Name: "A"},
			{ID: "b", Kind: graph.NodeFunction, Name: "B"},
		},
		Edges: []graph.Edge{{
			ID: "calls://b-a", Kind: graph.EdgeCalls, From: "b", To: "a",
			Meta: map[string]string{"evidence": "b.go:7"},
		}},
	}
	result, ok, err := NewIndex(g).ShortestPathResult("a", "b")
	if err != nil || !ok {
		t.Fatalf("ShortestPathResult = %#v, %v, %v", result, ok, err)
	}
	if result.Mode != PathUndirectedFallback || len(result.Hops) != 1 || result.Hops[0].Direction != PathHopReverse {
		t.Fatalf("fallback path metadata = %#v", result)
	}
	if result.Hops[0].From != "a" || result.Hops[0].To != "b" || result.Hops[0].Edge.From != "b" || result.Hops[0].Edge.To != "a" {
		t.Fatalf("fallback edge orientation = %#v", result.Hops[0])
	}

	var text bytes.Buffer
	if err := WritePathResult(&text, result, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mode=undirected_fallback", "direction=reverse", "kind=calls", "graph=b->a", "evidence=\"b.go:7\""} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("path output missing %q:\n%s", want, text.String())
		}
	}

	var encoded bytes.Buffer
	if err := WritePathResult(&encoded, result, true); err != nil {
		t.Fatal(err)
	}
	var decoded PathResult
	if err := json.Unmarshal(encoded.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, result) {
		t.Fatalf("path JSON = %#v, want %#v", decoded, result)
	}
}

func TestShortestPathResultUsesDirectedModeWhenAvailable(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{{ID: "a", Kind: graph.NodeFunction, Name: "A"}, {ID: "b", Kind: graph.NodeFunction, Name: "B"}},
		Edges: []graph.Edge{{ID: "calls://a-b", Kind: graph.EdgeCalls, From: "a", To: "b"}},
	}
	result, ok, err := ShortestPathResultFor(g, "A", "B")
	if err != nil || !ok || result.Mode != PathDirected || result.Hops[0].Direction != PathHopForward {
		t.Fatalf("directed result = %#v, %v, %v", result, ok, err)
	}
}

func TestShortestPathResultRejectsAmbiguousEndpoint(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{
		{ID: "a1", Kind: graph.NodeFunction, Name: "A"},
		{ID: "a2", Kind: graph.NodeFunction, Name: "A"},
		{ID: "b", Kind: graph.NodeFunction, Name: "B"},
	}}
	_, _, err := NewIndex(g).ShortestPathResult("A", "B")
	if !IsTargetError(err, TargetAmbiguous) || !strings.Contains(err.Error(), "path start") {
		t.Fatalf("ambiguous path endpoint error = %v", err)
	}
}
