package contentanalyzer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/12ya/reporavel/internal/graph"
	"github.com/12ya/reporavel/internal/scan"
)

func TestMarkdownExtractsSectionsAndLinks(t *testing.T) {
	file := testFile(t, "guide.md", "# Guide\nSee [API](api.md).\n## Setup\n")
	result, err := Markdown().Analyze(context.Background(), "", []scan.File{file})
	if err != nil {
		t.Fatal(err)
	}
	if countKind(result.Nodes, graph.NodeSection) != 2 || countEdges(result.Edges, graph.EdgeCites) != 1 {
		t.Fatalf("nodes=%#v edges=%#v", result.Nodes, result.Edges)
	}
}

func TestSQLExtractsTablesAndColumns(t *testing.T) {
	file := testFile(t, "schema.sql", "CREATE TABLE users (\n id UUID PRIMARY KEY,\n email TEXT NOT NULL\n);\n")
	result, err := SQL().Analyze(context.Background(), "", []scan.File{file})
	if err != nil {
		t.Fatal(err)
	}
	if countKind(result.Nodes, graph.NodeTable) != 1 || countKind(result.Nodes, graph.NodeColumn) != 2 {
		t.Fatalf("nodes=%#v", result.Nodes)
	}
}

func testFile(t *testing.T, name, content string) scan.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return scan.File{Path: name, AbsPath: path}
}

func countKind(nodes []graph.Node, kind graph.NodeKind) int {
	count := 0
	for _, node := range nodes {
		if node.Kind == kind {
			count++
		}
	}
	return count
}

func countEdges(edges []graph.Edge, kind graph.EdgeKind) int {
	count := 0
	for _, edge := range edges {
		if edge.Kind == kind {
			count++
		}
	}
	return count
}
