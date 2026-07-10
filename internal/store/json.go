package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/12ya/reporavel/internal/config"
	"github.com/12ya/reporavel/internal/graph"
	"github.com/12ya/reporavel/internal/scan"
)

type SymbolsFile struct {
	Symbols []graph.Node `json:"symbols"`
}

func WriteArtifacts(outDir string, g graph.Graph, scanResult scan.Result, report string, output config.OutputConfig) error {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}
	jsonFiles := []string{"graph.json", "files.json", "symbols.json"}
	if output.JSON {
		if err := WriteJSON(filepath.Join(outDir, "graph.json"), g); err != nil {
			return err
		}
		if err := WriteJSON(filepath.Join(outDir, "files.json"), scanResult); err != nil {
			return err
		}
		if err := WriteJSON(filepath.Join(outDir, "symbols.json"), SymbolsFile{Symbols: symbols(g)}); err != nil {
			return err
		}
	} else if err := removeArtifacts(outDir, jsonFiles); err != nil {
		return err
	}
	if output.MarkdownReport {
		if err := os.WriteFile(filepath.Join(outDir, "report.md"), []byte(report), 0644); err != nil {
			return err
		}
	} else if err := removeArtifacts(outDir, []string{"report.md"}); err != nil {
		return err
	}
	return removeArtifacts(outDir, []string{"index.db"})
}

func removeArtifacts(outDir string, names []string) error {
	for _, name := range names {
		if err := os.Remove(filepath.Join(outDir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func WriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

func LoadGraph(outDir string) (graph.Graph, error) {
	data, err := os.ReadFile(filepath.Join(outDir, "graph.json"))
	if err != nil {
		return graph.Graph{}, fmt.Errorf("load graph: %w", err)
	}
	var g graph.Graph
	if err := json.Unmarshal(data, &g); err != nil {
		return graph.Graph{}, err
	}
	return g, nil
}

func symbols(g graph.Graph) []graph.Node {
	var out []graph.Node
	for _, n := range g.Nodes {
		if graph.SymbolKind(n.Kind) {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
