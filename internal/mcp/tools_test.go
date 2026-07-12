package mcp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/12ya/reporavel/internal/graph"
	"github.com/12ya/reporavel/internal/query"
)

func TestKnownToolsRejectInvalidArgumentsBeforeGraphAccess(t *testing.T) {
	server := &server{graphs: newGraphCache(t.TempDir())}
	tests := []struct {
		name string
		tool string
		args string
	}{
		{name: "unknown argument", tool: "query", args: `{"query":"x","extra":true}`},
		{name: "unbounded query", tool: "query", args: `{"query":"x","limit":0}`},
		{name: "null boolean", tool: "context", args: `{"question":"x","infer_relations":null}`},
		{name: "small budget", tool: "context", args: `{"question":"x","token_budget":127}`},
		{name: "negative branch fanout", tool: "context", args: `{"question":"x","branch_fanout":-1}`},
		{name: "excessive branch fanout", tool: "affected", args: `{"target":"x","branch_fanout":10001}`},
		{name: "duplicate relation", tool: "affected", args: `{"target":"x","relations":["calls","calls"]}`},
		{name: "wrong path type", tool: "path", args: `{"from":1,"to":"x"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := server.callTool(test.tool, json.RawMessage(test.args))
			if !result.IsError || result.StructuredContent == nil || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "invalid_arguments") {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestRetrievalOptionsAcceptBranchFanoutOverride(t *testing.T) {
	args := map[string]json.RawMessage{"branch_fanout": json.RawMessage("37")}
	options, _, err := retrievalOptions(args, "both", false)
	if err != nil {
		t.Fatal(err)
	}
	if options.BranchFanout != 37 {
		t.Fatalf("BranchFanout = %d, want 37", options.BranchFanout)
	}

	for _, tool := range toolDefinitions() {
		if tool.Name != "context" && tool.Name != "affected" {
			continue
		}
		properties, ok := tool.InputSchema["properties"].(map[string]any)
		if !ok || properties["branch_fanout"] == nil {
			t.Fatalf("%s schema omits branch_fanout: %#v", tool.Name, tool.InputSchema)
		}
	}
}

func TestContextSchemaMatchesRuntimeDirectionDefault(t *testing.T) {
	for _, tool := range toolDefinitions() {
		if tool.Name != "context" {
			continue
		}
		properties := tool.InputSchema["properties"].(map[string]any)
		direction := properties["direction"].(map[string]any)
		if direction["default"] != string(query.DirectionBoth) {
			t.Fatalf("context direction schema default = %#v, want %q", direction["default"], query.DirectionBoth)
		}
		return
	}
	t.Fatal("context tool definition not found")
}

func TestTargetToolsReportAmbiguityAndPathFallbackHonestly(t *testing.T) {
	outDir := t.TempDir()
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "a1", Kind: graph.NodeFunction, Name: "A"},
			{ID: "a2", Kind: graph.NodeFunction, Name: "A"},
			{ID: "b", Kind: graph.NodeFunction, Name: "B"},
		},
		Edges: []graph.Edge{{ID: "calls://b-a1", Kind: graph.EdgeCalls, From: "b", To: "a1"}},
	}
	writeTestGraph(t, filepath.Join(outDir, "graph.json"), g)
	server := &server{graphs: newGraphCache(outDir)}
	for _, test := range []struct {
		name string
		tool string
		args string
	}{
		{name: "explain", tool: "explain", args: `{"target":"A"}`},
		{name: "affected", tool: "affected", args: `{"target":"A"}`},
		{name: "path", tool: "path", args: `{"from":"A","to":"B"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := server.callTool(test.tool, json.RawMessage(test.args))
			if !result.IsError || !strings.Contains(result.Content[0].Text, "target_ambiguous") || !strings.Contains(result.Content[0].Text, "a1") || !strings.Contains(result.Content[0].Text, "a2") {
				t.Fatalf("ambiguous %s result = %#v", test.tool, result)
			}
		})
	}

	result := server.callTool("path", json.RawMessage(`{"from":"a1","to":"B"}`))
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("path fallback result = %#v", result)
	}
	for _, want := range []string{"mode=undirected_fallback", "direction=reverse", "graph=b->a1"} {
		if !strings.Contains(result.Content[0].Text, want) {
			t.Fatalf("path fallback missing %q:\n%s", want, result.Content[0].Text)
		}
	}
}

func TestBoundTextPreservesUTF8AndBudget(t *testing.T) {
	input := strings.Repeat("한", 1_000)
	result := boundText(input, minimumTokenBudget)
	if !strings.Contains(result, "TRUNCATED") || len(result) > minimumTokenBudget*3 {
		t.Fatalf("bounded text bytes = %d, value = %q", len(result), result)
	}
	if !utf8.ValidString(result) {
		t.Fatal("bounded text split a UTF-8 sequence")
	}
}
