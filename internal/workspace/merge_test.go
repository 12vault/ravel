package workspace

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/12vault/ravel/internal/graph"
)

func TestMergeNamespacesCollidingRepositoryIDsAndPreservesEdges(t *testing.T) {
	generated := time.Date(2026, 7, 16, 1, 2, 3, 0, time.UTC)
	fixture := func(root, value string) graph.Graph {
		return graph.Graph{
			Root: root, GeneratedAt: generated,
			Nodes: []graph.Node{
				{ID: graph.RepoID(), Kind: graph.NodeRepo, Name: root, Path: "."},
				{ID: graph.FileID("main.go"), Kind: graph.NodeFile, Name: "main.go", Path: "main.go", Meta: map[string]string{"value": value}},
			},
			Edges:       []graph.Edge{{ID: "old-edge", Kind: graph.EdgeContains, From: graph.RepoID(), To: graph.FileID("main.go")}},
			Diagnostics: []graph.Diagnostic{{Path: "main.go", Level: "warning", Message: value}},
		}
	}
	merged, err := Merge([]Source{
		{Alias: "beta", Location: "/graphs/beta", Graph: fixture("/src/beta", "b")},
		{Alias: "alpha", Location: "/graphs/alpha", Graph: fixture("/src/alpha", "a")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if merged.Root != "ravel-workspace" || !merged.GeneratedAt.Equal(generated) {
		t.Fatalf("merged identity = root %q generated %s", merged.Root, merged.GeneratedAt)
	}
	if len(merged.Nodes) != 5 { // workspace root + two project roots + two files
		t.Fatalf("merged nodes = %#v", merged.Nodes)
	}
	projects := map[string]bool{}
	files := map[string]bool{}
	for _, node := range merged.Nodes {
		if node.Kind == graph.NodeRepo && node.Meta["project"] != "" {
			projects[node.Meta["project"]] = true
		}
		if node.Kind == graph.NodeFile {
			files[node.Path] = true
			if node.Meta["original_id"] != graph.FileID("main.go") {
				t.Fatalf("file lacks original identity: %#v", node)
			}
		}
	}
	if !reflect.DeepEqual(projects, map[string]bool{"alpha": true, "beta": true}) {
		t.Fatalf("projects = %#v", projects)
	}
	if !reflect.DeepEqual(files, map[string]bool{"alpha/main.go": true, "beta/main.go": true}) {
		t.Fatalf("files = %#v", files)
	}
	if len(merged.Edges) != 4 { // workspace containment + source containment per project
		t.Fatalf("merged edges = %#v", merged.Edges)
	}
	for _, edge := range merged.Edges {
		if edge.ID == "old-edge" {
			t.Fatalf("source edge id was not recalculated: %#v", edge)
		}
	}
	if got := []string{merged.Diagnostics[0].Path, merged.Diagnostics[1].Path}; !reflect.DeepEqual(got, []string{"alpha/main.go", "beta/main.go"}) {
		t.Fatalf("diagnostic paths = %#v", got)
	}
}

func TestMergeIsStableAcrossSourceOrder(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{{ID: graph.RepoID(), Kind: graph.NodeRepo, Name: "repo"}}}
	left, err := Merge([]Source{{Alias: "a", Graph: g}, {Alias: "b", Graph: g}})
	if err != nil {
		t.Fatal(err)
	}
	right, err := Merge([]Source{{Alias: "b", Graph: g}, {Alias: "a", Graph: g}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("source order changed merged graph:\nleft=%#v\nright=%#v", left, right)
	}
}

func TestMergeRejectsInvalidAliasesAndUnknownEndpoints(t *testing.T) {
	if _, err := Merge([]Source{{Alias: "bad alias", Graph: graph.Graph{}}}); err == nil || !strings.Contains(err.Error(), "invalid project alias") {
		t.Fatalf("invalid alias error = %v", err)
	}
	if _, err := Merge([]Source{{Alias: "same", Graph: graph.Graph{}}, {Alias: "same", Graph: graph.Graph{}}}); err == nil || !strings.Contains(err.Error(), "duplicate project alias") {
		t.Fatalf("duplicate alias error = %v", err)
	}
	g := graph.Graph{
		Nodes: []graph.Node{{ID: "known", Kind: graph.NodeFile, Name: "known"}},
		Edges: []graph.Edge{{Kind: graph.EdgeCalls, From: "known", To: "missing"}},
	}
	if _, err := Merge([]Source{{Alias: "valid", Graph: g}}); err == nil || !strings.Contains(err.Error(), "unknown endpoint") {
		t.Fatalf("unknown endpoint error = %v", err)
	}
}
