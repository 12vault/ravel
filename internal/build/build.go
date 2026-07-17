package build

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/12vault/ravel/internal/config"
	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/lang"
	"github.com/12vault/ravel/internal/lang/contentanalyzer"
	"github.com/12vault/ravel/internal/lang/goanalyzer"
	"github.com/12vault/ravel/internal/lang/treeanalyzer"
	"github.com/12vault/ravel/internal/scan"
)

type Result struct {
	Scan    scan.Result
	Graph   graph.Graph
	Skipped []scan.Ignored
}

type Progress struct {
	Stage         string
	Path          string
	Completed     int
	Total         int
	Unit          string
	Secondary     int
	SecondaryUnit string
}

func Run(ctx context.Context, root string, cfg config.Config) (Result, error) {
	return RunWithProgress(ctx, root, cfg, nil)
}

func RunWithProgress(ctx context.Context, root string, cfg config.Config, progress func(Progress)) (Result, error) {
	return RunWithCache(ctx, root, cfg, progress, CacheOptions{})
}

func RunWithCache(ctx context.Context, root string, cfg config.Config, progress func(Progress), cacheOptions CacheOptions) (Result, error) {
	scanResult, err := scan.ScanWithOptions(root, cfg, func(path string, files int) {
		if progress != nil {
			progress(Progress{Stage: "Scanning", Path: path, Completed: files})
		}
	}, scan.Options{HashCachePath: statHashCachePath(cacheOptions), ForceHashing: cacheOptions.Force})
	if err != nil {
		return Result{}, err
	}
	cache := newAnalysisCache(scanResult.Root, cacheOptions)

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
	completed := 0
	var analyses []*lang.AnalysisResult
	contributed := map[string]bool{}
	var skipped []scan.Ignored
	for language, files := range filesByLanguage {
		analyzer, ok := registry.ForLanguage(language)
		if !ok && cfg.Analysis.Polyglot && treeanalyzer.Supports(language, files) {
			analyzer, ok = treeanalyzer.NewWithJobs(language, cfg.Analysis.Jobs), true
		}
		if !ok {
			for _, file := range files {
				skipped = append(skipped, scan.Ignored{Path: file.Path, Reason: "no supported analyzer"})
			}
			completed += len(files)
			continue
		}
		units := analysisUnits(language, files)
		for _, unit := range units {
			status := func(cached bool) {
				if progress == nil {
					return
				}
				stage := "Analyzing " + language
				if cached {
					stage = "Cached " + language
				}
				progress(Progress{Stage: stage, Path: unit.files[0].Path, Completed: completed, Total: len(scanResult.Files)})
			}
			fileProgress := func(path string, unitCompleted int) {
				if progress != nil {
					progress(Progress{Stage: "Analyzing " + language, Path: path, Completed: completed + unitCompleted, Total: len(scanResult.Files)})
				}
			}
			analysis, err := analyzeWithCache(ctx, cache, analyzerIdentity(language, cfg), unit.name, analyzer, scanResult.Root, unit.files, status, fileProgress)
			if err != nil {
				return Result{}, err
			}
			analyses = append(analyses, analysis)
			for _, file := range unit.files {
				if analysisContributed(file.Path, analysis) {
					contributed[file.Path] = true
				} else {
					skipped = append(skipped, scan.Ignored{Path: file.Path, Reason: "analyzer produced zero graph content"})
				}
			}
			completed += len(unit.files)
		}
	}

	builder := graph.NewBuilder(scanResult.Root)
	addFileTopology(builder, scanResult, contributed)
	reportGraphProgress(progress, builder, "file topology", false)
	processed := 0
	for _, analysis := range analyses {
		for _, n := range analysis.Nodes {
			builder.AddNode(n)
			processed++
			if processed%256 == 0 {
				reportGraphProgress(progress, builder, n.ID, false)
			}
		}
		for _, e := range analysis.Edges {
			builder.AddEdge(e)
			processed++
			if processed%256 == 0 {
				reportGraphProgress(progress, builder, string(e.Kind)+" "+e.From+" → "+e.To, false)
			}
		}
		for _, d := range analysis.Diagnostics {
			builder.AddDiagnostic(d)
		}
	}
	reportGraphProgress(progress, builder, "sorting and measuring graph", true)

	graphResult := builder.Build()
	if cache != nil {
		if progress != nil {
			progress(Progress{Stage: "Cleaning cache", Completed: len(scanResult.Files), Total: len(scanResult.Files)})
		}
		cache.prune()
	}
	return Result{Scan: scanResult, Graph: graphResult, Skipped: skipped}, nil
}

func reportGraphProgress(progress func(Progress), builder *graph.Builder, path string, final bool) {
	if progress == nil {
		return
	}
	nodes, edges := builder.Counts()
	total := 0
	if final {
		total = nodes
	}
	progress(Progress{Stage: "Building graph", Path: path, Completed: nodes, Total: total, Unit: "nodes", Secondary: edges, SecondaryUnit: "edges"})
}

func analysisContributed(path string, analysis *lang.AnalysisResult) bool {
	for _, node := range analysis.Nodes {
		if node.Path == path || evidenceMatchesPath(node.Meta["evidence"], path) {
			return true
		}
	}
	for _, edge := range analysis.Edges {
		if evidenceMatchesPath(edge.Meta["evidence"], path) {
			return true
		}
	}
	return false
}

func evidenceMatchesPath(evidence, path string) bool {
	return evidence == path || strings.HasPrefix(evidence, path+":")
}

type analysisUnit struct {
	name  string
	files []scan.File
}

func analysisUnits(language string, files []scan.File) []analysisUnit {
	if language != "markdown" {
		return []analysisUnit{{name: "language:" + language, files: files}}
	}
	units := make([]analysisUnit, 0, len(files))
	for _, file := range files {
		units = append(units, analysisUnit{name: "markdown:" + file.Path, files: []scan.File{file}})
	}
	return units
}

func analyzerIdentity(language string, cfg config.Config) string {
	switch language {
	case "go":
		return "go;callGraph=" + boolString(cfg.Analysis.CallGraph) + ";typeResolution=" + boolString(cfg.Analysis.TypeResolution)
	case "markdown", "sql":
		return "content:" + language
	default:
		return "tree-sitter:" + language
	}
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func analyzeWithCache(ctx context.Context, cache *analysisCache, identity, unit string, analyzer lang.Analyzer, root string, files []scan.File, status func(cached bool), fileProgress func(path string, completed int)) (*lang.AnalysisResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cache != nil {
		if incremental, ok := analyzer.(lang.IncrementalProgressAnalyzer); ok {
			fileCache := cache.files(identity)
			result, err := incremental.AnalyzeWithFileCache(ctx, root, files, fileProgress, fileCache)
			if err != nil {
				return nil, err
			}
			status(fileCache.misses == 0 && fileCache.hits > 0)
			return result, nil
		}
		key, err := cache.key(identity, files)
		if err != nil {
			return nil, err
		}
		if result, ok := cache.load(unit, key); ok {
			status(true)
			return result, nil
		}
		status(false)
		result, err := analyze(ctx, analyzer, root, files, fileProgress)
		if err != nil {
			return nil, err
		}
		// Cache failures never make a valid analysis fail; the next build simply
		// analyzes the unit again.
		_ = cache.save(unit, key, result)
		return result, nil
	}
	status(false)
	result, err := analyze(ctx, analyzer, root, files, fileProgress)
	return result, err
}

func analyze(ctx context.Context, analyzer lang.Analyzer, root string, files []scan.File, progress func(path string, completed int)) (*lang.AnalysisResult, error) {
	if progressive, ok := analyzer.(lang.ProgressAnalyzer); ok {
		return progressive.AnalyzeWithProgress(ctx, root, files, progress)
	}
	return analyzer.Analyze(ctx, root, files)
}

func addFileTopology(builder *graph.Builder, scanResult scan.Result, contributed map[string]bool) {
	builder.AddNode(graph.Node{ID: graph.DirID("."), Kind: graph.NodeDir, Name: ".", Path: ".", Meta: topologyMeta(".")})
	builder.AddEdge(graph.Edge{Kind: graph.EdgeContains, From: graph.RepoID(), To: graph.DirID("."), Meta: topologyMeta(".")})
	seenDirs := map[string]bool{".": true}
	for _, file := range scanResult.Files {
		if !contributed[file.Path] {
			continue
		}
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
