package share

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/12ya/reporavel/internal/graph"
)

func TestWriteCreatesCommitSafeBundle(t *testing.T) {
	out := filepath.Join(t.TempDir(), "team-graph")
	g := graph.Graph{Nodes: []graph.Node{{ID: "file://main.go", Kind: graph.NodeFile, Name: "main.go", Path: "main.go"}}}
	if err := Write(out, g, time.Unix(123, 0)); err != nil {
		t.Fatal(err)
	}
	if err := Validate(out); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), `"containsRawSource": false`) {
		t.Fatalf("unsafe or malformed manifest: %s", manifest)
	}
	if _, err := os.Stat(filepath.Join(out, ".state")); !os.IsNotExist(err) {
		t.Fatalf("private state leaked into share bundle: %v", err)
	}
}
