package cli

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/12ya/reporavel/internal/scan"
)

func TestExecutePrintsVersion(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if err := Execute(context.Background(), args, &stdout, &stderr); err != nil {
			t.Fatalf("Execute(%v) error = %v", args, err)
		}
		want := "ravel " + Version + "\n"
		if stdout.String() != want {
			t.Fatalf("Execute(%v) output = %q, want %q", args, stdout.String(), want)
		}
		if stderr.Len() != 0 {
			t.Fatalf("Execute(%v) stderr = %q, want empty", args, stderr.String())
		}
	}
}

func TestSameScanUsesPathsAndHashes(t *testing.T) {
	before := scan.Result{Files: []scan.File{{Path: "a.go", Hash: "one"}}}
	after := scan.Result{Files: []scan.File{{Path: "a.go", Hash: "one", ModTime: time.Now()}}}
	if !sameScan(before, after) {
		t.Fatal("modification time alone should not trigger update")
	}
	after.Files[0].Hash = "two"
	if sameScan(before, after) {
		t.Fatal("changed hash should trigger update")
	}
}
