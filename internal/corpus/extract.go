package corpus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/12vault/ravel/internal/graph"
)

type Result struct {
	Source   string `json:"source"`
	Output   string `json:"output"`
	Language string `json:"language"`
	Tool     string `json:"tool"`
}

type Manifest struct {
	Version int      `json:"version"`
	Results []Result `json:"results"`
}

type Runner interface {
	LookPath(name string) (string, error)
	Run(ctx context.Context, name string, args ...string) error
}

type ExecRunner struct{}

func (ExecRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func Extract(ctx context.Context, g graph.Graph, outDir string, paths []string, runner Runner) (Manifest, error) {
	if len(paths) == 0 {
		return Manifest{}, errors.New("extract requires one or more corpus paths")
	}
	if runner == nil {
		runner = ExecRunner{}
	}
	available := map[string]graph.Node{}
	for _, node := range g.Nodes {
		if node.Kind == graph.NodeFile {
			available[node.Path] = node
		}
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{Version: 1}
	for _, requested := range paths {
		path := filepath.ToSlash(filepath.Clean(requested))
		node, ok := available[path]
		if !ok {
			return Manifest{}, fmt.Errorf("corpus path %q is not in the audited graph", path)
		}
		input := filepath.Join(g.Root, filepath.FromSlash(path))
		if err := requireInsideRoot(g.Root, input); err != nil {
			return Manifest{}, fmt.Errorf("extract %s: %w", path, err)
		}
		output := filepath.Join(outDir, outputName(path))
		tool, err := extractOne(ctx, node.Meta["language"], input, output, runner)
		if err != nil {
			return Manifest{}, fmt.Errorf("extract %s: %w", path, err)
		}
		manifest.Results = append(manifest.Results, Result{Source: path, Output: filepath.ToSlash(output), Language: node.Meta["language"], Tool: tool})
	}
	sort.Slice(manifest.Results, func(i, j int) bool { return manifest.Results[i].Source < manifest.Results[j].Source })
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func requireInsideRoot(root, input string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	realInput, err := filepath.EvalSymlinks(input)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(realRoot, realInput)
	if err != nil {
		return err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("corpus symlink resolves outside the audited root")
	}
	return nil
}

func extractOne(ctx context.Context, language, input, output string, runner Runner) (string, error) {
	switch language {
	case "pdf":
		if tool, err := runner.LookPath("pdftotext"); err == nil {
			return "pdftotext", runner.Run(ctx, tool, "-layout", input, output)
		}
		if tool, err := runner.LookPath("mutool"); err == nil {
			return "mutool", runner.Run(ctx, tool, "draw", "-F", "txt", "-o", output, input)
		}
		return "", errors.New("no local PDF extractor found; install pdftotext or mutool")
	case "document":
		tool, err := runner.LookPath("pandoc")
		if err != nil {
			return "", errors.New("pandoc is required for Office document extraction")
		}
		return "pandoc", runner.Run(ctx, tool, "-t", "plain", "-o", output, input)
	case "markdown", "documentation", "text":
		data, err := os.ReadFile(input)
		if err != nil {
			return "", err
		}
		return "builtin", os.WriteFile(output, data, 0o644)
	default:
		return "", fmt.Errorf("unsupported corpus language %q", language)
	}
}

func outputName(path string) string {
	sum := sha256.Sum256([]byte(path))
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return base + "-" + hex.EncodeToString(sum[:6]) + ".txt"
}
