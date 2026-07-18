package mcp

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/12vault/ravel/internal/query"
)

const maxGraphStateBytes int64 = 512 << 20

type graphFingerprint struct {
	path    string
	size    int64
	modTime time.Time
	hash    [sha256.Size]byte
	info    os.FileInfo
}

type graphSnapshot struct {
	index       *query.Index
	fingerprint graphFingerprint
}

type graphCache struct {
	outDir  string
	mu      sync.Mutex
	current *graphSnapshot
	reads   int
}

func newGraphCache(outDir string) *graphCache {
	outDir = filepath.Clean(outDir)
	if absolute, err := filepath.Abs(outDir); err == nil {
		outDir = absolute
	}
	return &graphCache{outDir: outDir}
}

func (c *graphCache) snapshot() (*graphSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	path, info, err := graphStatePath(c.outDir)
	if err != nil {
		return nil, err
	}
	if c.current != nil && sameGraphState(c.current.fingerprint, path, info) {
		return c.current, nil
	}
	c.reads++
	data, fingerprint, err := readGraphState(path)
	if err != nil {
		return nil, err
	}
	if c.current != nil && c.current.fingerprint.hash == fingerprint.hash {
		c.current = &graphSnapshot{index: c.current.index, fingerprint: fingerprint}
		return c.current, nil
	}
	index, _, err := query.LoadOrBuildIndex(data, filepath.Join(c.outDir, ".state", "cache"))
	if err != nil {
		return nil, fmt.Errorf("load query index for graph state %s: %w", fingerprint.path, err)
	}
	c.current = &graphSnapshot{index: index, fingerprint: fingerprint}
	return c.current, nil
}

func readGraphState(path string) ([]byte, graphFingerprint, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, graphFingerprint{}, fmt.Errorf("open graph state %s: %w", path, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, graphFingerprint{}, fmt.Errorf("stat graph state %s: %w", path, err)
	}
	if info.Size() > maxGraphStateBytes {
		return nil, graphFingerprint{}, fmt.Errorf("graph state %s exceeds %d bytes", path, maxGraphStateBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxGraphStateBytes+1))
	if err != nil {
		return nil, graphFingerprint{}, fmt.Errorf("read graph state %s: %w", path, err)
	}
	if int64(len(data)) > maxGraphStateBytes {
		return nil, graphFingerprint{}, fmt.Errorf("graph state %s exceeds %d bytes", path, maxGraphStateBytes)
	}
	return data, graphFingerprint{
		path:    path,
		size:    int64(len(data)),
		modTime: info.ModTime(),
		hash:    sha256.Sum256(data),
		info:    info,
	}, nil
}

func graphStatePath(outDir string) (string, os.FileInfo, error) {
	candidates := []string{
		filepath.Join(outDir, "graph.json"),
		filepath.Join(outDir, ".state", "graph.json"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil {
			return candidate, info, nil
		} else if !os.IsNotExist(err) {
			return "", nil, fmt.Errorf("stat graph state %s: %w", candidate, err)
		}
	}
	return "", nil, fmt.Errorf("graph state not found under %s", outDir)
}

func sameGraphState(fingerprint graphFingerprint, path string, info os.FileInfo) bool {
	return fingerprint.path == path && fingerprint.size == info.Size() && fingerprint.modTime.Equal(info.ModTime()) && fingerprint.info != nil && os.SameFile(fingerprint.info, info)
}
