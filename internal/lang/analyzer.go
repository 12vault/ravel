package lang

import (
	"context"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/scan"
)

type AnalysisResult struct {
	Nodes       []graph.Node
	Edges       []graph.Edge
	Diagnostics []graph.Diagnostic
}

type Analyzer interface {
	Language() string
	Extensions() []string
	Analyze(ctx context.Context, root string, files []scan.File) (*AnalysisResult, error)
}

// ProgressAnalyzer reports the file currently being analyzed while preserving
// a language-wide analysis unit for cross-file relationship resolution.
type ProgressAnalyzer interface {
	Analyzer
	AnalyzeWithProgress(ctx context.Context, root string, files []scan.File, progress func(path string, completed int)) (*AnalysisResult, error)
}

// FileAnalysisCache stores analyzer-owned, per-file intermediate results. The
// build layer owns persistence and invalidation; analyzers own the value shape.
type FileAnalysisCache interface {
	Load(file scan.File, destination any) bool
	Store(file scan.File, value any)
}

// IncrementalProgressAnalyzer reuses per-file intermediate results while still
// receiving the complete language unit for cross-file relationship resolution.
type IncrementalProgressAnalyzer interface {
	ProgressAnalyzer
	AnalyzeWithFileCache(ctx context.Context, root string, files []scan.File, progress func(path string, completed int), cache FileAnalysisCache) (*AnalysisResult, error)
}
