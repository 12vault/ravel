package workflow

import (
	"testing"

	"github.com/12vault/ravel/internal/graph"
)

func TestBuildViews(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "file://app.go", Kind: graph.NodeFile, Name: "app.go", Path: "app.go", Meta: map[string]string{"language": "go"}},
			{ID: "go://main.run", Kind: graph.NodeFunction, Name: "run", Path: "app.go"},
			{ID: "domain://billing", Kind: graph.NodeDomain, Name: "Billing"},
			{ID: "schema://db", Kind: graph.NodeSchema, Name: "db.sql"},
			{ID: "view://db#active", Kind: graph.NodeView, Name: "active"},
			{ID: "index://db#active_id", Kind: graph.NodeIndex, Name: "active_id"},
		},
		Edges:   []graph.Edge{{ID: "defines://1", Kind: graph.EdgeDefines, From: "file://app.go", To: "go://main.run"}},
		Metrics: graph.Metrics{NodesByKind: map[graph.NodeKind]int{graph.NodeFile: 1, graph.NodeDomain: 1, graph.NodeSchema: 1, graph.NodeView: 1, graph.NodeIndex: 1}, Languages: map[string]int{"go": 1}},
	}
	for _, mode := range []string{"tech", "understand", "learn", "docs", "pdf", "schema"} {
		if _, err := Build(mode, g, nil); err != nil {
			t.Fatalf("Build(%s): %v", mode, err)
		}
	}
	view, err := Build("diff", g, []string{"app.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Nodes) != 2 {
		t.Fatalf("diff nodes = %d, want 2", len(view.Nodes))
	}
	schemaView, err := Build("schema", g, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !viewHasKind(schemaView, graph.NodeView) || !viewHasKind(schemaView, graph.NodeIndex) {
		t.Fatalf("schema view omitted SQL views or indexes: %#v", schemaView.Nodes)
	}
}

func viewHasKind(view View, kind graph.NodeKind) bool {
	for _, node := range view.Nodes {
		if node.Kind == kind {
			return true
		}
	}
	return false
}
