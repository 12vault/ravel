package orchestrate

import (
	"testing"

	"github.com/12vault/ravel/internal/graph"
)

func TestUnderstandPlanHasDependencyWaves(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{
		{ID: "file://a.py", Kind: graph.NodeFile, Path: "a.py", Meta: map[string]string{"language": "python"}},
		{ID: "file://b.py", Kind: graph.NodeFile, Path: "b.py", Meta: map[string]string{"language": "python"}},
	}}
	plan, err := Build("understand", g, nil, 1, ".reporavel")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tasks) != 6 {
		t.Fatalf("tasks = %d, want 6", len(plan.Tasks))
	}
	if plan.Tasks[3].Role != "architecture-analyzer" || len(plan.Tasks[3].DependsOn) != 3 {
		t.Fatalf("unexpected architecture task: %#v", plan.Tasks[3])
	}
}

func TestCorpusPlansFilterFiles(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{
		{ID: "file://a.md", Kind: graph.NodeFile, Path: "a.md", Meta: map[string]string{"language": "markdown"}},
		{ID: "file://a.pdf", Kind: graph.NodeFile, Path: "a.pdf", Meta: map[string]string{"language": "pdf"}},
	}}
	plan, err := Build("pdf", g, nil, 20, ".reporavel")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tasks[0].SourcePaths) != 1 || plan.Tasks[0].SourcePaths[0] != "a.pdf" {
		t.Fatalf("unexpected PDF paths: %#v", plan.Tasks[0].SourcePaths)
	}
}
