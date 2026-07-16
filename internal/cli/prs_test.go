package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/graph"
	pranalysis "github.com/12vault/ravel/internal/prs"
	"github.com/12vault/ravel/internal/store"
)

func TestPRsCommandAnalyzesOfflineManifestAndFiltersNumber(t *testing.T) {
	root := t.TempDir()
	graphDir := filepath.Join(root, "graph")
	if err := os.MkdirAll(graphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	g := graph.Graph{Nodes: []graph.Node{{ID: graph.FileID("main.go"), Kind: graph.NodeFile, Name: "main.go", Path: "main.go"}}}
	if err := store.WriteJSON(filepath.Join(graphDir, "graph.json"), g); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "prs.json")
	pullRequests := []pranalysis.PullRequest{
		{Number: 1, Title: "first", Files: []pranalysis.File{{Path: "main.go"}}},
		{Number: 2, Title: "second", Files: []pranalysis.File{{Path: "main.go"}}},
	}
	if err := store.WriteJSON(manifest, pullRequests); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Execute(context.Background(), []string{"prs", "--out", graphDir, "--manifest", manifest}, &output, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "#1 first") || !strings.Contains(output.String(), "#1 ↔ #2 [file overlap]") {
		t.Fatalf("PR output:\n%s", output.String())
	}
	output.Reset()
	if err := Execute(context.Background(), []string{"prs", "--out", graphDir, "--manifest", manifest, "--json", "2"}, &output, &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), `"number": 1`) || !strings.Contains(output.String(), `"number": 2`) {
		t.Fatalf("filtered PR JSON:\n%s", output.String())
	}
}
