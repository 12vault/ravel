package evaluation

import (
	"testing"

	"github.com/12ya/reporavel/internal/graph"
)

func TestRunScoresRetrievalCases(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{
		{ID: "domain://billing", Kind: graph.NodeDomain, Name: "Billing payments"},
		{ID: "domain://identity", Kind: graph.NodeDomain, Name: "User identity"},
	}}
	report, err := Run(g, []Case{{ID: "q1", Dataset: "LOCOMO", Question: "billing payments", ExpectedNodeIDs: []string{"domain://billing"}}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if report.Overall.MeanRecall != 1 || report.Overall.MeanReciprocalRank != 1 {
		t.Fatalf("unexpected metrics: %#v", report.Overall)
	}
}

func TestRunRejectsInvalidTopK(t *testing.T) {
	if _, err := Run(graph.Graph{}, nil, 0); err == nil {
		t.Fatal("expected top-k error")
	}
}
