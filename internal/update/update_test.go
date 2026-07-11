package update

import (
	"testing"

	"github.com/12ya/reporavel/internal/graph"
	"github.com/12ya/reporavel/internal/scan"
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
	current := graph.NewBuilder("/repo").Build()
	previous := graph.Graph{Root: "/repo", Nodes: []graph.Node{
		{ID: "domain://billing", Kind: graph.NodeDomain, Name: "Billing", Meta: map[string]string{"source": "domain-analyzer"}},
		{ID: "python://app.run", Kind: graph.NodeFunction, Name: "run", Path: "app.py", Meta: map[string]string{"source": "code-analyzer"}},
	}}
	merged := preserveEnrichment(current, previous, map[string]bool{"app.py": true})
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
