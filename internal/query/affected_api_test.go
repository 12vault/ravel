package query

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/12ya/reporavel/internal/graph"
)

func TestIndexAffectedTraversesIncomingDependentsOnly(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "caller", Kind: graph.NodeFunction, Name: "Caller"},
			{ID: "target", Kind: graph.NodeFunction, Name: "Target"},
			{ID: "callee", Kind: graph.NodeFunction, Name: "Callee"},
		},
		Edges: []graph.Edge{
			{ID: "calls://caller-target", Kind: graph.EdgeCalls, From: "caller", To: "target"},
			{ID: "calls://target-callee", Kind: graph.EdgeCalls, From: "target", To: "callee"},
		},
	}
	result, err := NewIndex(g).Affected("Target", RetrieveOptions{
		Relations: []graph.EdgeKind{graph.EdgeCalls}, MaxDepth: 2, MaxNodes: 10,
		HubDegreeThreshold: -1, TokenBudget: 1_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Direction != DirectionIn || result.Stats.Traversal != TraversalBFS || !reflect.DeepEqual(result.Stats.SeedIDs, []string{"target"}) {
		t.Fatalf("affected stats = %#v", result.Stats)
	}
	ids := map[string]bool{}
	for _, node := range result.Nodes {
		ids[node.ID] = true
	}
	if !ids["target"] || !ids["caller"] || ids["callee"] {
		t.Fatalf("affected node IDs = %v", ids)
	}

	wrapper, err := Affected(g, "Target", RetrieveOptions{
		Relations: []graph.EdgeKind{graph.EdgeCalls}, MaxDepth: 2, MaxNodes: 10,
		HubDegreeThreshold: -1, TokenBudget: 1_000,
	})
	if err != nil || !reflect.DeepEqual(wrapper, result) {
		t.Fatalf("Affected wrapper = %#v, %v; want %#v", wrapper, err, result)
	}
}

func TestWriteAffectedLabelsCompactOutput(t *testing.T) {
	result := Retrieval{
		Query: "target",
		Nodes: []ContextNode{{ID: "target", Kind: graph.NodeFunction, Name: "Target", Seed: true}},
		Stats: RetrievalStats{Traversal: TraversalBFS, Direction: DirectionIn, Depth: 2, TokenBudget: 256},
	}
	var output bytes.Buffer
	if err := WriteAffected(&output, result, false); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "RAVEL_AFFECTED") {
		t.Fatalf("affected output = %q", output.String())
	}
}

func TestReusableIndexExplainAndShortestPathMatchGraphWrappers(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "one", Kind: graph.NodeFunction, Name: "One"},
			{ID: "two", Kind: graph.NodeFunction, Name: "Two"},
		},
		Edges: []graph.Edge{{ID: "calls://one-two", Kind: graph.EdgeCalls, From: "one", To: "two"}},
	}
	idx := NewIndex(g)
	indexedExplanation, indexedOK := idx.Explain("One")
	wrapperExplanation, wrapperOK := Explain(g, "One")
	if indexedOK != wrapperOK || !reflect.DeepEqual(indexedExplanation, wrapperExplanation) {
		t.Fatalf("indexed explanation = %#v, %v; wrapper = %#v, %v", indexedExplanation, indexedOK, wrapperExplanation, wrapperOK)
	}
	indexedPath, indexedOK := idx.ShortestPath("One", "Two")
	wrapperPath, wrapperOK := ShortestPath(g, "One", "Two")
	if indexedOK != wrapperOK || !reflect.DeepEqual(indexedPath, wrapperPath) {
		t.Fatalf("indexed path = %#v, %v; wrapper = %#v, %v", indexedPath, indexedOK, wrapperPath, wrapperOK)
	}
}
