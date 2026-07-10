package build

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/12ya/reporavel/internal/config"
	"github.com/12ya/reporavel/internal/graph"
)

func TestRunBuildsGoGraph(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "simple-go-service")
	result, err := Run(context.Background(), root, config.Default())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	wantNodes := []string{
		"file://cmd/api/main.go",
		"go://cmd/api.main",
		"go://internal/auth.(*SessionManager).CreateSession",
		"go://internal/auth.saveSession",
		"go://internal/db.SessionStore",
	}
	for _, id := range wantNodes {
		if !hasNode(result.Graph, id) {
			t.Fatalf("missing node %s", id)
		}
	}
	if !hasEdge(result.Graph, graph.EdgeCalls, "go://internal/auth.(*SessionManager).CreateSession", "go://internal/auth.saveSession") {
		t.Fatalf("missing call edge from CreateSession to saveSession")
	}
}

func TestRunCanDisableGoAnalysis(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "simple-go-service")
	cfg := config.Default()
	cfg.Analysis.Go = false
	result, err := Run(context.Background(), root, cfg)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, node := range result.Graph.Nodes {
		if node.Kind == graph.NodePackage || graph.SymbolKind(node.Kind) {
			t.Fatalf("found semantic node %s with Go analysis disabled", node.ID)
		}
	}
}

func hasNode(g graph.Graph, id string) bool {
	for _, n := range g.Nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

func hasEdge(g graph.Graph, kind graph.EdgeKind, from, to string) bool {
	for _, e := range g.Edges {
		if e.Kind == kind && e.From == from && e.To == to {
			return true
		}
	}
	return false
}
