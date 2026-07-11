package corpus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/12ya/reporavel/internal/graph"
)

type fakeRunner struct{}

func (fakeRunner) LookPath(name string) (string, error) { return "/tools/" + name, nil }
func (fakeRunner) Run(_ context.Context, _ string, args ...string) error {
	output := args[len(args)-1]
	if args[0] == "-layout" {
		output = args[2]
	}
	return os.WriteFile(output, []byte("page one\fpage two"), 0o644)
}

func TestExtractPDFUsesAuditedPathAndWritesManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "guide.pdf"), []byte("pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := graph.Graph{Root: root, Nodes: []graph.Node{{ID: "file://guide.pdf", Kind: graph.NodeFile, Path: "guide.pdf", Meta: map[string]string{"language": "pdf"}}}}
	out := filepath.Join(t.TempDir(), "corpus")
	manifest, err := Extract(context.Background(), g, out, []string{"guide.pdf"}, fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Results) != 1 || manifest.Results[0].Tool != "pdftotext" {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	if _, err := os.Stat(filepath.Join(out, "manifest.json")); err != nil {
		t.Fatal(err)
	}
}

func TestExtractRejectsUnauditedPath(t *testing.T) {
	if _, err := Extract(context.Background(), graph.Graph{Root: t.TempDir()}, t.TempDir(), []string{"secret.pdf"}, fakeRunner{}); err == nil {
		t.Fatal("expected unaudited path error")
	}
}

func TestExtractRejectsSymlinkOutsideRoot(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "outside.pdf")
	if err := os.WriteFile(external, []byte("private"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "guide.pdf")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	g := graph.Graph{Root: root, Nodes: []graph.Node{{ID: "file://guide.pdf", Kind: graph.NodeFile, Path: "guide.pdf", Meta: map[string]string{"language": "pdf"}}}}
	if _, err := Extract(context.Background(), g, t.TempDir(), []string{"guide.pdf"}, fakeRunner{}); err == nil {
		t.Fatal("expected external symlink rejection")
	}
}
