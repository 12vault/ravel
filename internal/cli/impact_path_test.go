package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/query"
	"github.com/12vault/ravel/internal/store"
)

func writeQueryCommandGraph(t *testing.T, g graph.Graph) string {
	t.Helper()
	outDir := t.TempDir()
	if err := store.WriteJSON(filepath.Join(outDir, "graph.json"), g); err != nil {
		t.Fatal(err)
	}
	return outDir
}

func TestExecuteExplainAndAffectedRejectAmbiguousExactNames(t *testing.T) {
	outDir := writeQueryCommandGraph(t, graph.Graph{Nodes: []graph.Node{
		{ID: "go://a.Handler", Kind: graph.NodeFunction, Name: "Handler"},
		{ID: "go://b.Handler", Kind: graph.NodeFunction, Name: "Handler"},
	}})
	commands := []struct {
		name string
		args []string
	}{
		{name: "explain", args: []string{"explain", "--out", outDir, "Handler"}},
		{name: "affected", args: []string{"affected", "--out", outDir, "Handler"}},
		{name: "path", args: []string{"path", "--out", outDir, "Handler", "go://a.Handler"}},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Execute(context.Background(), command.args, &stdout, &stderr)
			if err == nil || !query.IsTargetError(err, query.TargetAmbiguous) {
				t.Fatalf("%s ambiguity error = %v", command.name, err)
			}
			for _, want := range []string{"go://a.Handler", "go://b.Handler", "use an exact node ID"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("%s ambiguity error missing %q: %v", command.name, want, err)
				}
			}
		})
	}
}

func TestExecutePathReportsUndirectedFallbackMetadata(t *testing.T) {
	outDir := writeQueryCommandGraph(t, graph.Graph{
		Nodes: []graph.Node{{ID: "a", Kind: graph.NodeFunction, Name: "A"}, {ID: "b", Kind: graph.NodeFunction, Name: "B"}},
		Edges: []graph.Edge{{ID: "calls://b-a", Kind: graph.EdgeCalls, From: "b", To: "a"}},
	})
	var stdout, stderr bytes.Buffer
	if err := Execute(context.Background(), []string{"path", "--out", outDir, "A", "B"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mode=undirected_fallback", "direction=reverse", "graph=b->a"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("path output missing %q:\n%s", want, stdout.String())
		}
	}

	stdout.Reset()
	if err := Execute(context.Background(), []string{"path", "--out", outDir, "--json", "A", "B"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var result query.PathResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Mode != query.PathUndirectedFallback || len(result.Hops) != 1 || result.Hops[0].Direction != query.PathHopReverse {
		t.Fatalf("path JSON = %#v", result)
	}
}
