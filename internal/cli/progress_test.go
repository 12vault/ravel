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
	progress.Build(buildrunner.Progress{Stage: "Building graph", Path: "go://main.run", Completed: 12345, Unit: "nodes", Secondary: 366767, SecondaryUnit: "edges"})
	progress.Close()

	got := output.String()
	for _, want := range []string{"Scanning", "12 files", "scanner.go", "Analyzing go", "12/30 files", "commands.go", "Building graph", "12,345 nodes", "366,767 edges", "go://main.run", "\x1b[2K"} {
		if !strings.Contains(got, want) {
			t.Fatalf("progress output %q does not contain %q", got, want)
		}
	}
	if strings.ContainsAny(got, "◐◓◑◒") {
		t.Fatalf("progress output contains a spinner icon: %q", got)
	}
}

func TestFormatProgressNumber(t *testing.T) {
	for value, want := range map[int]string{0: "0", 12: "12", 1234: "1,234", 366767: "366,767", -12345: "-12,345"} {
		if got := formatProgressNumber(value); got != want {
			t.Errorf("formatProgressNumber(%d) = %q, want %q", value, got, want)
		}
	}
}

func TestShortenProgressPathKeepsTail(t *testing.T) {
	got := shortenProgressPath("some/very/long/path/to/scanner.go", 16)
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "scanner.go") || len([]rune(got)) > 16 {
		t.Fatalf("shortenProgressPath() = %q", got)
	}
}
