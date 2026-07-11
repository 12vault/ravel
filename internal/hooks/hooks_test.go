package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallStatusAndUninstallPreserveExistingHooks(t *testing.T) {
	root := t.TempDir()
	hooksDir := filepath.Join(root, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	postCommit := filepath.Join(hooksDir, "post-commit")
	existing := "#!/bin/sh\necho existing\n"
	if err := os.WriteFile(postCommit, []byte(existing), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Install(root, "/opt/ravel tools/ravel"); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(root, "/opt/ravel tools/ravel"); err != nil {
		t.Fatal(err)
	}
	status, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if !status.PostCommit || !status.PostCheckout {
		t.Fatalf("unexpected status: %+v", status)
	}
	data, err := os.ReadFile(postCommit)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), startMarker) != 1 || !strings.Contains(string(data), "echo existing") {
		t.Fatalf("unexpected installed hook:\n%s", data)
	}

	if _, err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(postCommit)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != existing {
		t.Fatalf("uninstall changed existing hook: %q", data)
	}
	if _, err := os.Stat(filepath.Join(hooksDir, "post-checkout")); !os.IsNotExist(err) {
		t.Fatalf("generated post-checkout hook still exists: %v", err)
	}
}
