package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/12vault/ravel/internal/evaluation"
	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/query"
	"github.com/12vault/ravel/internal/scan"
	"github.com/12vault/ravel/internal/selfupdate"
	"github.com/12vault/ravel/internal/store"
)

func TestExecutePrintsVersion(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if err := Execute(context.Background(), args, &stdout, &stderr); err != nil {
			t.Fatalf("Execute(%v) error = %v", args, err)
		}
		want := "ravel " + Version + "\n"
		if stdout.String() != want {
			t.Fatalf("Execute(%v) output = %q, want %q", args, stdout.String(), want)
		}
		if stderr.Len() != 0 {
			t.Fatalf("Execute(%v) stderr = %q, want empty", args, stderr.String())
		}
	}
}

func TestExecuteUpdateCheckReportsAvailableReleaseAndJSON(t *testing.T) {
	previous := checkForUpdate
	checkForUpdate = func(ctx context.Context, options selfupdate.CheckOptions) (selfupdate.CheckResult, error) {
		if options.CurrentVersion != Version || options.Repository != "12vault/ravel" {
			t.Fatalf("check options = %#v", options)
		}
		return selfupdate.CheckResult{
			CurrentVersion:  "v0.2.0",
			LatestVersion:   "v0.3.0",
			UpdateAvailable: true,
			ReleaseURL:      "https://github.com/12vault/ravel/releases/tag/v0.3.0",
		}, nil
	}
	t.Cleanup(func() { checkForUpdate = previous })

	var stdout, stderr bytes.Buffer
	if err := Execute(context.Background(), []string{"update-check"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Ravel v0.3.0 is available", "Run: ravel self-update", "releases/tag/v0.3.0"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("update-check output missing %q: %s", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}

	stdout.Reset()
	if err := Execute(context.Background(), []string{"update-check", "--json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var result selfupdate.CheckResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.UpdateAvailable || result.LatestVersion != "v0.3.0" {
		t.Fatalf("JSON result = %#v", result)
	}
}

func TestExecutePrintsDiscoverableSubcommandHelp(t *testing.T) {
	for _, args := range [][]string{{"context", "--help"}, {"help", "context"}, {"benchmark", "-h"}} {
		var stdout, stderr bytes.Buffer
		if err := Execute(context.Background(), args, &stdout, &stderr); err != nil {
			t.Fatalf("Execute(%v) error = %v", args, err)
		}
		if !strings.Contains(stdout.String(), "Usage:") {
			t.Fatalf("Execute(%v) help = %q", args, stdout.String())
		}
		if args[0] != "benchmark" && !strings.Contains(stdout.String(), "--infer-relations") {
			t.Fatalf("context help omits retrieval controls: %q", stdout.String())
		}
		if !strings.Contains(stdout.String(), "--branch-fanout") {
			t.Fatalf("retrieval help omits branch fanout: %q", stdout.String())
		}
	}
}

func TestSameScanUsesPathsAndHashes(t *testing.T) {
	before := scan.Result{Files: []scan.File{{Path: "a.go", Hash: "one"}}}
	after := scan.Result{Files: []scan.File{{Path: "a.go", Hash: "one", ModTime: time.Now()}}}
	if !sameScan(before, after) {
		t.Fatal("modification time alone should not trigger update")
	}
	after.Files[0].Hash = "two"
	if sameScan(before, after) {
		t.Fatal("changed hash should trigger update")
	}
}

func TestExecuteContextReturnsConnectedBudgetedGraph(t *testing.T) {
	out := t.TempDir()
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "function://checkout", Kind: graph.NodeFunction, Name: "Checkout", Path: "checkout.go", StartLine: 10},
			{ID: "function://charge", Kind: graph.NodeFunction, Name: "ChargeCard", Path: "payments.go", StartLine: 20},
		},
		Edges: []graph.Edge{{ID: "calls://checkout-charge", Kind: graph.EdgeCalls, From: "function://checkout", To: "function://charge", Meta: map[string]string{"confidence": "extracted", "path": "checkout.go", "line": "12"}}},
	}
	if err := store.WriteJSON(filepath.Join(out, "graph.json"), g); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{"context", "checkout calls", "--out", out, "--relations", "calls", "--token-budget", "256"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "SEED") || !strings.Contains(stdout.String(), "EDGE") || !strings.Contains(stdout.String(), "ChargeCard") {
		t.Fatalf("context output = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}

	stdout.Reset()
	err = Execute(context.Background(), []string{"context", "checkout calls", "--out", out, "--json", "--branch-fanout", "7", "--token-budget", "256"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	var result query.Retrieval
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("JSON output: %v\n%s", err, stdout.String())
	}
	if len(result.Nodes) < 2 || len(result.Edges) != 1 || result.Stats.TokenBudget != 256 || result.Stats.BranchFanout != 7 {
		t.Fatalf("retrieval = %#v", result)
	}
}

func TestExecuteContextRejectsMissingExplicitConfig(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{"context", "question", "--config", missing}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("explicit missing config error = %v", err)
	}
}

func TestExecuteBenchmarkUsesSharedRetrievalConfig(t *testing.T) {
	root := t.TempDir()
	graphDir := filepath.Join(root, "graph")
	if err := os.MkdirAll(graphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	g := graph.Graph{Nodes: []graph.Node{{ID: "function://checkout", Kind: graph.NodeFunction, Name: "Checkout"}}}
	if err := store.WriteJSON(filepath.Join(graphDir, "graph.json"), g); err != nil {
		t.Fatal(err)
	}
	dataset := filepath.Join(root, "cases.jsonl")
	if err := os.WriteFile(dataset, []byte(`{"id":"q1","dataset":"repository-questions","question":"checkout","expectedNodeIds":["function://checkout"]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "ravel.yaml")
	configData := `retrieval:
  traversal: dfs
  direction: in
  inferRelations: false
  seedLimit: 2
  maxDepth: 3
  maxNodes: 7
  branchFanout: 9
  hubDegreeThreshold: -1
  tokenBudget: 512
`
	if err := os.WriteFile(configPath, []byte(configData), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{"benchmark", "--config", configPath, "--graph", graphDir, "--dataset", dataset}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	var report evaluation.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("benchmark JSON: %v\n%s", err, stdout.String())
	}
	options := report.RetrievalOptions
	if report.TopK != 7 || options.MaxNodes != 7 || options.BranchFanout != 9 || options.Traversal != query.TraversalDFS || options.Direction != query.DirectionIn || !options.DisableRelationInference || options.TokenBudget != 512 || report.GraphSHA256 == "" || report.GraphRevision != "unspecified" {
		t.Fatalf("benchmark config not propagated: report = %#v", report)
	}
}
