// Package community detects stable, structural communities in a Ravel graph.
package community

import (
	"crypto/sha256"
	"sort"

	"github.com/12vault/ravel/internal/graph"
)

const MetaKey = "community"

// Assign returns a copy of g whose nodes carry deterministic community IDs.
// It uses a deterministic, weighted local modularity optimization pass. The
// result is visualization metadata only; edges and retrieval behavior do not
// change.
func Assign(g graph.Graph) graph.Graph {
	if len(g.Nodes) == 0 {
		return g
	}

	ids := make([]string, 0, len(g.Nodes))
	known := make(map[string]bool, len(g.Nodes))
	for _, node := range g.Nodes {
		ids = append(ids, node.ID)
		known[node.ID] = true
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
	for label, communityMembers := range members {
		hash := sha256.New()
		for _, id := range communityMembers {
			_, _ = hash.Write([]byte(id))
			_, _ = hash.Write([]byte{0})
		}
		stable[label] = "c-" + fmtHex(hash.Sum(nil)[:4])
	}

	out := g
	out.Nodes = append([]graph.Node(nil), g.Nodes...)
	for i := range out.Nodes {
		meta := make(map[string]string, len(out.Nodes[i].Meta)+1)
		for key, value := range out.Nodes[i].Meta {
			meta[key] = value
		}
		meta[MetaKey] = stable[labels[out.Nodes[i].ID]]
		out.Nodes[i].Meta = meta
	}
	return out
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
