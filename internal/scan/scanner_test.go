package scan

import "testing"

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
