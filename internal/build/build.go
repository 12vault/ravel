package build

import (
	"context"
	"path/filepath"

	"github.com/12vault/ravel/internal/config"
	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/lang"
	"github.com/12vault/ravel/internal/lang/contentanalyzer"
	"github.com/12vault/ravel/internal/lang/goanalyzer"
	"github.com/12vault/ravel/internal/lang/treeanalyzer"
	"github.com/12vault/ravel/internal/scan"
)

type Result struct {
	Scan  scan.Result
	Graph graph.Graph
}

func Run(ctx context.Context, root string, cfg config.Config) (Result, error) {
	scanResult, err := scan.Scan(root, cfg)
	if err != nil {
		return Result{}, err
	}

	builder := graph.NewBuilder(scanResult.Root)
	addFileTopology(builder, scanResult)

	registry := lang.NewRegistry()
	if cfg.Analysis.Go {
		registry.Register(goanalyzer.New(cfg.Analysis.CallGraph))
	}
	if cfg.Analysis.Documents {
		registry.Register(contentanalyzer.Markdown())
	}
	if cfg.Analysis.Schemas {
		registry.Register(contentanalyzer.SQL())
	}

	filesByLanguage := map[string][]scan.File{}
	for _, f := range scanResult.Files {
		filesByLanguage[f.Language] = append(filesByLanguage[f.Language], f)
	}
	for language, files := range filesByLanguage {
		analyzer, ok := registry.ForLanguage(language)
		if !ok && cfg.Analysis.Polyglot && treeanalyzer.Supports(language, files) {
			analyzer, ok = treeanalyzer.New(language), true
		}
		if !ok {
			continue
		}
		analysis, err := analyzer.Analyze(ctx, scanResult.Root, files)
		if err != nil {
			return Result{}, err
		}
		for _, n := range analysis.Nodes {
			builder.AddNode(n)
		}
		for _, e := range analysis.Edges {
			builder.AddEdge(e)
		}
		for _, d := range analysis.Diagnostics {
			builder.AddDiagnostic(d)
		}
	}

	return Result{Scan: scanResult, Graph: builder.Build()}, nil
}

func addFileTopology(builder *graph.Builder, scanResult scan.Result) {
	builder.AddNode(graph.Node{ID: graph.DirID("."), Kind: graph.NodeDir, Name: ".", Path: ".", Meta: topologyMeta(".")})
	builder.AddEdge(graph.Edge{Kind: graph.EdgeContains, From: graph.RepoID(), To: graph.DirID("."), Meta: topologyMeta(".")})
	seenDirs := map[string]bool{".": true}
	for _, file := range scanResult.Files {
		dir := graph.ParentDir(file.Path)
		addDir(builder, seenDirs, dir)
		builder.AddNode(graph.Node{
			ID:   graph.FileID(file.Path),
			Kind: graph.NodeFile,
			Name: filepath.Base(file.Path),
			Path: file.Path,
			Meta: map[string]string{
				"confidence": "extracted",
				"evidence":   file.Path,
				"language":   file.Language,
				"hash":       file.Hash,
				"size":       int64String(file.Size),
			},
		})
		builder.AddEdge(graph.Edge{Kind: graph.EdgeContains, From: graph.DirID(dir), To: graph.FileID(file.Path), Meta: topologyMeta(file.Path)})
	}
}

func addDir(builder *graph.Builder, seen map[string]bool, dir string) {
	if dir == "" {
		dir = "."
	}
	if seen[dir] {
		return
	}
	parent := filepath.ToSlash(filepath.Dir(dir))
	if parent == "." || parent == "/" {
		parent = "."
	}
	addDir(builder, seen, parent)
	builder.AddNode(graph.Node{ID: graph.DirID(dir), Kind: graph.NodeDir, Name: filepath.Base(dir), Path: dir, Meta: topologyMeta(dir)})
	builder.AddEdge(graph.Edge{Kind: graph.EdgeContains, From: graph.DirID(parent), To: graph.DirID(dir), Meta: topologyMeta(dir)})
	seen[dir] = true
}

func topologyMeta(evidence string) map[string]string {
	return map[string]string{"confidence": "extracted", "evidence": filepath.ToSlash(evidence)}
}

func int64String(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	n := v
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
