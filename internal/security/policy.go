package security

import (
	"fmt"
	"io"

	"github.com/12ya/reporavel/internal/config"
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
	fmt.Fprintln(w, "Supported languages: Go")
	fmt.Fprintf(w, "Max file size: %d bytes\n", cfg.Scan.MaxFileSizeBytes)
	fmt.Fprintf(w, "Max total read size: %d bytes\n", cfg.Scan.MaxTotalBytes)
}
