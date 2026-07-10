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
  callGraph: false
  typeResolution: false
output:
  dir: "custom-output"
  json: false
  sqlite: false
  markdownReport: true
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Analysis.Go {
		t.Fatal("Analysis.Go = true, want false")
	}
	if cfg.Analysis.CallGraph {
		t.Fatal("Analysis.CallGraph = true, want false")
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
