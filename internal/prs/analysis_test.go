package prs

import (
	"bytes"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/community"
	"github.com/12vault/ravel/internal/graph"
)

func TestAnalyzeMapsImpactAndDetectsFileAndCommunityConflicts(t *testing.T) {
	meta := func(id, name string) map[string]string {
		return map[string]string{community.MetaKey: id, community.MetaLabelKey: name}
	}
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: graph.FileID("auth/a.go"), Kind: graph.NodeFile, Name: "a.go", Path: "auth/a.go", Meta: meta("c-auth", "Authentication")},
			{ID: graph.FileID("auth/b.go"), Kind: graph.NodeFile, Name: "b.go", Path: "auth/b.go", Meta: meta("c-auth", "Authentication")},
			{ID: graph.FileID("data/c.go"), Kind: graph.NodeFile, Name: "c.go", Path: "data/c.go", Meta: meta("c-data", "Data")},
			{ID: "fa", Kind: graph.NodeFunction, Name: "A", Path: "auth/a.go", Meta: meta("c-auth", "Authentication")},
			{ID: "caller", Kind: graph.NodeFunction, Name: "Caller", Path: "data/c.go", Meta: meta("c-data", "Data")},
		},
		Edges: []graph.Edge{
			{Kind: graph.EdgeDefines, From: graph.FileID("auth/a.go"), To: "fa", Meta: map[string]string{"confidence": "extracted", "evidence": "auth/a.go:1"}},
			{Kind: graph.EdgeCalls, From: "caller", To: "fa", Meta: map[string]string{"confidence": "inferred", "resolved": "true", "rationale": "test"}},
		},
	}
	analysis := Analyze(g, []PullRequest{
		{Number: 3, Title: "same file", Files: []File{{Path: "auth/a.go"}}, StatusCheckRollup: []Check{{Conclusion: "FAILURE"}}},
		{Number: 1, Title: "auth A", Files: []File{{Path: "./auth/a.go"}}, StatusCheckRollup: []Check{{Conclusion: "SUCCESS"}}},
		{Number: 2, Title: "auth B", Files: []File{{Path: "auth/b.go"}}, StatusCheckRollup: []Check{{Status: "IN_PROGRESS"}}},
	})
	if len(analysis.PullRequests) != 3 || analysis.PullRequests[0].Number != 1 {
		t.Fatalf("PR results are not sorted: %#v", analysis.PullRequests)
	}
	first := analysis.PullRequests[0]
	if first.CI != "ci=passing" || len(first.MappedFiles) != 1 || len(first.AffectedNodeIDs) < 2 {
		t.Fatalf("first PR impact = %#v", first)
	}
	if analysis.PullRequests[1].CI != "ci=pending" || analysis.PullRequests[2].CI != "ci=failing" {
		t.Fatalf("CI summaries = %#v", analysis.PullRequests)
	}
	find := func(left, right int) *Conflict {
		for i := range analysis.Conflicts {
			if analysis.Conflicts[i].LeftPR == left && analysis.Conflicts[i].RightPR == right {
				return &analysis.Conflicts[i]
			}
		}
		return nil
	}
	if conflict := find(1, 2); conflict == nil || conflict.Risk != "community" || len(conflict.SharedFiles) != 0 || !strings.Contains(strings.Join(conflict.SharedCommunities, ","), "Authentication") {
		t.Fatalf("community conflict = %#v", conflict)
	}
	if conflict := find(1, 3); conflict == nil || conflict.Risk != "file" || len(conflict.SharedFiles) != 1 {
		t.Fatalf("file conflict = %#v", conflict)
	}

	var output bytes.Buffer
	if err := WriteText(&output, analysis, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "#1 auth A [ci=passing]") || !strings.Contains(output.String(), "#1 ↔ #3 [file overlap]") {
		t.Fatalf("text output:\n%s", output.String())
	}
}

func TestAnalyzeLeavesUnknownFilesUnmapped(t *testing.T) {
	analysis := Analyze(graph.Graph{}, []PullRequest{{Number: 1, Title: "unknown", Files: []File{{Path: "missing.go"}}}})
	if len(analysis.PullRequests) != 1 || len(analysis.PullRequests[0].ChangedFiles) != 1 || len(analysis.PullRequests[0].MappedFiles) != 0 {
		t.Fatalf("unknown file analysis = %#v", analysis)
	}
	if len(analysis.Conflicts) != 0 {
		t.Fatalf("unknown files created conflicts: %#v", analysis.Conflicts)
	}
}
