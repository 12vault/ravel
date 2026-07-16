// Package workspace composes independently built Ravel graphs without
// mutating their source artifacts. Every source graph is namespaced by a
// caller-chosen alias so identical repository-relative IDs cannot collide.
package workspace

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/12vault/ravel/internal/graph"
)

var validAlias = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type Source struct {
	Alias    string
	Location string
	Graph    graph.Graph
}

func Merge(sources []Source) (graph.Graph, error) {
	if len(sources) == 0 {
		return graph.Graph{}, errors.New("at least one source graph is required")
	}
	sources = append([]Source(nil), sources...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].Alias < sources[j].Alias })

	builder := graph.NewBuilder("ravel-workspace")
	seenAliases := map[string]bool{}
	var generatedAt time.Time
	for _, source := range sources {
		alias := strings.TrimSpace(source.Alias)
		if !validAlias.MatchString(alias) {
			return graph.Graph{}, fmt.Errorf("invalid project alias %q: use 1-64 letters, digits, dots, underscores, or hyphens", source.Alias)
		}
		if seenAliases[alias] {
			return graph.Graph{}, fmt.Errorf("duplicate project alias %q", alias)
		}
		seenAliases[alias] = true
		if source.Graph.GeneratedAt.After(generatedAt) {
			generatedAt = source.Graph.GeneratedAt
		}
		if err := mergeSource(builder, Source{Alias: alias, Location: source.Location, Graph: source.Graph}); err != nil {
			return graph.Graph{}, err
		}
	}
	merged := builder.Build()
	merged.GeneratedAt = generatedAt
	return merged, nil
}

func mergeSource(builder *graph.Builder, source Source) error {
	projectID := graph.ContentID("workspace-project", source.Alias)
	evidence := "workspace:" + source.Alias
	projectMeta := map[string]string{
		"confidence": "extracted",
		"evidence":   evidence,
		"project":    source.Alias,
	}
	builder.AddNode(graph.Node{ID: projectID, Kind: graph.NodeRepo, Name: source.Alias, Path: source.Alias, Meta: projectMeta})
	builder.AddEdge(graph.Edge{Kind: graph.EdgeContains, From: graph.RepoID(), To: projectID, Meta: projectMeta})

	ids := make(map[string]string, len(source.Graph.Nodes))
	for _, node := range source.Graph.Nodes {
		if strings.TrimSpace(node.ID) == "" {
			return fmt.Errorf("project %q contains a node without an id", source.Alias)
		}
		id := workspaceNodeID(source.Alias, node.ID)
		if node.Kind == graph.NodeRepo {
			id = projectID
		}
		ids[node.ID] = id
	}
	for _, node := range source.Graph.Nodes {
		originalID := node.ID
		node.ID = ids[originalID]
		node.Meta = workspaceMeta(node.Meta, source, originalID)
		if node.Kind == graph.NodeRepo {
			node.Name = source.Alias
			node.Path = source.Alias
		} else {
			node.Path = workspacePath(source.Alias, node.Path)
			node.Package = workspacePath(source.Alias, node.Package)
		}
		builder.AddNode(node)
	}
	for _, edge := range source.Graph.Edges {
		originalID := edge.ID
		from, fromOK := ids[edge.From]
		to, toOK := ids[edge.To]
		if !fromOK || !toOK {
			return fmt.Errorf("project %q edge %s references unknown endpoint %q -> %q", source.Alias, edge.Kind, edge.From, edge.To)
		}
		edge.ID = ""
		edge.From = from
		edge.To = to
		edge.Meta = workspaceMeta(edge.Meta, source, originalID)
		builder.AddEdge(edge)
	}
	for _, diagnostic := range source.Graph.Diagnostics {
		diagnostic.Path = workspacePath(source.Alias, diagnostic.Path)
		builder.AddDiagnostic(diagnostic)
	}
	return nil
}

func workspaceNodeID(alias, originalID string) string {
	return graph.ContentID("workspace-node", alias, originalID)
}

func workspacePath(alias, path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	path = strings.TrimPrefix(path, "./")
	if path == "" || path == "." {
		return ""
	}
	return alias + "/" + path
}

func workspaceMeta(meta map[string]string, source Source, originalID string) map[string]string {
	out := make(map[string]string, len(meta)+2)
	for key, value := range meta {
		out[key] = value
	}
	out["project"] = source.Alias
	if originalID != "" {
		out["original_id"] = originalID
	}
	return out
}
