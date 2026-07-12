// Package community detects stable, structural communities in a Ravel graph.
package community

import (
	"crypto/sha256"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/12vault/ravel/internal/graph"
)

const (
	MetaKey     = "community"
	MetaNameKey = "communityName"
	MetaSizeKey = "communitySize"
)

type Summary struct {
	ID       string
	Name     string
	Size     int
	TopKinds []graph.NodeKind
}

// Assign returns a copy of g whose nodes carry deterministic community IDs.
// It uses a deterministic, weighted local modularity optimization pass. The
// result changes metadata only. Edges and default retrieval behavior remain
// unchanged; callers may opt into community-aware retrieval separately.
func Assign(g graph.Graph) graph.Graph {
	if len(g.Nodes) == 0 {
		return g
	}

	ids := make([]string, 0, len(g.Nodes))
	known := make(map[string]bool, len(g.Nodes))
	nodeByID := make(map[string]graph.Node, len(g.Nodes))
	for _, node := range g.Nodes {
		ids = append(ids, node.ID)
		known[node.ID] = true
		nodeByID[node.ID] = node
	}
	sort.Strings(ids)

	weights := make(map[string]map[string]int64, len(ids))
	degree := make(map[string]int64, len(ids))
	var twiceTotal int64
	for _, edge := range g.Edges {
		if edge.From == edge.To || !known[edge.From] || !known[edge.To] {
			continue
		}
		w := edgeWeight(edge.Kind)
		if weights[edge.From] == nil {
			weights[edge.From] = map[string]int64{}
		}
		if weights[edge.To] == nil {
			weights[edge.To] = map[string]int64{}
		}
		weights[edge.From][edge.To] += w
		weights[edge.To][edge.From] += w
		degree[edge.From] += w
		degree[edge.To] += w
		twiceTotal += 2 * w
	}

	labels := make(map[string]string, len(ids))
	totals := make(map[string]int64, len(ids))
	for _, id := range ids {
		labels[id] = id
		totals[id] = degree[id]
	}

	if twiceTotal > 0 {
		for pass := 0; pass < 50; pass++ {
			moved := false
			for _, id := range ids {
				ki := degree[id]
				if ki == 0 {
					continue
				}
				current := labels[id]
				totals[current] -= ki

				inside := map[string]int64{}
				for neighbor, weight := range weights[id] {
					inside[labels[neighbor]] += weight
				}
				candidates := make([]string, 0, len(inside)+1)
				for candidate := range inside {
					candidates = append(candidates, candidate)
				}
				if _, ok := inside[current]; !ok {
					candidates = append(candidates, current)
				}
				sort.Strings(candidates)

				best := current
				bestScore := inside[current]*twiceTotal - totals[current]*ki
				for _, candidate := range candidates {
					score := inside[candidate]*twiceTotal - totals[candidate]*ki
					if score > bestScore || (score == bestScore && candidate < best) {
						best, bestScore = candidate, score
					}
				}
				labels[id] = best
				totals[best] += ki
				moved = moved || best != current
			}
			if !moved {
				break
			}
		}
	}

	members := map[string][]string{}
	for _, id := range ids {
		members[labels[id]] = append(members[labels[id]], id)
	}
	stable := make(map[string]string, len(members))
	names := make(map[string]string, len(members))
	for label, communityMembers := range members {
		hash := sha256.New()
		for _, id := range communityMembers {
			_, _ = hash.Write([]byte(id))
			_, _ = hash.Write([]byte{0})
		}
		stable[label] = "c-" + fmtHex(hash.Sum(nil)[:4])
		group := make([]graph.Node, 0, len(communityMembers))
		for _, id := range communityMembers {
			group = append(group, nodeByID[id])
		}
		names[label] = name(group)
	}

	out := g
	out.Nodes = append([]graph.Node(nil), g.Nodes...)
	for i := range out.Nodes {
		meta := make(map[string]string, len(out.Nodes[i].Meta)+1)
		for key, value := range out.Nodes[i].Meta {
			meta[key] = value
		}
		meta[MetaKey] = stable[labels[out.Nodes[i].ID]]
		meta[MetaNameKey] = names[labels[out.Nodes[i].ID]]
		meta[MetaSizeKey] = strconv.Itoa(len(members[labels[out.Nodes[i].ID]]))
		out.Nodes[i].Meta = meta
	}
	return out
}

// Summaries returns communities ordered by size, then stable ID.
func Summaries(g graph.Graph) []Summary {
	g = Assign(g)
	byID := map[string]*Summary{}
	kinds := map[string]map[graph.NodeKind]int{}
	for _, node := range g.Nodes {
		id := node.Meta[MetaKey]
		if byID[id] == nil {
			byID[id] = &Summary{ID: id, Name: node.Meta[MetaNameKey]}
			kinds[id] = map[graph.NodeKind]int{}
		}
		byID[id].Size++
		kinds[id][node.Kind]++
	}
	out := make([]Summary, 0, len(byID))
	for id, summary := range byID {
		type row struct {
			kind  graph.NodeKind
			count int
		}
		var rows []row
		for kind, count := range kinds[id] {
			rows = append(rows, row{kind, count})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].count == rows[j].count {
				return rows[i].kind < rows[j].kind
			}
			return rows[i].count > rows[j].count
		})
		for i := 0; i < len(rows) && i < 3; i++ {
			summary.TopKinds = append(summary.TopKinds, rows[i].kind)
		}
		out = append(out, *summary)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Size == out[j].Size {
			return out[i].ID < out[j].ID
		}
		return out[i].Size > out[j].Size
	})
	return out
}

// Remove returns a copy without generated community metadata.
func Remove(g graph.Graph) graph.Graph {
	out := g
	out.Nodes = append([]graph.Node(nil), g.Nodes...)
	for i := range out.Nodes {
		if out.Nodes[i].Meta == nil {
			continue
		}
		meta := make(map[string]string, len(out.Nodes[i].Meta))
		for key, value := range out.Nodes[i].Meta {
			if key != MetaKey && key != MetaNameKey && key != MetaSizeKey {
				meta[key] = value
			}
		}
		if len(meta) == 0 {
			meta = nil
		}
		out.Nodes[i].Meta = meta
	}
	return out
}

func name(nodes []graph.Node) string {
	counts := map[string]int{}
	for _, node := range nodes {
		candidate := pathGroup(node)
		if candidate == "" {
			continue
		}
		weight := 1
		if node.Kind == graph.NodePackage || node.Kind == graph.NodeModule || node.Kind == graph.NodeDir || node.Kind == graph.NodeFile {
			weight = 3
		}
		counts[candidate] += weight
	}
	best, bestCount := "", 0
	for candidate, count := range counts {
		if count > bestCount || (count == bestCount && candidate < best) {
			best, bestCount = candidate, count
		}
	}
	if best != "" {
		return best
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	if len(nodes) == 0 {
		return "Unclassified"
	}
	label := strings.ReplaceAll(string(nodes[0].Kind), "_", " ")
	if label == "" {
		label = "node"
	}
	return strings.ToUpper(label[:1]) + label[1:] + " · " + nodes[0].Name
}

func pathGroup(node graph.Node) string {
	path := strings.Trim(strings.TrimPrefix(filepath.ToSlash(node.Path), "./"), "/")
	if path == "" || path == "." {
		return ""
	}
	if node.Kind != graph.NodeDir && node.Kind != graph.NodePackage && node.Kind != graph.NodeModule {
		path = filepath.ToSlash(filepath.Dir(path))
	}
	if path == "." || path == "" {
		return "root"
	}
	parts := strings.Split(path, "/")
	if len(parts) > 1 && (parts[0] == "internal" || parts[0] == "pkg" || parts[0] == "cmd") {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

func fmtHex(value []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(value)*2)
	for i, b := range value {
		out[i*2] = digits[b>>4]
		out[i*2+1] = digits[b&15]
	}
	return string(out)
}

func edgeWeight(kind graph.EdgeKind) int64 {
	switch kind {
	case graph.EdgeCalls, graph.EdgeImports, graph.EdgeDependsOn, graph.EdgeImplements, graph.EdgeReferences:
		return 3
	default:
		return 1
	}
}
