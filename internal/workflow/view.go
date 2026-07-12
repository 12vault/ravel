package workflow

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/12vault/ravel/internal/graph"
)

type View struct {
	Mode      string            `json:"mode"`
	Summary   []string          `json:"summary"`
	Languages map[string]int    `json:"languages,omitempty"`
	Nodes     []graph.Node      `json:"nodes,omitempty"`
	Edges     []graph.Edge      `json:"edges,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}

func Build(mode string, g graph.Graph, targets []string) (View, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	view := View{Mode: mode, Languages: g.Metrics.Languages}
	switch mode {
	case "tech":
		view.Nodes = nodesOfKinds(g, graph.NodePackage, graph.NodeModule, graph.NodeSchema, graph.NodeTable, graph.NodeView)
		view.Summary = []string{
			fmt.Sprintf("%d files across %d detected languages", g.Metrics.NodesByKind[graph.NodeFile], len(g.Metrics.Languages)),
			fmt.Sprintf("%d packages/modules and %d schemas", g.Metrics.NodesByKind[graph.NodePackage]+g.Metrics.NodesByKind[graph.NodeModule], g.Metrics.NodesByKind[graph.NodeSchema]),
		}
	case "understand":
		view.Nodes = append(nodesOfKinds(g, graph.NodeDomain, graph.NodeFlow, graph.NodeStep, graph.NodeConcept), centralNodes(g, 20)...)
		view.Edges = edgesForNodes(g, view.Nodes)
		view.Summary = []string{
			fmt.Sprintf("%d nodes and %d relationships", len(g.Nodes), len(g.Edges)),
			fmt.Sprintf("%d domains, %d flows, %d concepts", g.Metrics.NodesByKind[graph.NodeDomain], g.Metrics.NodesByKind[graph.NodeFlow], g.Metrics.NodesByKind[graph.NodeConcept]),
		}
	case "learn":
		view.Nodes = append(nodesOfKinds(g, graph.NodeTour), centralNodes(g, 30)...)
		view.Edges = edgesForNodes(g, view.Nodes)
		view.Summary = []string{"Dependency-oriented learning view ordered by graph centrality."}
	case "docs":
		view.Nodes = nodesOfKinds(g, graph.NodeDocument, graph.NodeSection, graph.NodeConcept)
		view.Edges = edgesForNodes(g, view.Nodes)
		view.Summary = []string{fmt.Sprintf("%d documents, %d sections, %d concepts", g.Metrics.NodesByKind[graph.NodeDocument], g.Metrics.NodesByKind[graph.NodeSection], g.Metrics.NodesByKind[graph.NodeConcept])}
	case "pdf":
		for _, node := range g.Nodes {
			if node.Kind == graph.NodeFile && node.Meta["language"] == "pdf" {
				view.Nodes = append(view.Nodes, node)
			}
		}
		view.Nodes = append(view.Nodes, nodesOfKinds(g, graph.NodeDocument, graph.NodeSection, graph.NodeConcept)...)
		view.Edges = edgesForNodes(g, view.Nodes)
		view.Summary = []string{fmt.Sprintf("%d PDF corpus files and %d extracted document nodes", len(pdfFiles(g)), g.Metrics.NodesByKind[graph.NodeDocument])}
	case "schema":
		view.Nodes = nodesOfKinds(g, graph.NodeSchema, graph.NodeTable, graph.NodeView, graph.NodeColumn, graph.NodeIndex)
		view.Edges = edgesForNodes(g, view.Nodes)
		view.Summary = []string{fmt.Sprintf("%d schemas, %d tables, %d views, %d columns, %d indexes", g.Metrics.NodesByKind[graph.NodeSchema], g.Metrics.NodesByKind[graph.NodeTable], g.Metrics.NodesByKind[graph.NodeView], g.Metrics.NodesByKind[graph.NodeColumn], g.Metrics.NodesByKind[graph.NodeIndex])}
	case "diff":
		if len(targets) == 0 {
			view.Summary = []string{"No paths changed in the last recorded update."}
			break
		}
		view.Nodes, view.Edges = impact(g, targets, 2)
		view.Summary = []string{fmt.Sprintf("%d changed targets reach %d nodes within two graph hops", len(targets), len(view.Nodes))}
		view.Meta = map[string]string{"targets": strings.Join(targets, ",")}
	default:
		return View{}, fmt.Errorf("unsupported workflow %q", mode)
	}
	deduplicateAndSort(&view)
	return view, nil
}

func Write(w io.Writer, view View, jsonOutput bool) error {
	if jsonOutput {
		data, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(data))
		return err
	}
	fmt.Fprintf(w, "Ravel %s view\n", view.Mode)
	for _, line := range view.Summary {
		fmt.Fprintf(w, "- %s\n", line)
	}
	for _, node := range view.Nodes {
		location := node.Path
		if node.StartLine > 0 {
			location = fmt.Sprintf("%s:%d", location, node.StartLine)
		}
		fmt.Fprintf(w, "%s\t%s\t%s", node.Kind, node.ID, node.Name)
		if location != "" {
			fmt.Fprintf(w, "\t%s", location)
		}
		fmt.Fprintln(w)
	}
	return nil
}

func nodesOfKinds(g graph.Graph, kinds ...graph.NodeKind) []graph.Node {
	wanted := map[graph.NodeKind]bool{}
	for _, kind := range kinds {
		wanted[kind] = true
	}
	var nodes []graph.Node
	for _, node := range g.Nodes {
		if wanted[node.Kind] {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func centralNodes(g graph.Graph, limit int) []graph.Node {
	degree := map[string]int{}
	for _, edge := range g.Edges {
		degree[edge.From]++
		degree[edge.To]++
	}
	nodes := append([]graph.Node(nil), g.Nodes...)
	sort.Slice(nodes, func(i, j int) bool {
		if degree[nodes[i].ID] == degree[nodes[j].ID] {
			return nodes[i].ID < nodes[j].ID
		}
		return degree[nodes[i].ID] > degree[nodes[j].ID]
	})
	if len(nodes) > limit {
		nodes = nodes[:limit]
	}
	return nodes
}

func edgesForNodes(g graph.Graph, nodes []graph.Node) []graph.Edge {
	ids := map[string]bool{}
	for _, node := range nodes {
		ids[node.ID] = true
	}
	var edges []graph.Edge
	for _, edge := range g.Edges {
		if ids[edge.From] && ids[edge.To] {
			edges = append(edges, edge)
		}
	}
	return edges
}

func impact(g graph.Graph, targets []string, depth int) ([]graph.Node, []graph.Edge) {
	byID := map[string]graph.Node{}
	selected := map[string]bool{}
	for _, node := range g.Nodes {
		byID[node.ID] = node
		for _, target := range targets {
			if node.ID == target || node.Path == target || strings.EqualFold(node.Name, target) {
				selected[node.ID] = true
			}
		}
	}
	frontier := map[string]bool{}
	for id := range selected {
		frontier[id] = true
	}
	for hop := 0; hop < depth; hop++ {
		next := map[string]bool{}
		for _, edge := range g.Edges {
			if frontier[edge.From] || frontier[edge.To] {
				if !selected[edge.From] {
					next[edge.From] = true
				}
				if !selected[edge.To] {
					next[edge.To] = true
				}
			}
		}
		for id := range next {
			selected[id] = true
		}
		frontier = next
	}
	var nodes []graph.Node
	for id := range selected {
		if node := byID[id]; node.ID != "" {
			nodes = append(nodes, node)
		}
	}
	return nodes, edgesForNodes(g, nodes)
}

func pdfFiles(g graph.Graph) []graph.Node {
	var files []graph.Node
	for _, node := range g.Nodes {
		if node.Kind == graph.NodeFile && node.Meta["language"] == "pdf" {
			files = append(files, node)
		}
	}
	return files
}

func deduplicateAndSort(view *View) {
	nodes := map[string]graph.Node{}
	for _, node := range view.Nodes {
		nodes[node.ID] = node
	}
	view.Nodes = view.Nodes[:0]
	for _, node := range nodes {
		view.Nodes = append(view.Nodes, node)
	}
	sort.Slice(view.Nodes, func(i, j int) bool { return view.Nodes[i].ID < view.Nodes[j].ID })
	sort.Slice(view.Edges, func(i, j int) bool { return view.Edges[i].ID < view.Edges[j].ID })
}
