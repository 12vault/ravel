package cli

import (
	"bytes"
	"context"
	"testing"
)

func TestExecutePrintsVersion(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if err := Execute(context.Background(), args, &stdout, &stderr); err != nil {
			t.Fatalf("Execute(%v) error = %v", args, err)
		}
		want := "ravel v0.1.0\n"
		if stdout.String() != want {
			t.Fatalf("Execute(%v) output = %q, want %q", args, stdout.String(), want)
		}
		if stderr.Len() != 0 {
			t.Fatalf("Execute(%v) stderr = %q, want empty", args, stderr.String())
		}
	}
}
