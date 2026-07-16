package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/12vault/ravel/internal/community"
	"github.com/12vault/ravel/internal/graph"
)

func Markdown(g graph.Graph) string {
	return MarkdownConfigured(g, true)
}

func MarkdownConfigured(g graph.Graph, communities bool) string {
	return MarkdownWithCommunityOptions(g, communities, community.DefaultOptions())
}

func MarkdownWithCommunityOptions(g graph.Graph, communities bool, options community.Options) string {
	if communities {
		g = community.AssignWithOptions(g, options)
	}
	return MarkdownPrepared(g, communities)
}

// MarkdownPrepared renders a graph whose optional community metadata has
// already been prepared by the caller.
func MarkdownPrepared(g graph.Graph, communities bool) string {
	var b strings.Builder
	b.WriteString("# RepoRavel Report\n\n")
	b.WriteString("## Summary\n")
	fmt.Fprintf(&b, "- Root: `%s`\n", g.Root)
	fmt.Fprintf(&b, "- Files analyzed: %d\n", g.Metrics.NodesByKind[graph.NodeFile])
	fmt.Fprintf(&b, "- Packages: %d\n", g.Metrics.NodesByKind[graph.NodePackage])
	fmt.Fprintf(&b, "- Functions: %d\n", g.Metrics.NodesByKind[graph.NodeFunction])
	fmt.Fprintf(&b, "- Methods: %d\n", g.Metrics.NodesByKind[graph.NodeMethod])
	fmt.Fprintf(&b, "- Types: %d\n", typeCount(g))
	fmt.Fprintf(&b, "- Documents: %d\n", g.Metrics.NodesByKind[graph.NodeDocument])
	fmt.Fprintf(&b, "- Schema objects: %d tables, %d views, %d indexes\n", g.Metrics.NodesByKind[graph.NodeTable], g.Metrics.NodesByKind[graph.NodeView], g.Metrics.NodesByKind[graph.NodeIndex])
	fmt.Fprintf(&b, "- Business domains: %d\n", g.Metrics.NodesByKind[graph.NodeDomain])
	fmt.Fprintf(&b, "- Business flows: %d\n", g.Metrics.NodesByKind[graph.NodeFlow])
	fmt.Fprintf(&b, "- Edges: %d\n", len(g.Edges))
	if communities {
		fmt.Fprintf(&b, "- Communities: %d\n", len(community.Summaries(g)))
		if len(g.Nodes) > 0 {
			fmt.Fprintf(&b, "- Community granularity: %s\n", g.Nodes[0].Meta[community.MetaGranularityKey])
			fmt.Fprintf(&b, "- Community hub threshold: %s\n", g.Nodes[0].Meta[community.MetaHubThresholdKey])
		}
	}
	b.WriteString("\n")

	b.WriteString("## Languages\n")
	if len(g.Metrics.Languages) == 0 {
		b.WriteString("- None\n")
	} else {
		for _, kv := range sortedStringCounts(g.Metrics.Languages) {
			fmt.Fprintf(&b, "- %s: %d files\n", kv.Key, kv.Count)
		}
	}
	b.WriteString("\n")

	writeNodeList(&b, "Entry Points", entryPoints(g), 20)
	writeNodeList(&b, "Core Packages", corePackages(g), 20)
	if communities {
		writeCommunities(&b, g)
	}
	writeNodeList(&b, "High Fan-In Symbols", highFan(g, true), 20)
	writeNodeList(&b, "High Fan-Out Symbols", highFan(g, false), 20)
	writeNodeList(&b, "Business Domains", nodesOfKind(g, graph.NodeDomain), 20)
	writeNodeList(&b, "Business Flows", nodesOfKind(g, graph.NodeFlow), 20)
	writeNodeList(&b, "Documents", nodesOfKind(g, graph.NodeDocument), 20)
	writeNodeList(&b, "Schema Tables", nodesOfKind(g, graph.NodeTable), 20)
	writeNodeList(&b, "Schema Views", nodesOfKind(g, graph.NodeView), 20)
	writeNodeList(&b, "Schema Indexes", nodesOfKind(g, graph.NodeIndex), 20)
	writeImportList(&b, g)
	writeImportCycles(&b, g)
	writeCallFlows(&b, g)
	writeDiagnostics(&b, g)
	writeReadingOrder(&b, g)
	return b.String()
}

func writeCommunities(b *strings.Builder, g graph.Graph) {
	b.WriteString("## Communities\n")
	summaries := community.Summaries(g)
	if len(summaries) == 0 {
		b.WriteString("- None found\n\n")
		return
	}
	for i, summary := range summaries {
		if i >= 20 {
			break
		}
		kinds := make([]string, len(summary.TopKinds))
		for j, kind := range summary.TopKinds {
			kinds[j] = string(kind)
		}
		fmt.Fprintf(b, "- **%s** (`%s`): %d nodes", summary.Name, summary.ID, summary.Size)
		if len(kinds) > 0 {
			fmt.Fprintf(b, " · %s", strings.Join(kinds, ", "))
		}
		if summary.LabelStatus == "remapped" {
			fmt.Fprintf(b, " · label remapped at %s overlap", summary.LabelOverlap)
		}
		if summary.LabelStatus == "provisional" {
			fmt.Fprintf(b, " · **label review required** at %s overlap", summary.LabelOverlap)
		}
		b.WriteString("\n")
		if summary.Description != "" {
			fmt.Fprintf(b, "  - %s\n", markdownText(summary.Description))
		}
	}
	b.WriteString("\n")
}

func markdownText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	replacer := strings.NewReplacer("\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(value)
}

func typeCount(g graph.Graph) int {
	return g.Metrics.NodesByKind[graph.NodeType] + g.Metrics.NodesByKind[graph.NodeStruct] + g.Metrics.NodesByKind[graph.NodeInterface]
}

func entryPoints(g graph.Graph) []graph.Node {
	var out []graph.Node
	for _, n := range g.Nodes {
		if n.Kind == graph.NodeFile && (strings.HasSuffix(n.Path, "/main.go") || n.Path == "main.go") {
			out = append(out, n)
		}
		if n.Kind == graph.NodeFunction && n.Name == "main" {
			out = append(out, n)
		}
		if n.Meta != nil && n.Meta["entrypoint"] == "true" {
			out = append(out, n)
		}
	}
	return uniqueNodes(out)
}

func nodesOfKind(g graph.Graph, kind graph.NodeKind) []graph.Node {
	var nodes []graph.Node
	for _, node := range g.Nodes {
		if node.Kind == kind && (node.Meta == nil || node.Meta["reference"] != "true") {
			nodes = append(nodes, node)
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	return nodes
}

func corePackages(g graph.Graph) []graph.Node {
	degree := degreeByNode(g)
	var pkgs []graph.Node
	for _, n := range g.Nodes {
		if n.Kind == graph.NodePackage {
			n.Meta = cloneMeta(n.Meta)
			n.Meta["degree"] = fmt.Sprintf("%d", degree[n.ID])
			pkgs = append(pkgs, n)
		}
	}
	sort.Slice(pkgs, func(i, j int) bool {
		di := degree[pkgs[i].ID]
		dj := degree[pkgs[j].ID]
		if di == dj {
			return pkgs[i].Path < pkgs[j].Path
		}
		return di > dj
	})
	return pkgs
}

func highFan(g graph.Graph, incoming bool) []graph.Node {
	counts := map[string]int{}
	nodes := map[string]graph.Node{}
	for _, n := range g.Nodes {
		if graph.SymbolKind(n.Kind) || n.Kind == graph.NodeImport {
			nodes[n.ID] = n
		}
	}
	for _, e := range g.Edges {
		if incoming {
			counts[e.To]++
		} else {
			counts[e.From]++
		}
	}
	var out []graph.Node
	for id, count := range counts {
		n, ok := nodes[id]
		if !ok || count == 0 {
			continue
		}
		n.Meta = cloneMeta(n.Meta)
		n.Meta["degree"] = fmt.Sprintf("%d", count)
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		di := out[i].Meta["degree"]
		dj := out[j].Meta["degree"]
		if di == dj {
			return out[i].ID < out[j].ID
		}
		return numericStringGreater(di, dj)
	})
	return out
}

func writeImportList(b *strings.Builder, g graph.Graph) {
	counts := map[string]int{}
	for _, e := range g.Edges {
		if e.Kind == graph.EdgeImports {
			counts[e.To]++
		}
	}
	var rows []countKV
	for _, n := range g.Nodes {
		if n.Kind == graph.NodeImport && counts[n.ID] > 0 {
			rows = append(rows, countKV{Key: n.Name, Count: counts[n.ID]})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count == rows[j].Count {
			return rows[i].Key < rows[j].Key
		}
		return rows[i].Count > rows[j].Count
	})
	b.WriteString("## Top Imports\n")
	if len(rows) == 0 {
		b.WriteString("- None\n\n")
		return
	}
	for i, row := range rows {
		if i >= 20 {
			break
		}
		fmt.Fprintf(b, "- `%s` (%d files)\n", row.Key, row.Count)
	}
	b.WriteString("\n")
}

func writeDiagnostics(b *strings.Builder, g graph.Graph) {
	if len(g.Diagnostics) == 0 {
		return
	}
	b.WriteString("## Diagnostics\n")
	for _, d := range g.Diagnostics {
		if d.Path != "" {
			fmt.Fprintf(b, "- %s: `%s`", d.Level, d.Path)
			if d.Line > 0 {
				fmt.Fprintf(b, ":%d", d.Line)
			}
			fmt.Fprintf(b, " - %s\n", d.Message)
		} else {
			fmt.Fprintf(b, "- %s: %s\n", d.Level, d.Message)
		}
	}
	b.WriteString("\n")
}

func writeReadingOrder(b *strings.Builder, g graph.Graph) {
	b.WriteString("## Suggested Reading Order\n")
	nodes := append(entryPoints(g), corePackages(g)...)
	nodes = uniqueNodes(nodes)
	if len(nodes) == 0 {
		b.WriteString("- No entry points found yet.\n")
		return
	}
	for i, n := range nodes {
		if i >= 12 {
			break
		}
		fmt.Fprintf(b, "%d. `%s`\n", i+1, display(n))
	}
	b.WriteString("\n")
}

func writeNodeList(b *strings.Builder, title string, nodes []graph.Node, limit int) {
	b.WriteString("## " + title + "\n")
	if len(nodes) == 0 {
		b.WriteString("- None found\n\n")
		return
	}
	for i, n := range nodes {
		if i >= limit {
			break
		}
		extra := ""
		if n.Meta != nil && n.Meta["degree"] != "" {
			extra = fmt.Sprintf(" (%s edges)", n.Meta["degree"])
		}
		fmt.Fprintf(b, "- `%s`%s\n", display(n), extra)
	}
	b.WriteString("\n")
}

func display(n graph.Node) string {
	if n.Path != "" && (n.Kind == graph.NodeFile || n.Kind == graph.NodePackage) {
		return n.Path
	}
	if n.Path != "" && graph.SymbolKind(n.Kind) {
		return n.Path + ":" + n.Name
	}
	return graph.DisplayName(n)
}

func degreeByNode(g graph.Graph) map[string]int {
	out := map[string]int{}
	for _, e := range g.Edges {
		out[e.From]++
		out[e.To]++
	}
	return out
}

type countKV struct {
	Key   string
	Count int
}

func sortedStringCounts(m map[string]int) []countKV {
	var out []countKV
	for k, v := range m {
		out = append(out, countKV{Key: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Key < out[j].Key
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func uniqueNodes(nodes []graph.Node) []graph.Node {
	seen := map[string]bool{}
	var out []graph.Node
	for _, n := range nodes {
		if seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		out = append(out, n)
	}
	return out
}

func cloneMeta(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func numericStringGreater(a, b string) bool {
	if len(a) != len(b) {
		return len(a) > len(b)
	}
	return a > b
}
