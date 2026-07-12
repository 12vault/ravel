package cli

import (
	"bytes"
	"strings"
	"testing"

	buildrunner "github.com/12vault/ravel/internal/build"
)

func TestTraversalProgressRendersLiveScanAndAnalysis(t *testing.T) {
	var output bytes.Buffer
	progress := &traversalProgress{w: &output, enabled: true}
	progress.Scan("internal/scan/scanner.go", 12)
	progress.Build(buildrunner.Progress{Stage: "Analyzing go", Path: "internal/cli/commands.go", Completed: 12, Total: 30})
	progress.Close()

	got := output.String()
	for _, want := range []string{"Scanning", "12 files", "scanner.go", "Analyzing go", "12/30 files", "commands.go", "\x1b[2K"} {
		if !strings.Contains(got, want) {
			t.Fatalf("progress output %q does not contain %q", got, want)
		}
	}
}

func TestShortenProgressPathKeepsTail(t *testing.T) {
	got := shortenProgressPath("some/very/long/path/to/scanner.go", 16)
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "scanner.go") || len([]rune(got)) > 16 {
		t.Fatalf("shortenProgressPath() = %q", got)
	}
}
