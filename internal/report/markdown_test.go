package report

import (
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/community"
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

func TestMarkdownEscapesOptionalCommunityDescription(t *testing.T) {
	g := community.Assign(graph.Graph{Nodes: []graph.Node{{ID: "a", Name: "A"}}})
	id := g.Nodes[0].Meta[community.MetaKey]
	described, err := community.ApplyDescriptions(g, community.DescriptionFile{Version: 1, Source: "test-ai", Descriptions: []community.Description{{Community: id, Text: "Handles *requests* <safely>.", Rationale: "Graph facts."}}})
	if err != nil {
		t.Fatal(err)
	}
	markdown := Markdown(described)
	if !strings.Contains(markdown, `Handles \*requests\* &lt;safely&gt;.`) {
		t.Fatalf("description was not safely rendered:\n%s", markdown)
	}
}
