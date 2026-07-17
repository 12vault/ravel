package scan

import (
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	statHashCacheSchema  = 1
	maxStatHashCacheSize = 64 << 20
)

type statHashCacheEntry struct {
	Size      int64  `json:"size"`
	ModTimeNS int64  `json:"mtime_ns"`
	Hash      string `json:"hash"`
}

type statHashCachePayload struct {
	Schema int                           `json:"schema"`
	Root   string                        `json:"root"`
	Files  map[string]statHashCacheEntry `json:"files"`
}

type statHashCache struct {
	path     string
	root     string
	force    bool
	previous map[string]statHashCacheEntry
	next     map[string]statHashCacheEntry
}

func newStatHashCache(path, root string, force bool) *statHashCache {
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	cache := &statHashCache{
		path:     filepath.Clean(path),
		root:     root,
		force:    force,
		previous: map[string]statHashCacheEntry{},
		next:     map[string]statHashCacheEntry{},
	}
	cache.load()
	return cache
}

func (c *statHashCache) load() {
	info, err := os.Lstat(c.path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxStatHashCacheSize {
		return
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	var payload statHashCachePayload
	if json.Unmarshal(data, &payload) != nil || payload.Schema != statHashCacheSchema || payload.Root != c.root {
		return
	}
	for path, entry := range payload.Files {
		if path == "" || !validSHA256(entry.Hash) {
			continue
		}
		c.previous[path] = entry
	}
}

func (c *statHashCache) hash(path, relative string, info fs.FileInfo, calculate func(string) (string, error)) (string, error) {
	relative = filepath.ToSlash(relative)
	entry := statHashCacheEntry{Size: info.Size(), ModTimeNS: info.ModTime().UnixNano()}
	if previous, ok := c.previous[relative]; !c.force && ok && previous.Size == entry.Size && previous.ModTimeNS == entry.ModTimeNS && validSHA256(previous.Hash) {
		c.next[relative] = previous
		return previous.Hash, nil
	}
	hash, err := calculate(path)
	if err != nil {
		return "", err
	}
	entry.Hash = hash
	c.next[relative] = entry
	return hash, nil
}

func (c *statHashCache) save() error {
	if equalStatHashEntries(c.previous, c.next) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(statHashCachePayload{Schema: statHashCacheSchema, Root: c.root, Files: c.next})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(c.path), ".stat-index-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, c.path); err != nil {
		return err
	}
	temporaryPath = ""
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func equalStatHashEntries(left, right map[string]statHashCacheEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for path, entry := range left {
		if right[path] != entry {
			return false
		}
	}
	return true
}
