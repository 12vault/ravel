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

func TestMarkdownPreparedUsesExistingCommunityMetadata(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{{
		ID:   "a",
		Kind: graph.NodeFile,
		Name: "a.go",
		Meta: map[string]string{
			community.MetaKey:             "c-prepared",
			community.MetaNameKey:         "Prepared community",
			community.MetaLabelKey:        "Prepared label",
			community.MetaSizeKey:         "1",
			community.MetaGranularityKey:  "balanced",
			community.MetaHubThresholdKey: "0",
		},
	}}}
	markdown := MarkdownPrepared(g, true)
	if !strings.Contains(markdown, "Prepared label") || !strings.Contains(markdown, "c-prepared") {
		t.Fatalf("prepared community metadata was not rendered:\n%s", markdown)
	}
}

func TestMarkdownReportsImportCyclesAndKeyCallFlows(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "file://a.ts", Kind: graph.NodeFile, Name: "a.ts", Path: "a.ts"},
			{ID: "file://b.ts", Kind: graph.NodeFile, Name: "b.ts", Path: "b.ts"},
			{ID: "main", Kind: graph.NodeFunction, Name: "main", Path: "a.ts"},
			{ID: "load", Kind: graph.NodeFunction, Name: "load", Path: "a.ts"},
			{ID: "store", Kind: graph.NodeFunction, Name: "store", Path: "b.ts"},
		},
		Edges: []graph.Edge{
			{Kind: graph.EdgeImports, From: "file://a.ts", To: "file://b.ts", Meta: map[string]string{"resolved": "true"}},
			{Kind: graph.EdgeImports, From: "file://b.ts", To: "file://a.ts", Meta: map[string]string{"resolved": "true"}},
			{Kind: graph.EdgeCalls, From: "main", To: "load", Meta: map[string]string{"resolved": "true"}},
			{Kind: graph.EdgeCalls, From: "load", To: "store", Meta: map[string]string{"resolved": "true"}},
		},
	}
	markdown := MarkdownConfigured(g, false)
	if !strings.Contains(markdown, "## Import Cycles") || !strings.Contains(markdown, "`a.ts` → `b.ts` → `a.ts`") {
		t.Fatalf("missing concrete import cycle:\n%s", markdown)
	}
	if !strings.Contains(markdown, "## Key Call Flows") || !strings.Contains(markdown, "`a.ts:main` → `a.ts:load` → `b.ts:store`") {
		t.Fatalf("missing key call flow:\n%s", markdown)
	}
}

func TestArchitectureReportSkipsUnresolvedAndExternalRelationships(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "file://a.ts", Kind: graph.NodeFile, Name: "a.ts", Path: "a.ts"},
			{ID: "external", Kind: graph.NodeImport, Name: "external"},
			{ID: "run", Kind: graph.NodeFunction, Name: "run", Path: "a.ts"},
			{ID: "missing", Kind: graph.NodeFunction, Name: "missing", Path: "a.ts", Meta: map[string]string{"resolved": "false"}},
		},
		Edges: []graph.Edge{
			{Kind: graph.EdgeImports, From: "file://a.ts", To: "external", Meta: map[string]string{"resolved": "false"}},
			{Kind: graph.EdgeCalls, From: "run", To: "missing", Meta: map[string]string{"resolved": "false"}},
		},
	}
	markdown := MarkdownConfigured(g, false)
	if strings.Count(markdown, "- None detected") != 2 {
		t.Fatalf("unresolved relationships leaked into architecture sections:\n%s", markdown)
	}
}
