package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/12vault/ravel/internal/lang"
	"github.com/12vault/ravel/internal/scan"
)

const analysisCacheSchema = 1

type CacheOptions struct {
	OutputDir string
	Version   string
}

type analysisCache struct {
	dir     string
	root    string
	version string
	used    map[string]bool
}

type analysisCacheKey struct {
	Schema   int             `json:"schema"`
	Version  string          `json:"version"`
	Analyzer string          `json:"analyzer"`
	Root     string          `json:"root"`
	Files    []cacheFileHash `json:"files"`
}

type cacheFileHash struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type analysisCacheEntry struct {
	Schema int                 `json:"schema"`
	Key    string              `json:"key"`
	Result lang.AnalysisResult `json:"result"`
}

type analysisFileCacheEntry struct {
	Schema int             `json:"schema"`
	Key    string          `json:"key"`
	Value  json.RawMessage `json:"value"`
}

type fileAnalysisCache struct {
	cache    *analysisCache
	identity string
	hits     int
	misses   int
}

func newAnalysisCache(root string, options CacheOptions) *analysisCache {
	if strings.TrimSpace(options.OutputDir) == "" || strings.TrimSpace(options.Version) == "" {
		return nil
	}
	out := options.OutputDir
	if !filepath.IsAbs(out) {
		out = filepath.Join(root, out)
	}
	return &analysisCache{
		dir:     filepath.Join(out, ".state", "cache", "analysis-v1"),
		root:    root,
		version: options.Version,
		used:    map[string]bool{},
	}
}

func (c *analysisCache) key(analyzer string, files []scan.File) (string, error) {
	key := analysisCacheKey{Schema: analysisCacheSchema, Version: c.version, Analyzer: analyzer, Root: c.root}
	for _, file := range files {
		key.Files = append(key.Files, cacheFileHash{Path: file.Path, Hash: file.Hash})
	}
	data, err := json.Marshal(key)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (c *analysisCache) path(unit string) string {
	sum := sha256.Sum256([]byte(unit))
	name := safeCacheName(unit) + "-" + hex.EncodeToString(sum[:6]) + ".json"
	return filepath.Join(c.dir, name)
}

func safeCacheName(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "analysis"
	}
	if len(name) > 48 {
		return name[:48]
	}
	return name
}

func (c *analysisCache) load(unit, key string) (*lang.AnalysisResult, bool) {
	path := c.path(unit)
	c.used[path] = true
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<20 {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var entry analysisCacheEntry
	if json.Unmarshal(data, &entry) != nil || entry.Schema != analysisCacheSchema || entry.Key != key {
		return nil, false
	}
	return &entry.Result, true
}

func (c *analysisCache) save(unit, key string, result *lang.AnalysisResult) error {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	path := c.path(unit)
	c.used[path] = true
	data, err := json.Marshal(analysisCacheEntry{Schema: analysisCacheSchema, Key: key, Result: *result})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeCacheAtomic(path, data)
}

func (c *analysisCache) files(identity string) *fileAnalysisCache {
	return &fileAnalysisCache{cache: c, identity: identity}
}

func (c *fileAnalysisCache) Load(file scan.File, destination any) bool {
	key, err := c.cache.key(c.identity, []scan.File{file})
	if err != nil {
		c.misses++
		return false
	}
	path := c.cache.path(c.unit(file))
	c.cache.used[path] = true
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<20 {
		c.misses++
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		c.misses++
		return false
	}
	var entry analysisFileCacheEntry
	if json.Unmarshal(data, &entry) != nil || entry.Schema != analysisCacheSchema || entry.Key != key || json.Unmarshal(entry.Value, destination) != nil {
		c.misses++
		return false
	}
	c.hits++
	return true
}

func (c *fileAnalysisCache) Store(file scan.File, value any) {
	key, err := c.cache.key(c.identity, []scan.File{file})
	if err != nil {
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	path := c.cache.path(c.unit(file))
	c.cache.used[path] = true
	data, err := json.Marshal(analysisFileCacheEntry{Schema: analysisCacheSchema, Key: key, Value: encoded})
	if err != nil {
		return
	}
	data = append(data, '\n')
	if os.MkdirAll(c.cache.dir, 0o755) != nil {
		return
	}
	_ = writeCacheAtomic(path, data)
}

func (c *fileAnalysisCache) unit(file scan.File) string {
	return "file:" + c.identity + ":" + file.Path
}

func (c *analysisCache) prune() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(c.dir, entry.Name())
		if !c.used[path] {
			_ = os.Remove(path)
		}
	}
}

func writeCacheAtomic(path string, data []byte) (err error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".analysis-cache-*.tmp")
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
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	temporaryPath = ""
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil && runtime.GOOS != "windows" {
		return err
	}
	return nil
}
