package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/12vault/ravel/internal/graph"
)

const (
	maxArchitectureRows = 10
	maxCallFlowNodes    = 6
)

func writeImportCycles(b *strings.Builder, g graph.Graph) {
	b.WriteString("## Import Cycles\n")
	cycles := importCycles(g)
	if len(cycles) == 0 {
		b.WriteString("- None detected\n\n")
		return
	}
	for i, cycle := range cycles {
		if i >= maxArchitectureRows {
			break
		}
		fmt.Fprintf(b, "- %s\n", formatArchitecturePath(cycle))
	}
	b.WriteString("\n")
}

func writeCallFlows(b *strings.Builder, g graph.Graph) {
	b.WriteString("## Key Call Flows\n")
	flows := keyCallFlows(g)
	if len(flows) == 0 {
		b.WriteString("- None detected\n\n")
		return
	}
	for i, flow := range flows {
		if i >= maxArchitectureRows {
			break
		}
		fmt.Fprintf(b, "- %s\n", formatArchitecturePath(flow))
	}
	b.WriteString("\n")
}

func formatArchitecturePath(nodes []graph.Node) string {
	parts := make([]string, 0, len(nodes))
	for _, node := range nodes {
		label := strings.ReplaceAll(display(node), "`", "\\`")
		parts = append(parts, "`"+label+"`")
	}
	return strings.Join(parts, " → ")
}

// importCycles returns one concrete, deterministic cycle for every strongly
// connected import component. Only repository-local files, packages, and
// modules participate; unresolved external import nodes cannot form cycles.
func importCycles(g graph.Graph) [][]graph.Node {
	nodes := architectureNodes(g, func(node graph.Node) bool {
		return node.Kind == graph.NodeFile || node.Kind == graph.NodePackage || node.Kind == graph.NodeModule
	})
	adjacency := map[string][]string{}
	for _, edge := range g.Edges {
		if edge.Kind != graph.EdgeImports || edge.Meta["resolved"] == "false" {
			continue
		}
		if _, ok := nodes[edge.From]; !ok {
			continue
		}
		if _, ok := nodes[edge.To]; !ok {
			continue
		}
		adjacency[edge.From] = appendUnique(adjacency[edge.From], edge.To)
	}
	for id := range adjacency {
		sort.Strings(adjacency[id])
	}

	components := stronglyConnectedComponents(nodes, adjacency)
	var cycles [][]graph.Node
	for _, component := range components {
		if len(component) == 1 && !containsString(adjacency[component[0]], component[0]) {
			continue
		}
		ids := concreteCycle(component, adjacency)
		if len(ids) < 2 {
			continue
		}
		cycle := make([]graph.Node, 0, len(ids))
		for _, id := range ids {
			cycle = append(cycle, nodes[id])
		}
		cycles = append(cycles, cycle)
	}
	sort.Slice(cycles, func(i, j int) bool {
		return architecturePathKey(cycles[i]) < architecturePathKey(cycles[j])
	})
	return cycles
}

func stronglyConnectedComponents(nodes map[string]graph.Node, adjacency map[string][]string) [][]string {
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	index := 0
	indices := map[string]int{}
	lowlink := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var components [][]string
	var visit func(string)
	visit = func(id string) {
		indices[id] = index
		lowlink[id] = index
		index++
		stack = append(stack, id)
		onStack[id] = true

		for _, next := range adjacency[id] {
			if _, seen := indices[next]; !seen {
				visit(next)
				if lowlink[next] < lowlink[id] {
					lowlink[id] = lowlink[next]
				}
			} else if onStack[next] && indices[next] < lowlink[id] {
				lowlink[id] = indices[next]
			}
		}

		if lowlink[id] != indices[id] {
			return
		}
		var component []string
		for len(stack) > 0 {
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[last] = false
			component = append(component, last)
			if last == id {
				break
			}
		}
		sort.Strings(component)
		components = append(components, component)
	}

	for _, id := range ids {
		if _, seen := indices[id]; !seen {
			visit(id)
		}
	}
	return components
}

func concreteCycle(component []string, adjacency map[string][]string) []string {
	allowed := map[string]bool{}
	for _, id := range component {
		allowed[id] = true
	}
	start := component[0]
	path := []string{start}
	inPath := map[string]bool{start: true}
	var search func(string) bool
	search = func(current string) bool {
		for _, next := range adjacency[current] {
			if !allowed[next] {
				continue
			}
			if next == start {
				path = append(path, start)
				return true
			}
			if inPath[next] {
				continue
			}
			inPath[next] = true
			path = append(path, next)
			if search(next) {
				return true
			}
			path = path[:len(path)-1]
			delete(inPath, next)
		}
		return false
	}
	if search(start) {
		return path
	}
	return nil
}

// keyCallFlows chooses representative local call chains. Entrypoints come
// first, then roots with no incoming local calls, then high-fan-out callers.
// The traversal is bounded and cycle-safe so report generation remains cheap.
func keyCallFlows(g graph.Graph) [][]graph.Node {
	nodes := architectureNodes(g, func(node graph.Node) bool {
		return graph.SymbolKind(node.Kind) && node.Path != "" && node.Meta["resolved"] != "false"
	})
	adjacency := map[string][]string{}
	incoming := map[string]int{}
	for _, edge := range g.Edges {
		if edge.Kind != graph.EdgeCalls || edge.Meta["resolved"] == "false" {
			continue
		}
		if _, ok := nodes[edge.From]; !ok {
			continue
		}
		if _, ok := nodes[edge.To]; !ok {
			continue
		}
		before := len(adjacency[edge.From])
		adjacency[edge.From] = appendUnique(adjacency[edge.From], edge.To)
		if len(adjacency[edge.From]) != before {
			incoming[edge.To]++
		}
	}
	for id := range adjacency {
		sort.Slice(adjacency[id], func(i, j int) bool {
			left, right := adjacency[id][i], adjacency[id][j]
			if len(adjacency[left]) == len(adjacency[right]) {
				return left < right
			}
			return len(adjacency[left]) > len(adjacency[right])
		})
	}

	type seed struct {
		id       string
		priority int
	}
	var seeds []seed
	for id, node := range nodes {
		if len(adjacency[id]) == 0 {
			continue
		}
		priority := 2
		if node.Name == "main" || node.Meta["entrypoint"] == "true" {
			priority = 0
		} else if incoming[id] == 0 {
			priority = 1
		}
		seeds = append(seeds, seed{id: id, priority: priority})
	}
	sort.Slice(seeds, func(i, j int) bool {
		if seeds[i].priority != seeds[j].priority {
			return seeds[i].priority < seeds[j].priority
		}
		if len(adjacency[seeds[i].id]) != len(adjacency[seeds[j].id]) {
			return len(adjacency[seeds[i].id]) > len(adjacency[seeds[j].id])
		}
		return seeds[i].id < seeds[j].id
	})

	var flows [][]graph.Node
	seen := map[string]bool{}
	for _, candidate := range seeds {
		ids := representativeCallPath(candidate.id, adjacency)
		if len(ids) < 2 {
			continue
		}
		key := strings.Join(ids, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		flow := make([]graph.Node, 0, len(ids))
		for _, id := range ids {
			flow = append(flow, nodes[id])
		}
		flows = append(flows, flow)
		if len(flows) >= maxArchitectureRows {
			break
		}
	}
	return flows
}

func representativeCallPath(start string, adjacency map[string][]string) []string {
	path := []string{start}
	seen := map[string]bool{start: true}
	current := start
	for len(path) < maxCallFlowNodes {
		next := ""
		for _, candidate := range adjacency[current] {
			if !seen[candidate] {
				next = candidate
				break
			}
		}
		if next == "" {
			break
		}
		path = append(path, next)
		seen[next] = true
		current = next
	}
	return path
}

func architectureNodes(g graph.Graph, include func(graph.Node) bool) map[string]graph.Node {
	nodes := map[string]graph.Node{}
	for _, node := range g.Nodes {
		if include(node) {
			nodes[node.ID] = node
		}
	}
	return nodes
}

func appendUnique(values []string, value string) []string {
	if containsString(values, value) {
		return values
	}
	return append(values, value)
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func architecturePathKey(nodes []graph.Node) string {
	parts := make([]string, len(nodes))
	for i, node := range nodes {
		parts[i] = node.ID
	}
	return strings.Join(parts, "\x00")
}
