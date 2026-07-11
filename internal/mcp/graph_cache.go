package mcp

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/12ya/reporavel/internal/graph"
	"github.com/12ya/reporavel/internal/query"
)

const maxGraphStateBytes int64 = 512 << 20

type graphFingerprint struct {
	path    string
	size    int64
	modTime time.Time
	hash    [sha256.Size]byte
}

type graphSnapshot struct {
	index       *query.Index
	fingerprint graphFingerprint
}

type graphCache struct {
	outDir  string
	mu      sync.Mutex
	current *graphSnapshot
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

	data, fingerprint, err := readGraphState(c.outDir)
	if err != nil {
		return nil, err
	}
	if c.current != nil && c.current.fingerprint == fingerprint {
		return c.current, nil
	}
	var g graph.Graph
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("decode graph state %s: %w", fingerprint.path, err)
	}
	c.current = &graphSnapshot{index: query.NewIndex(g), fingerprint: fingerprint}
	return c.current, nil
}

func readGraphState(outDir string) ([]byte, graphFingerprint, error) {
	path, err := graphStatePath(outDir)
	if err != nil {
		return nil, graphFingerprint{}, err
	}
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
	}, nil
}

func graphStatePath(outDir string) (string, error) {
	candidates := []string{
		filepath.Join(outDir, "graph.json"),
		filepath.Join(outDir, ".state", "graph.json"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("stat graph state %s: %w", candidate, err)
		}
	}
	return "", fmt.Errorf("graph state not found under %s", outDir)
}
