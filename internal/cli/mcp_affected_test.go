package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/12ya/reporavel/internal/graph"
	"github.com/12ya/reporavel/internal/query"
	"github.com/12ya/reporavel/internal/store"
)

func TestExecuteIOStartsMCPWithInjectedStdin(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	input := append([]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))), body...)
	var stdout, stderr bytes.Buffer
	if err := ExecuteIO(context.Background(), []string{"mcp", "--out", t.TempDir()}, bytes.NewReader(input), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "Content-Length:") || !strings.Contains(stdout.String(), `"result":{}`) {
		t.Fatalf("MCP stdout = %q", stdout.String())
	}
}

func TestExecuteAffectedReturnsReverseImpact(t *testing.T) {
	outDir := t.TempDir()
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "caller", Kind: graph.NodeFunction, Name: "Checkout"},
			{ID: "target", Kind: graph.NodeFunction, Name: "ChargeCard"},
			{ID: "callee", Kind: graph.NodeFunction, Name: "Gateway"},
		},
		Edges: []graph.Edge{
			{ID: "calls://caller-target", Kind: graph.EdgeCalls, From: "caller", To: "target"},
			{ID: "calls://target-callee", Kind: graph.EdgeCalls, From: "target", To: "callee"},
		},
	}
	if err := store.WriteJSON(filepath.Join(outDir, "graph.json"), g); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Execute(context.Background(), []string{"affected", "--out", outDir, "--relations", "calls", "ChargeCard"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "RAVEL_AFFECTED") || !strings.Contains(stdout.String(), "Checkout") || strings.Contains(stdout.String(), "Gateway") {
		t.Fatalf("affected output = %q", stdout.String())
	}

	stdout.Reset()
	if err := Execute(context.Background(), []string{"affected", "--out", outDir, "--relations", "calls", "--branch-fanout", "6", "--json", "ChargeCard"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var result query.Retrieval
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("affected JSON: %v\n%s", err, stdout.String())
	}
	if result.Stats.Direction != query.DirectionIn || result.Stats.BranchFanout != 6 || len(result.Stats.SeedIDs) != 1 || result.Stats.SeedIDs[0] != "target" {
		t.Fatalf("affected result = %#v", result)
	}
}

func TestHelpListsMCPAndAffected(t *testing.T) {
	for _, args := range [][]string{{"mcp", "--help"}, {"affected", "--help"}} {
		var stdout, stderr bytes.Buffer
		if err := Execute(context.Background(), args, &stdout, &stderr); err != nil {
			t.Fatalf("Execute(%v): %v", args, err)
		}
		if !strings.Contains(stdout.String(), "Usage: ravel "+args[0]) {
			t.Fatalf("help for %s = %q", args[0], stdout.String())
		}
		if args[0] == "affected" && !strings.Contains(stdout.String(), "--branch-fanout") {
			t.Fatalf("affected help omits branch fanout: %q", stdout.String())
		}
	}
}
