package query

import (
	"testing"

	"github.com/12ya/reporavel/internal/graph"
)

func TestSearchAcceptsNaturalLanguageTerms(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{
		{ID: "domain://auth", Kind: graph.NodeDomain, Name: "Authentication"},
		{ID: "domain://billing", Kind: graph.NodeDomain, Name: "Billing"},
	}}
	results := Search(g, "which parts handle authentication?", 10)
	if len(results) == 0 || results[0].Node.ID != "domain://auth" {
		t.Fatalf("Search() = %#v", results)
	}
}

func TestExplainIncludesSemanticRelations(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "domain://checkout", Kind: graph.NodeDomain, Name: "Checkout"},
			{ID: "flow://pay", Kind: graph.NodeFlow, Name: "Pay"},
		},
		Edges: []graph.Edge{{Kind: graph.EdgeBelongsTo, From: "flow://pay", To: "domain://checkout"}},
	}
	explanation, ok := Explain(g, "Pay")
	if !ok || len(explanation.Outgoing) != 1 || explanation.Outgoing[0].Kind != graph.EdgeBelongsTo {
		t.Fatalf("Explain() = %#v, %v", explanation, ok)
	}
}
