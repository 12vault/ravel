package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadHonorsAnalysisAndOutputSettings(t *testing.T) {
	path := writeConfig(t, `version: 1
mode: offline
analysis:
  go: false
  polyglot: false
  callGraph: false
  typeResolution: false
  jobs: 2
output:
  dir: "custom-output"
  json: false
  sqlite: false
  markdownReport: true
  communityClustering: true
  communityGranularity: fine
  communityHubDegreeThreshold: 42
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Analysis.Go {
		t.Fatal("Analysis.Go = true, want false")
	}
	if cfg.Analysis.Polyglot {
		t.Fatal("Analysis.Polyglot = true, want false")
	}
	if cfg.Analysis.CallGraph {
		t.Fatal("Analysis.CallGraph = true, want false")
	}
	if cfg.Analysis.Jobs != 2 {
		t.Fatalf("Analysis.Jobs = %d, want 2", cfg.Analysis.Jobs)
	}
	if cfg.Output.Dir != "custom-output" {
		t.Fatalf("Output.Dir = %q, want custom-output", cfg.Output.Dir)
	}
	if cfg.Output.JSON {
		t.Fatal("Output.JSON = true, want false")
	}
	if !cfg.Output.MarkdownReport {
		t.Fatal("Output.MarkdownReport = false, want true")
	}
	if cfg.Output.CommunityGranularity != "fine" || cfg.Output.CommunityHubDegreeThreshold != 42 {
		t.Fatalf("community output config = %#v", cfg.Output)
	}
}

func TestDefaultYAMLLoads(t *testing.T) {
	path := writeConfig(t, DefaultYAML())
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load(DefaultYAML()) error = %v", err)
	}
	want := Default()
	if got != want {
		t.Fatalf("Load(DefaultYAML()) = %#v, want %#v", got, want)
	}
}

func TestLoadRequiredRejectsMissingExplicitPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	if _, err := LoadRequired(path); err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("LoadRequired() error = %v, want missing-config error", err)
	}
	if got, err := Load(path); err != nil || got != Default() {
		t.Fatalf("Load() implicit default = %#v, %v", got, err)
	}
}

func TestLoadHonorsRetrievalSettings(t *testing.T) {
	path := writeConfig(t, `retrieval:
  traversal: dfs
  direction: in
  inferRelations: false
  relations: calls,references
  seedLimit: 5
  maxDepth: 4
  maxNodes: 250
  branchFanout: 32
  hubDegreeThreshold: 75
  tokenBudget: 4096
  communityBoost: true
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := RetrievalConfig{Traversal: "dfs", Direction: "in", InferRelations: false, Relations: "calls,references", SeedLimit: 5, MaxDepth: 4, MaxNodes: 250, BranchFanout: 32, HubDegreeThreshold: 75, TokenBudget: 4096, CommunityBoost: true}
	if cfg.Retrieval != want {
		t.Fatalf("Retrieval = %#v, want %#v", cfg.Retrieval, want)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "unknown setting", content: "output:\n  mystery: true\n", want: `unknown setting "output.mystery"`},
		{name: "unknown section", content: "database:\n  enabled: true\n", want: `unknown section "database"`},
		{name: "invalid boolean", content: "analysis:\n  go: maybe\n", want: "expected true or false"},
		{name: "invalid integer", content: "scan:\n  maxFileSize: large\n", want: "expected an integer"},
		{name: "invalid analysis jobs", content: "analysis:\n  jobs: 0\n", want: "analysis.jobs must be between 1 and 256"},
		{name: "invalid traversal", content: "retrieval:\n  traversal: sideways\n", want: "retrieval.traversal must be bfs or dfs"},
		{name: "invalid direction", content: "retrieval:\n  direction: around\n", want: "retrieval.direction must be out, in, or both"},
		{name: "invalid branch fanout", content: "retrieval:\n  branchFanout: -1\n", want: "retrieval.branchFanout must be 0 (automatic) or between"},
		{name: "invalid retrieval budget", content: "retrieval:\n  tokenBudget: 12\n", want: "retrieval.tokenBudget must be between"},
		{name: "invalid community granularity", content: "output:\n  communityGranularity: microscopic\n", want: "output.communityGranularity must be coarse, balanced, or fine"},
		{name: "invalid community hub threshold", content: "output:\n  communityHubDegreeThreshold: -2\n", want: "output.communityHubDegreeThreshold must be -1, 0, or positive"},
		{name: "unsafe permission", content: "permissions:\n  network: true\n", want: "permissions.network cannot be enabled"},
		{name: "type resolution unavailable", content: "analysis:\n  typeResolution: true\n", want: "analysis.typeResolution is not implemented"},
		{name: "sqlite unavailable", content: "output:\n  sqlite: true\n", want: "output.sqlite is not implemented"},
		{name: "no output", content: "output:\n  json: false\n  markdownReport: false\n", want: "at least one output format must be enabled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.content))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".reporavel.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
