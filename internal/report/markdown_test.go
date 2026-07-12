package report

import (
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/graph"
)

func TestMarkdownCommunitySummaryCanBeDisabled(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{{ID: "a", Kind: graph.NodeFile, Name: "a.go", Path: "internal/query/a.go"}}}
	with := MarkdownConfigured(g, true)
	if !strings.Contains(with, "## Communities") || !strings.Contains(with, "internal/query") {
		t.Fatalf("missing community summary:\n%s", with)
	}
	without := MarkdownConfigured(g, false)
	if strings.Contains(without, "Communities") || strings.Contains(without, "community") {
		t.Fatalf("disabled report contains communities:\n%s", without)
	}
}
