package query

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/12vault/ravel/internal/graph"
)

// TargetErrorKind identifies why an exact query target could not be resolved.
type TargetErrorKind string

const (
	TargetNotFound  TargetErrorKind = "not_found"
	TargetAmbiguous TargetErrorKind = "ambiguous"
)

// TargetError reports an unresolved or ambiguous target. Candidates are
// sorted by stable node ID so callers can present deterministic guidance.
type TargetError struct {
	Kind       TargetErrorKind
	Query      string
	Candidates []graph.Node
}

func (e *TargetError) Error() string {
	query := strings.TrimSpace(e.Query)
	if e.Kind != TargetAmbiguous {
		return fmt.Sprintf("target %q not found", query)
	}
	ids := make([]string, 0, min(len(e.Candidates), 8))
	for _, candidate := range e.Candidates[:min(len(e.Candidates), 8)] {
		ids = append(ids, safeTextBytes(candidate.ID, 256))
	}
	detail := strings.Join(ids, ", ")
	if omitted := len(e.Candidates) - len(ids); omitted > 0 {
		detail += fmt.Sprintf(", +%d more", omitted)
	}
	return fmt.Sprintf("target %q is ambiguous; use an exact node ID (candidates: %s)", query, detail)
}

// IsTargetError reports whether err is a target-resolution error of kind.
func IsTargetError(err error, kind TargetErrorKind) bool {
	var targetErr *TargetError
	return errors.As(err, &targetErr) && targetErr.Kind == kind
}

// ResolveTarget resolves IDs first, then source/container paths, then names.
// Duplicate exact names are rejected rather than depending on graph order.
// A canonical source node wins an exact path shared with symbols defined in
// that source (for example a file path shared by every function in the file).
func (idx *Index) ResolveTarget(query string) (graph.Node, error) {
	value := strings.TrimSpace(query)
	if value == "" {
		return graph.Node{}, &TargetError{Kind: TargetNotFound, Query: query}
	}

	if matches := idx.exactTargetMatches(func(node graph.Node) bool { return node.ID == value }); len(matches) > 0 {
		return resolveUniqueTarget(value, matches)
	}
	if matches := idx.exactTargetMatches(func(node graph.Node) bool { return node.Path == value }); len(matches) > 0 {
		preferred := preferredPathTargets(matches)
		return resolveUniqueTarget(value, preferred)
	}
	if matches := idx.exactTargetMatches(func(node graph.Node) bool { return node.Name == value }); len(matches) > 0 {
		return resolveUniqueTarget(value, matches)
	}

	results := idx.Search(value, 1)
	if len(results) == 0 {
		return graph.Node{}, &TargetError{Kind: TargetNotFound, Query: query}
	}
	return cloneNode(results[0].Node), nil
}

func (idx *Index) exactTargetMatches(match func(graph.Node) bool) []graph.Node {
	var matches []graph.Node
	for i := range idx.docs {
		if match(idx.docs[i].node) {
			matches = append(matches, cloneNode(idx.docs[i].node))
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
	return matches
}

func resolveUniqueTarget(query string, matches []graph.Node) (graph.Node, error) {
	if len(matches) == 1 {
		return cloneNode(matches[0]), nil
	}
	return graph.Node{}, &TargetError{Kind: TargetAmbiguous, Query: query, Candidates: matches}
}

func preferredPathTargets(matches []graph.Node) []graph.Node {
	bestPriority := 100
	var preferred []graph.Node
	for _, candidate := range matches {
		priority := pathTargetPriority(candidate.Kind)
		if priority > bestPriority {
			continue
		}
		if priority < bestPriority {
			bestPriority = priority
			preferred = preferred[:0]
		}
		preferred = append(preferred, candidate)
	}
	return preferred
}

func pathTargetPriority(kind graph.NodeKind) int {
	switch kind {
	case graph.NodeFile:
		return 0
	case graph.NodeDocument:
		return 1
	case graph.NodePackage, graph.NodeModule:
		return 2
	case graph.NodeDir, graph.NodeRepo:
		return 3
	default:
		return 100
	}
}
