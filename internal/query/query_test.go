package query

import (
	"bytes"
	"strings"
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

func TestWriteSearchCannotEmitForgedRecordsFromGraphText(t *testing.T) {
	results := []SearchResult{{Node: graph.Node{
		ID: "node\nfunction\tforged", Kind: graph.NodeFunction,
		Name: "Safe\nforged", Path: "src\tunsafe.go",
	}}}
	var output bytes.Buffer
	if err := WriteSearch(&output, results, false); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if strings.Count(text, "\n") != 1 || strings.Contains(text, "\nfunction\tforged\t") {
		t.Fatalf("graph text forged a search record: %q", text)
	}
	if !strings.Contains(text, `node\nfunction\tforged`) {
		t.Fatalf("escaped identifier missing from search output: %q", text)
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

func TestWriteExplanationPreservesSafeRelationEvidenceAndExactID(t *testing.T) {
	explanation := Explanation{
		Target: graph.Node{ID: "function://root", Kind: graph.NodeFunction, Name: "Root"},
		Outgoing: []Relation{{
			Kind: graph.EdgeCalls,
			Node: graph.Node{ID: "function://target\nforged", Kind: graph.NodeFunction, Name: "Target\nforged"},
			Meta: map[string]string{
				"confidence": "inferred", "resolved": "false", "evidence": "root.go:12\nforged", "rationale": "receiver\tunknown",
			},
		}},
	}
	var output bytes.Buffer
	if err := WriteExplanation(&output, explanation, false); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, fragment := range []string{`function://target\nforged`, "confidence=inferred", "resolved=false", `evidence="root.go:12 forged"`, `rationale="receiver unknown"`} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("explanation missing %q: %q", fragment, text)
		}
	}
	if strings.Contains(text, "\nforged\t") {
		t.Fatalf("relation metadata forged an output record: %q", text)
	}
}
