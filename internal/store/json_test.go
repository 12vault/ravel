package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/12ya/reporavel/internal/config"
	"github.com/12ya/reporavel/internal/graph"
	"github.com/12ya/reporavel/internal/scan"
)

func TestWriteArtifactsHonorsOutputSettings(t *testing.T) {
	t.Run("markdown only", func(t *testing.T) {
		outDir := t.TempDir()
		for _, name := range []string{"graph.json", "files.json", "symbols.json", "index.db"} {
			if err := os.WriteFile(filepath.Join(outDir, name), []byte("stale"), 0644); err != nil {
				t.Fatalf("write stale artifact: %v", err)
			}
		}
		output := config.Default().Output
		output.JSON = false

		if err := WriteArtifacts(outDir, graph.Graph{}, scan.Result{}, "# Report\n", output); err != nil {
			t.Fatalf("WriteArtifacts() error = %v", err)
		}
		assertExists(t, outDir, "report.md", true)
		for _, name := range []string{"graph.json", "files.json", "symbols.json", "index.db"} {
			assertExists(t, outDir, name, false)
		}
		assertExists(t, filepath.Join(outDir, stateDir), "graph.json", true)
		assertExists(t, filepath.Join(outDir, stateDir), "files.json", true)
		if _, err := LoadGraph(outDir); err != nil {
			t.Fatalf("LoadGraph() after markdown-only write: %v", err)
		}
		if _, err := LoadScan(outDir); err != nil {
			t.Fatalf("LoadScan() after markdown-only write: %v", err)
		}
	})

	t.Run("json only", func(t *testing.T) {
		outDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(outDir, "report.md"), []byte("stale"), 0644); err != nil {
			t.Fatalf("write stale report: %v", err)
		}
		output := config.Default().Output
		output.MarkdownReport = false

		if err := WriteArtifacts(outDir, graph.Graph{}, scan.Result{}, "# Report\n", output); err != nil {
			t.Fatalf("WriteArtifacts() error = %v", err)
		}
		for _, name := range []string{"graph.json", "files.json", "symbols.json"} {
			assertExists(t, outDir, name, true)
		}
		assertExists(t, outDir, "report.md", false)
		assertExists(t, outDir, "index.db", false)
	})
}

func assertExists(t *testing.T, dir, name string, want bool) {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, name))
	if want && err != nil {
		t.Fatalf("%s should exist: %v", name, err)
	}
	if !want && !os.IsNotExist(err) {
		t.Fatalf("%s should not exist", name)
	}
}
