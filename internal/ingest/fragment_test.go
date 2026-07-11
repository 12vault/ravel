package ingest

import (
	"testing"

	"github.com/12ya/reporavel/internal/graph"
)

func TestApplyMergesAgentFragmentWithProvenance(t *testing.T) {
	builder := graph.NewBuilder("/repo")
	builder.AddNode(graph.Node{ID: graph.FileID("app.py"), Kind: graph.NodeFile, Name: "app.py", Path: "app.py"})
	current := builder.Build()
	fragment := Fragment{
		Version: 1,
		Source:  "code-analyzer",
		Nodes:   []graph.Node{{ID: "python://app.run", Kind: graph.NodeFunction, Name: "run", Path: "app.py"}},
		Edges:   []graph.Edge{{Kind: graph.EdgeDefines, From: graph.FileID("app.py"), To: "python://app.run"}},
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
		Version: 1,
		Source:  "domain-analyzer",
		Edges:   []graph.Edge{{Kind: graph.EdgeFlowsTo, From: "flow://a", To: "flow://b"}},
	})
	if err == nil {
		t.Fatal("expected unknown endpoint error")
	}
}
