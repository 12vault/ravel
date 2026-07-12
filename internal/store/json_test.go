package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/config"
	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/scan"
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

func TestWriteArtifactsAddsCommunityMetadata(t *testing.T) {
	outDir := t.TempDir()
	g := graph.Graph{Nodes: []graph.Node{{ID: "file://a", Kind: graph.NodeFile, Name: "a"}}}
	if err := WriteArtifacts(outDir, g, scan.Result{}, "# Report\n", config.Default().Output); err != nil {
		t.Fatal(err)
	}
	stored, err := LoadGraph(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.Nodes[0].Meta["community"]; !strings.HasPrefix(got, "c-") {
		t.Fatalf("community metadata = %q", got)
	}
	if g.Nodes[0].Meta != nil {
		t.Fatal("WriteArtifacts mutated its input graph")
	}
}

func TestWriteJSONReplacesAtomicallyAndCleansTemporaryFiles(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "graph.json")
	if err := WriteJSON(path, map[string]string{"value": "old"}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteJSON(path, map[string]string{"value": strings.Repeat("new", 1_000)}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("replacement is not valid JSON: %v", err)
	}
	if decoded["value"] != strings.Repeat("new", 1_000) {
		t.Fatalf("replacement value length = %d", len(decoded["value"]))
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("replacement mode = %o, want 600", info.Mode().Perm())
		}
	}
	temporary, err := filepath.Glob(filepath.Join(directory, ".graph.json.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary files remain: %v", temporary)
	}
}

func TestWriteJSONNewArtifactUsesNormalCreationPermissions(t *testing.T) {
	directory := t.TempDir()
	reference := filepath.Join(directory, "reference.json")
	path := filepath.Join(directory, "graph.json")
	if err := os.WriteFile(reference, []byte("reference"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(path, map[string]string{"value": "new"}); err != nil {
		t.Fatal(err)
	}
	referenceInfo, err := os.Stat(reference)
	if err != nil {
		t.Fatal(err)
	}
	artifactInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if artifactInfo.Mode().Perm() != referenceInfo.Mode().Perm() {
		t.Fatalf("new artifact mode = %o, normal creation mode = %o", artifactInfo.Mode().Perm(), referenceInfo.Mode().Perm())
	}
	assertNoJSONTemporaryFiles(t, directory)
}

func TestWriteJSONEncodingFailurePreservesLastGoodArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.json")
	if err := WriteJSON(path, map[string]string{"value": "good"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(path, make(chan int)); err == nil {
		t.Fatal("expected unsupported-value encoding error")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("failed write changed artifact: before=%q after=%q", before, after)
	}
}

func TestWriteJSONDoesNotReplaceDirectoryAfterRenameFailure(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "graph.json")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, "keep")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteJSON(path, map[string]string{"value": "replacement"}); err == nil {
		t.Fatal("expected replacing a directory to fail")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("failed replacement changed directory into %v", info.Mode())
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "keep" {
		t.Fatalf("failed replacement changed directory contents: data=%q err=%v", data, err)
	}
	assertNoJSONTemporaryFiles(t, directory)
}

func TestWriteJSONReplacesSymlinkWithoutChangingItsTarget(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	path := filepath.Join(directory, "graph.json")
	before := []byte("target must remain unchanged")
	if err := os.WriteFile(target, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	if err := WriteJSON(path, map[string]string{"value": "replacement"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("symlink target changed: before=%q after=%q", before, after)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		t.Fatalf("artifact is still a symlink: mode=%v", info.Mode())
	}
	var decoded map[string]string
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &decoded); err != nil || decoded["value"] != "replacement" {
		t.Fatalf("replacement artifact = %q, decode error = %v", data, err)
	}
}

func TestWriteJSONReadersNeverObservePartialReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Go does not guarantee atomic Rename replacement on Windows")
	}
	path := filepath.Join(t.TempDir(), "graph.json")
	if err := WriteJSON(path, map[string]string{"value": "initial"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for index := 0; index < 50; index++ {
			if err := WriteJSON(path, map[string]string{"value": strings.Repeat(string(rune('a'+index%26)), 64<<10)}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	reads := 0
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			if reads == 0 {
				t.Fatal("replacement completed before any concurrent read")
			}
			return
		default:
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]string
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("reader observed partial JSON after %d reads: %v", reads, err)
			}
			reads++
		}
	}
}

func assertNoJSONTemporaryFiles(t *testing.T, directory string) {
	t.Helper()
	temporary, err := filepath.Glob(filepath.Join(directory, ".graph.json.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary files remain: %v", temporary)
	}
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
