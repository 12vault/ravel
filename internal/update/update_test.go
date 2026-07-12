package update

import (
	"testing"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/scan"
)

func TestChangesFindsModifiedAddedAndRemovedFiles(t *testing.T) {
	oldFiles := []scan.File{{Path: "same.go", Hash: "1"}, {Path: "changed.go", Hash: "1"}, {Path: "gone.go", Hash: "1"}}
	newFiles := []scan.File{{Path: "same.go", Hash: "1"}, {Path: "changed.go", Hash: "2"}, {Path: "new.go", Hash: "1"}}
	changed, removed := changes(oldFiles, newFiles)
	if len(changed) != 2 || changed[0] != "changed.go" || changed[1] != "new.go" || len(removed) != 1 || removed[0] != "gone.go" {
		t.Fatalf("changed=%v removed=%v", changed, removed)
	}
}

func TestPreserveEnrichmentDropsChangedFileNodes(t *testing.T) {
	builder := graph.NewBuilder("/repo")
	builder.AddNode(graph.Node{ID: graph.FileID("app.py"), Kind: graph.NodeFile, Name: "app.py", Path: "app.py", Meta: map[string]string{"hash": "new"}})
	current := builder.Build()
	previous := graph.Graph{Root: "/repo", Nodes: []graph.Node{
		{ID: "domain://billing", Kind: graph.NodeDomain, Name: "Billing", Meta: map[string]string{"source": "domain-analyzer", "sourceHashes": `{"app.py":"new"}`}},
		{ID: "python://app.run", Kind: graph.NodeFunction, Name: "run", Path: "app.py", Meta: map[string]string{"source": "code-analyzer", "sourceHashes": `{"app.py":"old"}`}},
	}}
	merged := preserveEnrichment(current, previous)
	if !hasNode(merged, "domain://billing") || hasNode(merged, "python://app.run") {
		t.Fatalf("nodes=%#v", merged.Nodes)
	}
}

func hasNode(g graph.Graph, id string) bool {
	for _, node := range g.Nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}
