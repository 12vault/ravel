package community

import (
	"reflect"
	"sort"
	"testing"

	"github.com/12vault/ravel/internal/graph"
)

func TestAssignFindsSeparateCommunities(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "x"}, {ID: "y"}, {ID: "z"}},
		Edges: []graph.Edge{
			{From: "a", To: "b", Kind: graph.EdgeCalls}, {From: "b", To: "c", Kind: graph.EdgeCalls}, {From: "c", To: "a", Kind: graph.EdgeCalls},
			{From: "x", To: "y", Kind: graph.EdgeCalls}, {From: "y", To: "z", Kind: graph.EdgeCalls}, {From: "z", To: "x", Kind: graph.EdgeCalls},
		},
	}
	got := Assign(g)
	communities := nodeCommunities(got)
	if communities["a"] != communities["b"] || communities["b"] != communities["c"] {
		t.Fatalf("first component was split: %#v", communities)
	}
	if communities["x"] != communities["y"] || communities["y"] != communities["z"] {
		t.Fatalf("second component was split: %#v", communities)
	}
	if communities["a"] == communities["x"] {
		t.Fatalf("disconnected components share a community: %#v", communities)
	}
}

func TestAssignIsStableAcrossInputOrder(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{{ID: "c"}, {ID: "a"}, {ID: "b"}, {ID: "isolated", Meta: map[string]string{"keep": "yes"}}},
		Edges: []graph.Edge{{From: "b", To: "c", Kind: graph.EdgeReferences}, {From: "a", To: "b", Kind: graph.EdgeCalls}},
	}
	reversed := g
	reversed.Nodes = append([]graph.Node(nil), g.Nodes...)
	reversed.Edges = append([]graph.Edge(nil), g.Edges...)
	sort.Slice(reversed.Nodes, func(i, j int) bool { return reversed.Nodes[i].ID < reversed.Nodes[j].ID })
	sort.Slice(reversed.Edges, func(i, j int) bool { return reversed.Edges[i].From > reversed.Edges[j].From })

	first, second := nodeCommunities(Assign(g)), nodeCommunities(Assign(reversed))
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("community IDs changed with input order:\nfirst: %#v\nsecond: %#v", first, second)
	}
	assigned := Assign(g)
	for _, node := range assigned.Nodes {
		if node.ID == "isolated" && node.Meta["keep"] != "yes" {
			t.Fatal("existing metadata was not preserved")
		}
	}
	if _, exists := g.Nodes[3].Meta[MetaKey]; exists {
		t.Fatal("Assign mutated its input graph")
	}
}

func nodeCommunities(g graph.Graph) map[string]string {
	out := map[string]string{}
	for _, node := range g.Nodes {
		out[node.ID] = node.Meta[MetaKey]
	}
	return out
}
