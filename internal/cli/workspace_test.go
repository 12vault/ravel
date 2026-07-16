package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/store"
)

func TestMergeAndGlobalCommandsBuildQueryableNamespacedGraphs(t *testing.T) {
	root := t.TempDir()
	writeGraph := func(alias string) string {
		dir := filepath.Join(root, alias)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		g := graph.Graph{
			Root: "/source/" + alias,
			Nodes: []graph.Node{
				{ID: graph.RepoID(), Kind: graph.NodeRepo, Name: alias, Path: "."},
				{ID: graph.FileID("main.go"), Kind: graph.NodeFile, Name: "main.go", Path: "main.go", Meta: map[string]string{"language": "go"}},
			},
			Edges: []graph.Edge{{Kind: graph.EdgeContains, From: graph.RepoID(), To: graph.FileID("main.go")}},
		}
		if err := store.WriteJSON(filepath.Join(dir, "graph.json"), g); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	alpha := writeGraph("alpha")
	beta := writeGraph("beta")
	mergedDir := filepath.Join(root, "merged")
	var stdout bytes.Buffer
	if err := Execute(context.Background(), []string{"merge", "--out", mergedDir, "alpha=" + alpha, "beta=" + beta}, &stdout, &stdout); err != nil {
		t.Fatal(err)
	}
	merged, err := store.LoadGraph(mergedDir)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, node := range merged.Nodes {
		if node.Kind == graph.NodeFile {
			paths[node.Path] = true
		}
	}
	if !paths["alpha/main.go"] || !paths["beta/main.go"] {
		t.Fatalf("merged file paths = %#v", paths)
	}

	registry := filepath.Join(root, "registry.json")
	for _, args := range [][]string{
		{"global", "add", "--registry", registry, "alpha", alpha},
		{"global", "add", "--registry", registry, "beta", beta},
	} {
		stdout.Reset()
		if err := Execute(context.Background(), args, &stdout, &stdout); err != nil {
			t.Fatal(err)
		}
	}
	stdout.Reset()
	if err := Execute(context.Background(), []string{"global", "list", "--registry", registry}, &stdout, &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "alpha\t") || !strings.Contains(stdout.String(), "beta\t") {
		t.Fatalf("global list output:\n%s", stdout.String())
	}
	stdout.Reset()
	if err := Execute(context.Background(), []string{"global", "query", "--registry", registry, "main.go"}, &stdout, &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "alpha/main.go") || !strings.Contains(stdout.String(), "beta/main.go") {
		t.Fatalf("global query output:\n%s", stdout.String())
	}
	globalDir := filepath.Join(root, "global")
	stdout.Reset()
	if err := Execute(context.Background(), []string{"global", "build", "--registry", registry, "--out", globalDir}, &stdout, &stdout); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadGraph(globalDir); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := Execute(context.Background(), []string{"global", "remove", "--registry", registry, "alpha"}, &stdout, &stdout); err != nil {
		t.Fatal(err)
	}
}
