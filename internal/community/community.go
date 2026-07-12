// Package community detects stable, structural communities in a Ravel graph.
package community

import (
	"crypto/sha256"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/12vault/ravel/internal/graph"
)

const (
	MetaKey                      = "community"
	MetaNameKey                  = "communityName"
	MetaSizeKey                  = "communitySize"
	MetaGranularityKey           = "communityGranularity"
	MetaHubThresholdKey          = "communityHubThreshold"
	MetaDescriptionKey           = "communityDescription"
	MetaDescriptionSourceKey     = "communityDescriptionSource"
	MetaDescriptionConfidenceKey = "communityDescriptionConfidence"
	MetaDescriptionRationaleKey  = "communityDescriptionRationale"
)

type Preset string

const (
	PresetCoarse   Preset = "coarse"
	PresetBalanced Preset = "balanced"
	PresetFine     Preset = "fine"
)

type Options struct {
	Granularity        Preset
	HubDegreeThreshold int // 0 selects automatic p99; -1 disables down-weighting.
}

func DefaultOptions() Options { return Options{Granularity: PresetBalanced, HubDegreeThreshold: 0} }

func ParsePreset(value string) (Preset, error) {
	preset := Preset(strings.ToLower(strings.TrimSpace(value)))
	if preset == "" {
		preset = PresetBalanced
	}
	switch preset {
	case PresetCoarse, PresetBalanced, PresetFine:
		return preset, nil
	default:
		return "", errors.New("community granularity must be coarse, balanced, or fine")
	}
}

type Summary struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Size        int              `json:"size"`
	TopKinds    []graph.NodeKind `json:"topKinds"`
	Description string           `json:"description,omitempty"`
}

// Assign returns a copy of g whose nodes carry deterministic community IDs.
// It uses a deterministic, weighted local modularity optimization pass. The
// result changes metadata only. Edges and default retrieval behavior remain
// unchanged; callers may opt into community-aware retrieval separately.
func Assign(g graph.Graph) graph.Graph {
	return AssignWithOptions(g, DefaultOptions())
}

func AssignWithOptions(g graph.Graph, options Options) graph.Graph {
	if len(g.Nodes) == 0 {
		return g
	}
	preset, err := ParsePreset(string(options.Granularity))
	if err != nil {
		preset = PresetBalanced
	}
	options.Granularity = preset

	ids := make([]string, 0, len(g.Nodes))
	known := make(map[string]bool, len(g.Nodes))
	nodeByID := make(map[string]graph.Node, len(g.Nodes))
	for _, node := range g.Nodes {
		ids = append(ids, node.ID)
		known[node.ID] = true
		nodeByID[node.ID] = node
	}
	sort.Strings(ids)

	rawDegree := make(map[string]int64, len(ids))
	for _, edge := range g.Edges {
		if edge.From == edge.To || !known[edge.From] || !known[edge.To] {
			continue
		}
		rawDegree[edge.From]++
		rawDegree[edge.To]++
	}
	hubThreshold := options.HubDegreeThreshold
	if hubThreshold == 0 {
		hubThreshold = automaticHubThreshold(rawDegree)
	}

	weights := make(map[string]map[string]int64, len(ids))
	degree := make(map[string]int64, len(ids))
	var twiceTotal int64
	for _, edge := range g.Edges {
		if edge.From == edge.To || !known[edge.From] || !known[edge.To] {
			continue
		}
		w := edgeWeight(edge.Kind) * 1024
		if hubThreshold >= 0 {
			w = downWeightHub(w, rawDegree[edge.From], int64(hubThreshold))
			w = downWeightHub(w, rawDegree[edge.To], int64(hubThreshold))
		}
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
				resolution := presetResolution(options.Granularity)
				bestScore := inside[current]*twiceTotal*100 - totals[current]*ki*resolution
				for _, candidate := range candidates {
					score := inside[candidate]*twiceTotal*100 - totals[candidate]*ki*resolution
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
	if options.Granularity == PresetCoarse {
		coarsenSmallCommunities(labels, ids, weights)
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
		newCommunity := stable[labels[out.Nodes[i].ID]]
		oldCommunity := out.Nodes[i].Meta[MetaKey]
		meta := make(map[string]string, len(out.Nodes[i].Meta)+1)
		for key, value := range out.Nodes[i].Meta {
			if descriptionMetaKey(key) && oldCommunity != newCommunity {
				continue
			}
			meta[key] = value
		}
		meta[MetaKey] = newCommunity
		meta[MetaNameKey] = names[labels[out.Nodes[i].ID]]
		meta[MetaSizeKey] = strconv.Itoa(len(members[labels[out.Nodes[i].ID]]))
		meta[MetaGranularityKey] = string(options.Granularity)
		meta[MetaHubThresholdKey] = strconv.Itoa(hubThreshold)
		out.Nodes[i].Meta = meta
	}
	return out
}

func coarsenSmallCommunities(labels map[string]string, ids []string, weights map[string]map[string]int64) {
	members := map[string][]string{}
	for _, id := range ids {
		members[labels[id]] = append(members[labels[id]], id)
	}
	communityIDs := make([]string, 0, len(members))
	for id := range members {
		communityIDs = append(communityIDs, id)
	}
	sort.Strings(communityIDs)
	for _, id := range communityIDs {
		group := members[id]
		if len(group) == 0 || len(group) >= 4 {
			continue
		}
		neighborWeight := map[string]int64{}
		for _, node := range group {
			for neighbor, weight := range weights[node] {
				target := labels[neighbor]
				if target != id {
					neighborWeight[target] += weight
				}
			}
		}
		best := ""
		var bestWeight int64
		for target, weight := range neighborWeight {
			if len(members[target]) == 0 || len(group)+len(members[target]) > 8 {
				continue
			}
			if weight > bestWeight || (weight == bestWeight && (best == "" || target < best)) {
				best, bestWeight = target, weight
			}
		}
		if best == "" {
			continue
		}
		for _, node := range group {
			labels[node] = best
		}
		members[best] = append(members[best], group...)
		members[id] = nil
	}
}

func descriptionMetaKey(key string) bool {
	switch key {
	case MetaDescriptionKey, MetaDescriptionSourceKey, MetaDescriptionConfidenceKey, MetaDescriptionRationaleKey:
		return true
	default:
		return false
	}
}

// Summaries returns communities ordered by size, then stable ID.
func Summaries(g graph.Graph) []Summary {
	if !assigned(g) {
		g = Assign(g)
	}
	byID := map[string]*Summary{}
	kinds := map[string]map[graph.NodeKind]int{}
	for _, node := range g.Nodes {
		id := node.Meta[MetaKey]
		if byID[id] == nil {
			byID[id] = &Summary{ID: id, Name: node.Meta[MetaNameKey], Description: node.Meta[MetaDescriptionKey]}
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
			if !generatedMetaKey(key) {
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

func assigned(g graph.Graph) bool {
	if len(g.Nodes) == 0 {
		return true
	}
	for _, node := range g.Nodes {
		if node.Meta[MetaKey] == "" {
			return false
		}
	}
	return true
}

func generatedMetaKey(key string) bool {
	switch key {
	case MetaKey, MetaNameKey, MetaSizeKey, MetaGranularityKey, MetaHubThresholdKey,
		MetaDescriptionKey, MetaDescriptionSourceKey, MetaDescriptionConfidenceKey, MetaDescriptionRationaleKey:
		return true
	default:
		return false
	}
}

func automaticHubThreshold(degree map[string]int64) int {
	values := make([]int, 0, len(degree))
	for _, value := range degree {
		if value > 0 {
			values = append(values, int(value))
		}
	}
	if len(values) == 0 {
		return 50
	}
	sort.Ints(values)
	index := (len(values)*99+99)/100 - 1
	if index < 0 {
		index = 0
	}
	return max(50, values[index])
}

func downWeightHub(weight, degree, threshold int64) int64 {
	if threshold < 0 || degree <= threshold || degree == 0 {
		return weight
	}
	return max(int64(1), weight*threshold/degree)
}

func presetResolution(preset Preset) int64 {
	switch preset {
	case PresetCoarse:
		return 75
	case PresetFine:
		return 150
	default:
		return 100
	}
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
	type candidateRow struct {
		label string
		count int
	}
	rows := make([]candidateRow, 0, len(counts))
	for candidate, count := range counts {
		rows = append(rows, candidateRow{candidate, count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count == rows[j].count {
			return rows[i].label < rows[j].label
		}
		return rows[i].count > rows[j].count
	})
	if len(rows) > 0 && rows[0].label != "root" {
		label := rows[0].label
		if len(rows) > 1 && rows[1].label != "root" && rows[1].count*100 >= rows[0].count*70 {
			label += " + " + rows[1].label
		}
		return label
	}
	if anchor := anchorName(nodes); anchor != "" {
		return anchor
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

func anchorName(nodes []graph.Node) string {
	counts := map[string]int{}
	for _, node := range nodes {
		switch node.Kind {
		case graph.NodePackage, graph.NodeModule, graph.NodeClass, graph.NodeInterface, graph.NodeDomain, graph.NodeFlow:
			value := strings.TrimSpace(node.Name)
			if value != "" && value != "." {
				counts[value]++
			}
		}
	}
	best, bestCount := "", 0
	for value, count := range counts {
		if count > bestCount || (count == bestCount && value < best) {
			best, bestCount = value, count
		}
	}
	return best
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
	if len(parts) > 1 {
		if len(parts) > 2 && (parts[1] == "features" || parts[1] == "modules" || parts[1] == "services" || parts[1] == "packages") {
			return strings.Join(parts[:3], "/")
		}
		switch parts[0] {
		case "internal", "pkg", "cmd", "src", "app", "lib", "packages", "testdata":
			return strings.Join(parts[:2], "/")
		}
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
