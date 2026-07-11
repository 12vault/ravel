package share

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/12ya/reporavel/internal/dashboard"
	"github.com/12ya/reporavel/internal/graph"
	"github.com/12ya/reporavel/internal/report"
	"github.com/12ya/reporavel/internal/store"
)

type Manifest struct {
	Format            string    `json:"format"`
	Version           int       `json:"version"`
	GeneratedAt       time.Time `json:"generatedAt"`
	Nodes             int       `json:"nodes"`
	Edges             int       `json:"edges"`
	ContainsRawSource bool      `json:"containsRawSource"`
}

func Write(outDir string, g graph.Graph, now time.Time) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := store.WriteJSON(filepath.Join(outDir, "graph.json"), g); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "report.md"), []byte(report.Markdown(g)), 0o644); err != nil {
		return err
	}
	if err := dashboard.Write(filepath.Join(outDir, "graph.html"), g); err != nil {
		return err
	}
	manifest := Manifest{Format: "ravel-share", Version: 1, GeneratedAt: now.UTC(), Nodes: len(g.Nodes), Edges: len(g.Edges), ContainsRawSource: false}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "manifest.json"), append(data, '\n'), 0o644)
}

func Validate(outDir string) error {
	for _, name := range []string{"graph.json", "report.md", "graph.html", "manifest.json"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			return fmt.Errorf("invalid share bundle: %s: %w", name, err)
		}
	}
	return nil
}
