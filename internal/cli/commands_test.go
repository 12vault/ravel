package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func TestWriteInstallSuccess(t *testing.T) {
	var output bytes.Buffer
	writeInstallSuccess(&output, "/tmp/ravel/SKILL.md", false)

	for _, want := range []string{
		"●────────╮",
		"● · · · · · ●",
		"█▀▀▄  ▄▀▀▄",
		"skill installed  →  /tmp/ravel/SKILL.md",
		"local CLI ready  →  ravel",
		"network access   →  none",
		"/ravel understand",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("install output missing %q:\n%s", want, output.String())
		}
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("plain install output contains ANSI escapes: %q", output.String())
	}
}

func TestWriteInstallSuccessUsesBrandColors(t *testing.T) {
	var output bytes.Buffer
	writeInstallSuccess(&output, "/tmp/ravel/SKILL.md", true)

	if !strings.Contains(output.String(), "\x1b[38;2;0;194;199m") {
		t.Fatal("colored install output is missing Ravel cyan")
	}
	if !strings.Contains(output.String(), "\x1b[38;2;255;92;77m╲") ||
		!strings.Contains(output.String(), "\x1b[38;2;255;92;77m●") {
		t.Fatal("colored install output is missing the coral diagonal or endpoint")
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
		if args[0] != "benchmark" && !strings.Contains(stdout.String(), "--candidate-shortlist") {
			t.Fatalf("context help omits candidate shortlist profile: %q", stdout.String())
		}
	}
}

func TestBuildAndUpdateExposeAndValidateJobs(t *testing.T) {
	for _, command := range []string{"build", "update"} {
		t.Run(command+" help", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := Execute(context.Background(), []string{command, "--help"}, &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stdout.String(), "--jobs <n>") {
				t.Fatalf("%s help omits --jobs: %q", command, stdout.String())
			}
		})
		t.Run(command+" invalid jobs", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Execute(context.Background(), []string{command, "--jobs", "0"}, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), "--jobs must be between 1 and 256") {
				t.Fatalf("%s invalid jobs error = %v", command, err)
			}
		})
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

func TestExecuteDashboardHonorsDisabledCommunityConfig(t *testing.T) {
	root := t.TempDir()
	graphDir := filepath.Join(root, "graph")
	if err := os.MkdirAll(graphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	g := graph.Graph{Nodes: []graph.Node{{ID: "a", Meta: map[string]string{"community": "stale"}}}}
	if err := store.WriteJSON(filepath.Join(graphDir, "graph.json"), g); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "ravel.yaml")
	configData := "output:\n  dir: \"" + graphDir + "\"\n  communityClustering: false\n"
	if err := os.WriteFile(configPath, []byte(configData), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Execute(context.Background(), []string{"dashboard", "--config", configPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(graphDir, "graph.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"community":"`) {
		t.Fatal("dashboard ignored disabled community config")
	}
}

func TestExecuteCommunityTemplateAndDescriptionImport(t *testing.T) {
	out := t.TempDir()
	g := graph.Graph{Nodes: []graph.Node{{ID: "a", Kind: graph.NodeFile, Name: "a.go", Path: "internal/query/a.go"}}}
	if err := store.WriteJSON(filepath.Join(out, "graph.json"), g); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Execute(context.Background(), []string{"community", "--out", out, "--template"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var template struct {
		Descriptions []struct {
			Community string `json:"community"`
		} `json:"descriptions"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &template); err != nil {
		t.Fatalf("template JSON: %v\n%s", err, stdout.String())
	}
	if len(template.Descriptions) != 1 || template.Descriptions[0].Community == "" {
		t.Fatalf("template = %#v", template)
	}
	descriptionPath := filepath.Join(t.TempDir(), "descriptions.json")
	descriptionJSON := fmt.Sprintf(`{"version":1,"source":"test-ai","descriptions":[{"community":%q,"description":"Handles graph queries.","rationale":"The community is dominated by query files."}]}`, template.Descriptions[0].Community)
	if err := os.WriteFile(descriptionPath, []byte(descriptionJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := Execute(context.Background(), []string{"community", "describe", descriptionPath, "--out", out}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	stored, err := store.LoadGraph(out)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Nodes[0].Meta["communityDescription"] != "Handles graph queries." || stored.Nodes[0].Meta["communityName"] != "internal/query" {
		t.Fatalf("described node = %#v", stored.Nodes[0])
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
  communityBoost: true
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
	if report.TopK != 7 || options.MaxNodes != 7 || options.BranchFanout != 9 || options.Traversal != query.TraversalDFS || options.Direction != query.DirectionIn || !options.DisableRelationInference || options.TokenBudget != 512 || !options.CommunityBoost || report.GraphSHA256 == "" || report.GraphRevision != "unspecified" {
		t.Fatalf("benchmark config not propagated: report = %#v", report)
	}
}
