package community

import (
	"sort"
	"strconv"

	"github.com/12vault/ravel/internal/graph"
)

type overlapCandidate struct {
	oldID        string
	newID        string
	intersection int
	union        int
}

// RemapLabels transfers deterministic display labels from previous communities
// by Jaccard overlap. Membership-hashed IDs remain authoritative. Descriptions
// are never transferred when IDs differ.
func RemapLabels(current, previous graph.Graph) graph.Graph {
	if !assigned(current) || !assigned(previous) {
		return current
	}
	oldMembers := membersByCommunity(previous)
	newMembers := membersByCommunity(current)
	if len(oldMembers) == 0 || len(newMembers) == 0 {
		return current
	}

	oldLabels := map[string]string{}
	oldDescriptions := map[string]map[string]string{}
	for _, node := range previous.Nodes {
		id := node.Meta[MetaKey]
		if oldLabels[id] == "" {
			oldLabels[id] = node.Meta[MetaLabelKey]
			if oldLabels[id] == "" {
				oldLabels[id] = node.Meta[MetaNameKey]
			}
		}
		if oldDescriptions[id] == nil && node.Meta[MetaDescriptionKey] != "" {
			oldDescriptions[id] = map[string]string{}
			for _, key := range []string{MetaDescriptionKey, MetaDescriptionSourceKey, MetaDescriptionConfidenceKey, MetaDescriptionRationaleKey} {
				oldDescriptions[id][key] = node.Meta[key]
			}
		}
	}

	var candidates []overlapCandidate
	mergeMatches := map[string]int{}
	type communityPair struct{ oldID, newID string }
	newByNode := map[string]string{}
	for newID, members := range newMembers {
		for nodeID := range members {
			newByNode[nodeID] = newID
		}
	}
	intersections := map[communityPair]int{}
	for oldID, members := range oldMembers {
		for nodeID := range members {
			if newID := newByNode[nodeID]; newID != "" {
				intersections[communityPair{oldID, newID}]++
			}
		}
	}
	for pair, intersection := range intersections {
		candidate := overlapCandidate{oldID: pair.oldID, newID: pair.newID, intersection: intersection, union: len(oldMembers[pair.oldID]) + len(newMembers[pair.newID]) - intersection}
		if atLeast(candidate, 2, 5) {
			mergeMatches[pair.newID]++
		}
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		leftProduct := left.intersection * right.union
		rightProduct := right.intersection * left.union
		if leftProduct != rightProduct {
			return leftProduct > rightProduct
		}
		if left.newID != right.newID {
			return left.newID < right.newID
		}
		return left.oldID < right.oldID
	})

	type match struct{ oldID, status, overlap string }
	matches := map[string]match{}
	usedOld := map[string]bool{}
	for _, candidate := range candidates {
		if usedOld[candidate.oldID] || matches[candidate.newID].oldID != "" {
			continue
		}
		if mergeMatches[candidate.newID] > 1 {
			continue
		}
		if !atLeast(candidate, 2, 5) {
			continue
		}
		status := "provisional"
		if candidate.oldID == candidate.newID {
			status = "stable"
		} else if atLeast(candidate, 7, 10) {
			status = "remapped"
		}
		matches[candidate.newID] = match{oldID: candidate.oldID, status: status, overlap: overlapString(candidate)}
		usedOld[candidate.oldID] = true
	}

	out := current
	out.Nodes = append([]graph.Node(nil), current.Nodes...)
	for i := range out.Nodes {
		id := out.Nodes[i].Meta[MetaKey]
		matched, ok := matches[id]
		if !ok {
			continue
		}
		meta := cloneMeta(out.Nodes[i].Meta)
		if label := oldLabels[matched.oldID]; label != "" {
			meta[MetaLabelKey] = label
		}
		meta[MetaLabelStatusKey] = matched.status
		meta[MetaLabelOverlapKey] = matched.overlap
		if matched.oldID != id {
			meta[MetaPreviousIDKey] = matched.oldID
		} else {
			delete(meta, MetaPreviousIDKey)
		}
		if matched.oldID == id {
			for key, value := range oldDescriptions[matched.oldID] {
				meta[key] = value
			}
		} else {
			delete(meta, MetaDescriptionKey)
			delete(meta, MetaDescriptionSourceKey)
			delete(meta, MetaDescriptionConfidenceKey)
			delete(meta, MetaDescriptionRationaleKey)
		}
		out.Nodes[i].Meta = meta
	}
	return out
}

func membersByCommunity(g graph.Graph) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, node := range g.Nodes {
		id := node.Meta[MetaKey]
		if id == "" {
			continue
		}
		if out[id] == nil {
			out[id] = map[string]bool{}
		}
		out[id][node.ID] = true
	}
	return out
}

func atLeast(candidate overlapCandidate, numerator, denominator int) bool {
	return candidate.intersection*denominator >= candidate.union*numerator
}

func overlapString(candidate overlapCandidate) string {
	return strconv.FormatFloat(float64(candidate.intersection)/float64(candidate.union), 'f', 3, 64)
}

func cloneMeta(meta map[string]string) map[string]string {
	out := make(map[string]string, len(meta)+4)
	for key, value := range meta {
		out[key] = value
	}
	return out
}
