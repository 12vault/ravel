package scan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/12vault/ravel/internal/config"
)

func TestStatHashCacheReusesMatchingMetadataAndForceBypassesIt(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	if err := os.WriteFile(source, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(root, ".state", "stat-index-v1.json")
	first := newStatHashCache(cachePath, root, false)
	calculations := 0
	calculate := func(string) (string, error) {
		calculations++
		return strings.Repeat("a", 64), nil
	}
	if _, err := first.hash(source, "main.go", info, calculate); err != nil {
		t.Fatal(err)
	}
	if calculations != 1 {
		t.Fatalf("cold hash calculations = %d, want 1", calculations)
	}
	if err := first.save(); err != nil {
		t.Fatal(err)
	}

	calculations = 0
	warm := newStatHashCache(cachePath, root, false)
	if hash, err := warm.hash(source, "main.go", info, calculate); err != nil || hash != strings.Repeat("a", 64) {
		t.Fatalf("warm hash = %q, err = %v", hash, err)
	}
	if calculations != 0 {
		t.Fatalf("warm hash calculations = %d, want 0", calculations)
	}

	calculations = 0
	forced := newStatHashCache(cachePath, root, true)
	if _, err := forced.hash(source, "main.go", info, calculate); err != nil {
		t.Fatal(err)
	}
	if calculations != 1 {
		t.Fatalf("forced hash calculations = %d, want 1", calculations)
	}
}

func TestStatHashCacheInvalidatesSizeAndNanosecondModTime(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	if err := os.WriteFile(source, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseline := time.Unix(1_700_000_000, 123_456_789)
	if err := os.Chtimes(source, baseline, baseline); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(root, "stat-index-v1.json")
	cache := newStatHashCache(cachePath, root, false)
	if _, err := cache.hash(source, "main.go", info, func(string) (string, error) { return strings.Repeat("a", 64), nil }); err != nil {
		t.Fatal(err)
	}
	if err := cache.save(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		content string
		mtime   time.Time
	}{
		{name: "size", content: "longer\n", mtime: baseline},
		{name: "nanosecond mtime", content: "two\n", mtime: baseline.Add(time.Nanosecond)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(source, []byte(test.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(source, test.mtime, test.mtime); err != nil {
				t.Fatal(err)
			}
			changedInfo, err := os.Stat(source)
			if err != nil {
				t.Fatal(err)
			}
			calculations := 0
			loaded := newStatHashCache(cachePath, root, false)
			if _, err := loaded.hash(source, "main.go", changedInfo, func(string) (string, error) {
				calculations++
				return strings.Repeat("b", 64), nil
			}); err != nil {
				t.Fatal(err)
			}
			if calculations != 1 {
				t.Fatalf("hash calculations = %d, want 1", calculations)
			}
		})
	}
}

func TestScanWithOptionsPersistsPrunesAndRepairsStatHashIndex(t *testing.T) {
	root := t.TempDir()
	for name, source := range map[string]string{
		"main.go":  "package main\n",
		"other.go": "package other\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cachePath := filepath.Join(cfg.Output.Dir, ".state", "cache", "stat-index-v1.json")
	options := Options{HashCachePath: cachePath}
	first, err := ScanWithOptions(root, cfg, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != 2 {
		t.Fatalf("cold files = %#v", first.Files)
	}
	absoluteCachePath := filepath.Join(root, cachePath)
	baseline := time.Unix(946684800, 0)
	if err := os.Chtimes(absoluteCachePath, baseline, baseline); err != nil {
		t.Fatal(err)
	}
	second, err := ScanWithOptions(root, cfg, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	if second.Files[0].Hash != first.Files[0].Hash || second.Files[1].Hash != first.Files[1].Hash {
		t.Fatal("warm cached hashes differ from cold hashes")
	}
	if info, err := os.Stat(absoluteCachePath); err != nil || !info.ModTime().Equal(baseline) {
		t.Fatalf("warm scan rewrote stat index: modTime=%v, err=%v", info.ModTime(), err)
	}

	if err := os.Remove(filepath.Join(root, "other.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := ScanWithOptions(root, cfg, nil, options); err != nil {
		t.Fatal(err)
	}
	payload := readStatHashCachePayload(t, absoluteCachePath)
	if len(payload.Files) != 1 || payload.Files["main.go"].Hash == "" {
		t.Fatalf("pruned stat index = %#v", payload.Files)
	}

	if err := os.WriteFile(absoluteCachePath, []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ScanWithOptions(root, cfg, nil, options); err != nil {
		t.Fatal(err)
	}
	payload = readStatHashCachePayload(t, absoluteCachePath)
	if payload.Schema != statHashCacheSchema || payload.Root != first.Root || len(payload.Files) != 1 {
		t.Fatalf("repaired stat index = %#v", payload)
	}
}

func readStatHashCachePayload(t *testing.T, path string) statHashCachePayload {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var payload statHashCachePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}
