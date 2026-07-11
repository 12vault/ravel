package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/12ya/reporavel/internal/config"
)

func TestLanguageForPathRecognizesPopularAgentLanguages(t *testing.T) {
	tests := map[string]string{
		"app.py":       "python",
		"app.tsx":      "typescript",
		"main.rs":      "rust",
		"App.vue":      "vue",
		"main.dart":    "dart",
		"schema.proto": "protobuf",
		"infra.tf":     "terraform",
		"query.gql":    "graphql",
		"paper.pdf":    "pdf",
	}
	for path, want := range tests {
		if got := LanguageForPath(path); got != want {
			t.Errorf("LanguageForPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestScanAdmitsUnknownTextAndRejectsUnknownBinary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "program.wren"), []byte("class App {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "blob.custom"), []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Scan(root, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "program.wren" || result.Files[0].Language != "unknown" {
		t.Fatalf("files = %#v", result.Files)
	}
	if len(result.Ignored) != 1 || result.Ignored[0].Path != "blob.custom" {
		t.Fatalf("ignored = %#v", result.Ignored)
	}
}
