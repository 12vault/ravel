package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/12ya/reporavel/internal/graph"
	"github.com/12ya/reporavel/internal/store"
)

func TestGraphCacheReusesIndexAndReloadsHashWithUnchangedStat(t *testing.T) {
	outDir := t.TempDir()
	path := filepath.Join(outDir, "graph.json")
	writeTestGraph(t, path, graphWithNode("node://one", "Alpha"))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	cache := newGraphCache(outDir)
	first, err := cache.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := cache.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if unchanged != first || unchanged.index != first.index {
		t.Fatal("unchanged graph rebuilt the immutable index")
	}

	writeTestGraph(t, path, graphWithNode("node://one", "Bravo"))
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	updatedInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if updatedInfo.Size() != info.Size() {
		t.Fatalf("fixture sizes differ: %d != %d", updatedInfo.Size(), info.Size())
	}

	updated, err := cache.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if updated == first || updated.index == first.index {
		t.Fatal("changed graph hash did not rebuild the index")
	}
	results := updated.index.Search("Bravo", 1)
	if len(results) != 1 || results[0].Node.Name != "Bravo" {
		t.Fatalf("reloaded search = %#v", results)
	}
}

func TestGraphCacheFallsBackToPrivateStateAndRecoversAfterCorruption(t *testing.T) {
	outDir := t.TempDir()
	statePath := filepath.Join(outDir, ".state", "graph.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestGraph(t, statePath, graphWithNode("node://private", "PrivateState"))

	cache := newGraphCache(outDir)
	first, err := cache.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := first.index.Search("PrivateState", 1); len(got) != 1 {
		t.Fatalf("private-state search = %#v", got)
	}

	if err := os.WriteFile(statePath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.snapshot(); err == nil {
		t.Fatal("corrupt replacement returned no error")
	}
	if cache.current != first {
		t.Fatal("corrupt replacement displaced the last good snapshot")
	}

	writeTestGraph(t, statePath, graphWithNode("node://recovered", "Recovered"))
	recovered, err := cache.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if recovered == first || len(recovered.index.Search("Recovered", 1)) != 1 {
		t.Fatalf("recovered snapshot = %#v", recovered)
	}
}

func TestGraphCacheSwitchesToPublicGraphWhenItAppears(t *testing.T) {
	outDir := t.TempDir()
	statePath := filepath.Join(outDir, ".state", "graph.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestGraph(t, statePath, graphWithNode("node://private", "PrivateState"))
	cache := newGraphCache(outDir)
	private, err := cache.snapshot()
	if err != nil {
		t.Fatal(err)
	}

	writeTestGraph(t, filepath.Join(outDir, "graph.json"), graphWithNode("node://public", "PublicState"))
	public, err := cache.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if public == private || len(public.index.Search("PublicState", 1)) != 1 {
		t.Fatalf("public snapshot = %#v", public)
	}
}

func writeTestGraph(t *testing.T, path string, value graph.Graph) {
	t.Helper()
	if err := store.WriteJSON(path, value); err != nil {
		t.Fatalf("WriteJSON(%q): %v", path, err)
	}
}

func graphWithNode(id, name string) graph.Graph {
	return graph.Graph{Version: "test", Nodes: []graph.Node{{ID: id, Kind: graph.NodeFunction, Name: name}}}
}
