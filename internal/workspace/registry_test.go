package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/store"
)

func TestRegistryRoundTripIsPrivateStrictAndSorted(t *testing.T) {
	root := t.TempDir()
	registryPath := filepath.Join(root, "config", "registry.json")
	registry := Registry{Version: RegistryVersion, Projects: []Project{
		{Alias: "zeta", GraphDir: filepath.Join(root, "zeta")},
		{Alias: "alpha", GraphDir: filepath.Join(root, "alpha")},
	}}
	if err := SaveRegistry(registryPath, registry); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("registry permissions = %o, want private", info.Mode().Perm())
	}
	loaded, err := LoadRegistry(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{loaded.Projects[0].Alias, loaded.Projects[1].Alias}; !reflect.DeepEqual(got, []string{"alpha", "zeta"}) {
		t.Fatalf("project order = %#v", got)
	}
	if err := os.WriteFile(registryPath, []byte(`{"version":1,"projects":[],"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRegistry(registryPath); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestRegisterValidatesGraphAndMergeRegistryNamespacesProjects(t *testing.T) {
	root := t.TempDir()
	writeGraph := func(alias string) string {
		dir := filepath.Join(root, alias)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		g := graph.Graph{Root: "/private/" + alias, Nodes: []graph.Node{
			{ID: graph.RepoID(), Kind: graph.NodeRepo, Name: alias},
			{ID: graph.FileID("main.go"), Kind: graph.NodeFile, Name: "main.go", Path: "main.go"},
		}}
		if err := store.WriteJSON(filepath.Join(dir, "graph.json"), g); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	registry := EmptyRegistry()
	var err error
	registry, err = Register(registry, "alpha", writeGraph("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	registry, err = Register(registry, "beta", writeGraph("beta"))
	if err != nil {
		t.Fatal(err)
	}
	merged, err := MergeRegistry(registry)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, node := range merged.Nodes {
		if node.Kind == graph.NodeFile {
			paths[node.Path] = true
		}
		for _, value := range node.Meta {
			if strings.Contains(value, "/private/") || strings.Contains(value, root) {
				t.Fatalf("merged metadata leaked a source path: %#v", node)
			}
		}
	}
	if !reflect.DeepEqual(paths, map[string]bool{"alpha/main.go": true, "beta/main.go": true}) {
		t.Fatalf("merged paths = %#v", paths)
	}
	registry, removed := Remove(registry, "alpha")
	if !removed || len(registry.Projects) != 1 || registry.Projects[0].Alias != "beta" {
		t.Fatalf("remove result = removed %v registry %#v", removed, registry)
	}
}

func TestRegisterRejectsMissingGraphAndRegistryRejectsRelativePaths(t *testing.T) {
	if _, err := Register(EmptyRegistry(), "missing", t.TempDir()); err == nil || !strings.Contains(err.Error(), "load project") {
		t.Fatalf("missing graph error = %v", err)
	}
	registry := Registry{Version: RegistryVersion, Projects: []Project{{Alias: "relative", GraphDir: "graph"}}}
	if err := ValidateRegistry(registry); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("relative path error = %v", err)
	}
}
