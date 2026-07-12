package ingest

import (
	"testing"

	"github.com/12vault/ravel/internal/graph"
)

func TestApplyMergesAgentFragmentWithProvenance(t *testing.T) {
	builder := graph.NewBuilder("/repo")
	builder.AddNode(graph.Node{ID: graph.FileID("app.py"), Kind: graph.NodeFile, Name: "app.py", Path: "app.py", Meta: map[string]string{"hash": "abc"}})
	current := builder.Build()
	fragment := Fragment{
		Version:     1,
		Source:      "code-analyzer",
		SourcePaths: []string{"app.py"},
		Nodes:       []graph.Node{{ID: "python://app.run", Kind: graph.NodeFunction, Name: "run", Path: "app.py", Meta: map[string]string{"confidence": "extracted", "evidence": "app.py:1"}}},
		Edges:       []graph.Edge{{Kind: graph.EdgeDefines, From: graph.FileID("app.py"), To: "python://app.run", Meta: map[string]string{"confidence": "extracted", "evidence": "app.py:1"}}},
	}

	merged, err := Apply(current, fragment)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Nodes) != len(current.Nodes)+1 {
		t.Fatalf("nodes = %d, want %d", len(merged.Nodes), len(current.Nodes)+1)
	}
	for _, node := range merged.Nodes {
		if node.ID == "python://app.run" && node.Meta["source"] != "code-analyzer" {
			t.Fatalf("missing provenance: %#v", node.Meta)
		}
	}
}

func TestApplyRejectsUnknownEndpoints(t *testing.T) {
	_, err := Apply(graph.NewBuilder("/repo").Build(), Fragment{
		Version:     1,
		Source:      "domain-analyzer",
		SourcePaths: []string{"app.py"},
		Edges:       []graph.Edge{{Kind: graph.EdgeFlowsTo, From: "flow://a", To: "flow://b", Meta: map[string]string{"confidence": "inferred", "rationale": "control flow"}}},
	})
	if err == nil {
		t.Fatal("expected unknown endpoint error")
	}
}

func TestValidateRejectsUnsupportedProvenanceClaims(t *testing.T) {
	base := Fragment{Version: 1, Source: "agent", SourcePaths: []string{"app.py"}}
	base.Nodes = []graph.Node{{ID: "domain://x", Kind: graph.NodeDomain, Name: "X", Meta: map[string]string{"confidence": "extracted"}}}
	if err := Validate(base); err == nil {
		t.Fatal("expected extracted claim without evidence to fail")
	}
	base.Nodes[0].Meta = map[string]string{"confidence": "certain", "evidence": "app.py:1"}
	if err := Validate(base); err == nil {
		t.Fatal("expected unknown confidence to fail")
	}
}
