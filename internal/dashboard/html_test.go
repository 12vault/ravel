package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/graph"
)

func TestWriteCreatesSelfContainedDashboard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.html")
	g := graph.Graph{Nodes: []graph.Node{{ID: "concept://safe</script", Kind: graph.NodeConcept, Name: "Safe"}}}
	if err := Write(path, g); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "Ravel Graph") || strings.Contains(text, "safe</script") {
		t.Fatalf("unexpected dashboard output")
	}
	if !strings.Contains(text, `"community":"c-`) {
		t.Fatal("dashboard did not embed automatic community metadata")
	}
	if !strings.Contains(text, `id="community"`) || !strings.Contains(text, "Largest communities") {
		t.Fatal("dashboard is missing community filter or legend")
	}
}

func TestWriteCanDisableCommunities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.html")
	g := graph.Graph{Nodes: []graph.Node{{ID: "a", Meta: map[string]string{"community": "stale"}}}}
	if err := WriteWithOptions(path, g, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"community":"`) {
		t.Fatal("disabled dashboard embedded community metadata")
	}
}
