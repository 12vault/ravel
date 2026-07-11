package update

import (
	"context"

	buildrunner "github.com/12ya/reporavel/internal/build"
	"github.com/12ya/reporavel/internal/config"
	"github.com/12ya/reporavel/internal/graph"
	"github.com/12ya/reporavel/internal/scan"
)

type Result struct {
	Build   buildrunner.Result
	Changed []string
	Removed []string
}

func Run(ctx context.Context, root string, cfg config.Config, previous graph.Graph, previousScan scan.Result) (Result, error) {
	built, err := buildrunner.Run(ctx, root, cfg)
	if err != nil {
		return Result{}, err
	}
	changed, removed := changes(previousScan.Files, built.Scan.Files)
	changedSet := map[string]bool{}
	for _, path := range append(append([]string{}, changed...), removed...) {
		changedSet[path] = true
	}
	built.Graph = preserveEnrichment(built.Graph, previous, changedSet)
	return Result{Build: built, Changed: changed, Removed: removed}, nil
}

func changes(oldFiles, newFiles []scan.File) ([]string, []string) {
	oldHashes := map[string]string{}
	newHashes := map[string]string{}
	for _, file := range oldFiles {
		oldHashes[file.Path] = file.Hash
	}
	for _, file := range newFiles {
		newHashes[file.Path] = file.Hash
	}
	var changed, removed []string
	for _, file := range newFiles {
		if oldHashes[file.Path] != file.Hash {
			changed = append(changed, file.Path)
		}
	}
	for _, file := range oldFiles {
		if _, ok := newHashes[file.Path]; !ok {
			removed = append(removed, file.Path)
		}
	}
	return changed, removed
}

func preserveEnrichment(current, previous graph.Graph, changed map[string]bool) graph.Graph {
	builder := graph.NewBuilder(current.Root)
	known := map[string]bool{}
	for _, node := range current.Nodes {
		known[node.ID] = true
		builder.AddNode(node)
	}
	for _, node := range previous.Nodes {
		if !agentGenerated(node.Meta) || (node.Path != "" && changed[node.Path]) {
			continue
		}
		known[node.ID] = true
		builder.AddNode(node)
	}
	for _, edge := range current.Edges {
		builder.AddEdge(edge)
	}
	for _, edge := range previous.Edges {
		if agentGenerated(edge.Meta) && known[edge.From] && known[edge.To] {
			builder.AddEdge(edge)
		}
	}
	for _, diagnostic := range current.Diagnostics {
		builder.AddDiagnostic(diagnostic)
	}
	return builder.Build()
}

func agentGenerated(meta map[string]string) bool {
	return meta != nil && meta["source"] != ""
}
