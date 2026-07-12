package update

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	buildrunner "github.com/12vault/ravel/internal/build"
	"github.com/12vault/ravel/internal/community"
	"github.com/12vault/ravel/internal/config"
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

func TestRunRemapsLabelAndInvalidatesDescriptionWhenMembershipChanges(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/remap\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	appPath := filepath.Join(root, "app.go")
	if err := os.WriteFile(appPath, []byte("package remap\n\nfunc One() {}\nfunc Two() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	first, err := buildrunner.Run(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	previous := community.AssignWithOptions(first.Graph, community.Options{Granularity: community.PresetBalanced, HubDegreeThreshold: 0})
	oldCommunity := ""
	for _, node := range previous.Nodes {
		if node.ID == graph.FileID("app.go") {
			oldCommunity = node.Meta[community.MetaKey]
		}
	}
	if oldCommunity == "" {
		t.Fatal("app.go community not found")
	}
	for i := range previous.Nodes {
		if previous.Nodes[i].Meta[community.MetaKey] != oldCommunity {
			continue
		}
		previous.Nodes[i].Meta[community.MetaLabelKey] = "Application Core"
		previous.Nodes[i].Meta[community.MetaDescriptionKey] = "Contains exactly two functions."
	}
	if err := os.WriteFile(appPath, []byte("package remap\n\nfunc One() {}\nfunc Two() {}\nfunc Three() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, err := Run(context.Background(), root, cfg, previous, first.Scan)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range updated.Build.Graph.Nodes {
		if node.ID != graph.FileID("app.go") {
			continue
		}
		if node.Meta[community.MetaKey] == oldCommunity {
			t.Fatal("membership ID did not change after adding a community member")
		}
		if node.Meta[community.MetaLabelKey] != "Application Core" || node.Meta[community.MetaLabelStatusKey] != "remapped" {
			t.Fatalf("label continuity metadata = %#v", node.Meta)
		}
		if node.Meta[community.MetaDescriptionKey] != "" {
			t.Fatalf("stale description survived: %#v", node.Meta)
		}
		return
	}
	t.Fatal("updated app.go node not found")
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
