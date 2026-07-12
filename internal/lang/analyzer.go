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
