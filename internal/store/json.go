package store

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/12vault/ravel/internal/community"
	"github.com/12vault/ravel/internal/config"
	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/scan"
)

const stateDir = ".state"

type SymbolsFile struct {
	Symbols []graph.Node `json:"symbols"`
}

type ChangesFile struct {
	Changed []string `json:"changed"`
	Removed []string `json:"removed"`
}

type ArtifactProgress struct {
	Path      string
	Completed int
	Total     int
}

func WriteArtifacts(outDir string, g graph.Graph, scanResult scan.Result, report string, output config.OutputConfig) error {
	if output.CommunityClustering {
		g = community.AssignWithOptions(g, community.Options{Granularity: community.Preset(output.CommunityGranularity), HubDegreeThreshold: output.CommunityHubDegreeThreshold})
	} else {
		g = community.Remove(g)
	}
	return WritePreparedArtifacts(outDir, g, scanResult, report, output)
}

// WritePreparedArtifacts persists a graph whose optional community metadata
// has already been prepared by the caller.
func WritePreparedArtifacts(outDir string, g graph.Graph, scanResult scan.Result, report string, output config.OutputConfig) error {
	return WritePreparedArtifactsWithProgress(outDir, g, scanResult, report, output, nil)
}

func WritePreparedArtifactsWithProgress(outDir string, g graph.Graph, scanResult scan.Result, report string, output config.OutputConfig, progress func(ArtifactProgress)) error {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}
	statePath := filepath.Join(outDir, stateDir)
	if err := os.MkdirAll(statePath, 0o755); err != nil {
		return err
	}
	total := 2
	if output.JSON {
		total += 3
	}
	if output.MarkdownReport {
		total++
	}
	completed := 0
	writeArtifact := func(path string, write func() error) error {
		if progress != nil {
			progress(ArtifactProgress{Path: path, Completed: completed, Total: total})
		}
		if err := write(); err != nil {
			return err
		}
		completed++
		if progress != nil {
			progress(ArtifactProgress{Path: path, Completed: completed, Total: total})
		}
		return nil
	}
	if err := writeArtifact(filepath.Join(stateDir, "graph.json"), func() error {
		return WriteJSON(filepath.Join(statePath, "graph.json"), g)
	}); err != nil {
		return err
	}
	if err := writeArtifact(filepath.Join(stateDir, "files.json"), func() error {
		return WriteJSON(filepath.Join(statePath, "files.json"), scanResult)
	}); err != nil {
		return err
	}
	jsonFiles := []string{"graph.json", "files.json", "symbols.json"}
	if output.JSON {
		if err := writeArtifact("graph.json", func() error {
			return WriteJSON(filepath.Join(outDir, "graph.json"), g)
		}); err != nil {
			return err
		}
		if err := writeArtifact("files.json", func() error {
			return WriteJSON(filepath.Join(outDir, "files.json"), scanResult)
		}); err != nil {
			return err
		}
		if err := writeArtifact("symbols.json", func() error {
			return WriteJSON(filepath.Join(outDir, "symbols.json"), SymbolsFile{Symbols: symbols(g)})
		}); err != nil {
			return err
		}
	} else if err := removeArtifacts(outDir, jsonFiles); err != nil {
		return err
	}
	if output.MarkdownReport {
		if err := writeArtifact("report.md", func() error {
			return os.WriteFile(filepath.Join(outDir, "report.md"), []byte(report), 0644)
		}); err != nil {
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
	return writeFileAtomic(path, data, 0o644)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) (err error) {
	directory := filepath.Dir(path)
	preserveMode := false
	if info, statErr := os.Lstat(path); statErr == nil {
		// Replacing a symlink must not inherit metadata from, or otherwise
		// depend on, a target that can live outside the artifact directory.
		if info.Mode().IsRegular() {
			mode = info.Mode().Perm()
			preserveMode = true
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if !preserveMode {
		mode, err = creationMode(directory, filepath.Base(path), mode)
		if err != nil {
			return err
		}
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if temporaryPath != "" {
			_ = os.Remove(temporaryPath)
		}
	}()
	written, err := temporary.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	// CreateTemp starts private. Keep the in-progress contents private until a
	// complete payload exists, then apply the final permissions before syncing.
	if err := temporary.Chmod(mode); err != nil {
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
	return syncDirectory(directory)
}

func creationMode(directory, base string, requested os.FileMode) (os.FileMode, error) {
	// CreateTemp always starts at 0600. Use an empty probe to let the operating
	// system apply the process umask to a newly created artifact's requested
	// mode, without ever exposing the artifact payload through that probe.
	path := filepath.Join(directory, "."+base+".tmp-mode-"+rand.Text())
	probe, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, requested)
	if err != nil {
		return 0, err
	}
	info, statErr := probe.Stat()
	closeErr := probe.Close()
	removeErr := os.Remove(path)
	if statErr != nil {
		return 0, statErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if removeErr != nil {
		return 0, removeErr
	}
	return info.Mode().Perm(), nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil && runtime.GOOS != "windows" {
		return err
	}
	return nil
}

func LoadGraph(outDir string) (graph.Graph, error) {
	data, err := LoadGraphData(outDir)
	if err != nil {
		return graph.Graph{}, err
	}
	var g graph.Graph
	if err := json.Unmarshal(data, &g); err != nil {
		return graph.Graph{}, err
	}
	return g, nil
}

// LoadGraphData returns the exact persisted graph bytes selected by LoadGraph.
// Query indexes hash these bytes so a cache can be validated without first
// decoding and normalizing the graph again.
func LoadGraphData(outDir string) ([]byte, error) {
	data, err := readState(outDir, "graph.json")
	if err != nil {
		return nil, fmt.Errorf("load graph: %w", err)
	}
	return data, nil
}

func LoadScan(outDir string) (scan.Result, error) {
	data, err := readState(outDir, "files.json")
	if err != nil {
		return scan.Result{}, fmt.Errorf("load files: %w", err)
	}
	var result scan.Result
	if err := json.Unmarshal(data, &result); err != nil {
		return scan.Result{}, err
	}
	return result, nil
}

func WriteChanges(outDir string, changed, removed []string) error {
	path := filepath.Join(outDir, stateDir)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	return WriteJSON(filepath.Join(path, "changes.json"), ChangesFile{Changed: changed, Removed: removed})
}

func LoadChanges(outDir string) (ChangesFile, error) {
	data, err := readState(outDir, "changes.json")
	if err != nil {
		return ChangesFile{}, fmt.Errorf("load changes: %w", err)
	}
	var changes ChangesFile
	if err := json.Unmarshal(data, &changes); err != nil {
		return ChangesFile{}, err
	}
	return changes, nil
}

func readState(outDir, name string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(outDir, name))
	if err == nil || !os.IsNotExist(err) {
		return data, err
	}
	return os.ReadFile(filepath.Join(outDir, stateDir, name))
}

func RewriteGraphViews(outDir string, g graph.Graph, markdown string) error {
	return RewriteGraphViewsConfigured(outDir, g, markdown, community.DefaultOptions())
}

func RewriteGraphViewsConfigured(outDir string, g graph.Graph, markdown string, options community.Options) error {
	g = community.AssignWithOptions(g, options)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}
	if err := WriteJSON(filepath.Join(outDir, "graph.json"), g); err != nil {
		return err
	}
	if err := WriteJSON(filepath.Join(outDir, "symbols.json"), SymbolsFile{Symbols: symbols(g)}); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "report.md"), []byte(markdown), 0644)
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
