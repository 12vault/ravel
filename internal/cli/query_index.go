package cli

import (
	"path/filepath"

	"github.com/12vault/ravel/internal/query"
	"github.com/12vault/ravel/internal/store"
)

func loadQueryIndex(outDir string) (*query.Index, error) {
	graphData, err := store.LoadGraphData(outDir)
	if err != nil {
		return nil, err
	}
	index, _, err := query.LoadOrBuildIndex(graphData, filepath.Join(outDir, ".state", "cache"))
	return index, err
}
