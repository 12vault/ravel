package security

import (
	"fmt"
	"io"

	"github.com/12vault/ravel/internal/config"
)

func WriteDoctor(w io.Writer, cfg config.Config) {
	fmt.Fprintln(w, "RepoRavel Doctor")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Mode: %s\n", cfg.Mode)
	fmt.Fprintln(w, "Network: disabled")
	fmt.Fprintln(w, "Shell execution: disabled")
	fmt.Fprintln(w, "LLM: disabled")
	fmt.Fprintln(w, "Subagents: disabled")
	fmt.Fprintln(w, "Secret files: ignored")
	fmt.Fprintf(w, "Output dir: %s\n", cfg.Output.Dir)
	fmt.Fprintln(w, "Scanned language inventory: scanner detection for audited safe files (inventory only)")
	fmt.Fprintf(w, "Polyglot Tree-sitter semantics: %s (syntax extracted; name-based targets inferred)\n", enabledLabel(cfg.Analysis.Polyglot))
	fmt.Fprintln(w, "Deterministic semantics: Go AST; pure-Go Tree-sitter definitions/call sites; Markdown headings/links; SQL tables/views/columns/indexes/foreign keys/FROM/JOIN references")
	fmt.Fprintf(w, "Max file size: %d bytes\n", cfg.Scan.MaxFileSizeBytes)
	fmt.Fprintf(w, "Max total read size: %d bytes\n", cfg.Scan.MaxTotalBytes)
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}
