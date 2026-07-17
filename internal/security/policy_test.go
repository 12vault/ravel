package security

import (
	"bytes"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/config"
)

func TestWriteDoctorDistinguishesInventoryFromDeterministicSemantics(t *testing.T) {
	var output bytes.Buffer
	WriteDoctor(&output, config.Default())

	text := output.String()
	for _, want := range []string{
		"Scanned language inventory: scanner detection for audited safe files (inventory only)",
		"Polyglot Tree-sitter semantics: enabled (syntax extracted; name-based targets inferred)",
		"Tree-sitter worker limit: 4",
		"Deterministic semantics: Go AST; pure-Go Tree-sitter definitions/call sites; Markdown headings/links; SQL tables/views/columns/indexes/foreign keys/FROM/JOIN references",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Supported languages: Go") {
		t.Fatalf("doctor output still conflates inventory and semantics:\n%s", text)
	}
}
