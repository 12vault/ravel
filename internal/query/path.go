package query

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/12ya/reporavel/internal/graph"
)

type PathMode string

const (
	PathDirected           PathMode = "directed"
	PathUndirectedFallback PathMode = "undirected_fallback"
)

type PathHopDirection string

const (
	PathHopForward PathHopDirection = "forward"
	PathHopReverse PathHopDirection = "reverse"
)

// PathHop preserves both traversal direction and the original edge
// orientation. Reverse hops occur only in an undirected fallback.
type PathHop struct {
	From      string           `json:"from"`
	To        string           `json:"to"`
	Direction PathHopDirection `json:"direction"`
	Edge      graph.Edge       `json:"edge"`
}

// PathResult is an honest shortest-path envelope. Mode distinguishes a fully
// directed path from a connectivity-only undirected fallback.
type PathResult struct {
	Mode  PathMode     `json:"mode"`
	Nodes []graph.Node `json:"nodes"`
	Hops  []PathHop    `json:"hops"`
}

type pathAdjacent struct {
	next    string
	edge    graph.Edge
	forward bool
}

type pathPrevious struct {
	from string
	hop  pathAdjacent
}

// ShortestPathResult resolves both targets strictly, searches directed edges
// first, and labels any undirected fallback explicitly.
func ShortestPathResultFor(g graph.Graph, fromQuery, toQuery string) (PathResult, bool, error) {
	return NewIndex(g).ShortestPathResult(fromQuery, toQuery)
}

// ShortestPathResult is the reusable-index form of ShortestPathResultFor.
func (idx *Index) ShortestPathResult(fromQuery, toQuery string) (PathResult, bool, error) {
	from, err := idx.ResolveTarget(fromQuery)
	if err != nil {
		return PathResult{}, false, fmt.Errorf("path start: %w", err)
	}
	to, err := idx.ResolveTarget(toQuery)
	if err != nil {
		return PathResult{}, false, fmt.Errorf("path destination: %w", err)
	}
	if result, ok := idx.shortestPath(from.ID, to.ID, true); ok {
		result.Mode = PathDirected
		return result, true, nil
	}
	result, ok := idx.shortestPath(from.ID, to.ID, false)
	if ok {
		result.Mode = PathUndirectedFallback
	}
	return result, ok, nil
}

func (idx *Index) shortestPath(fromID, toID string, directed bool) (PathResult, bool) {
	adjacency := map[string][]pathAdjacent{}
	for _, edge := range idx.graph.Edges {
		if _, fromOK := idx.byID[edge.From]; !fromOK {
			continue
		}
		if _, toOK := idx.byID[edge.To]; !toOK {
			continue
		}
		adjacency[edge.From] = append(adjacency[edge.From], pathAdjacent{next: edge.To, edge: cloneEdge(edge), forward: true})
		if !directed && edge.From != edge.To {
			adjacency[edge.To] = append(adjacency[edge.To], pathAdjacent{next: edge.From, edge: cloneEdge(edge), forward: false})
		}
	}
	for nodeID := range adjacency {
		sort.Slice(adjacency[nodeID], func(i, j int) bool {
			left, right := adjacency[nodeID][i], adjacency[nodeID][j]
			if left.next != right.next {
				return left.next < right.next
			}
			if relationPriority(left.edge.Kind) != relationPriority(right.edge.Kind) {
				return relationPriority(left.edge.Kind) < relationPriority(right.edge.Kind)
			}
			if left.edge.Kind != right.edge.Kind {
				return left.edge.Kind < right.edge.Kind
			}
			return stableEdgeID(left.edge) < stableEdgeID(right.edge)
		})
	}

	queue := []string{fromID}
	seen := map[string]bool{fromID: true}
	previous := map[string]pathPrevious{}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == toID {
			return idx.reconstructPath(fromID, toID, previous), true
		}
		for _, adjacent := range adjacency[current] {
			if seen[adjacent.next] {
				continue
			}
			seen[adjacent.next] = true
			previous[adjacent.next] = pathPrevious{from: current, hop: adjacent}
			queue = append(queue, adjacent.next)
		}
	}
	return PathResult{}, false
}

func (idx *Index) reconstructPath(fromID, toID string, previous map[string]pathPrevious) PathResult {
	ids := []string{toID}
	var reversedHops []PathHop
	for current := toID; current != fromID; {
		entry := previous[current]
		direction := PathHopForward
		if !entry.hop.forward {
			direction = PathHopReverse
		}
		reversedHops = append(reversedHops, PathHop{
			From: entry.from, To: current, Direction: direction, Edge: cloneEdge(entry.hop.edge),
		})
		current = entry.from
		ids = append(ids, current)
	}
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	for i, j := 0, len(reversedHops)-1; i < j; i, j = i+1, j-1 {
		reversedHops[i], reversedHops[j] = reversedHops[j], reversedHops[i]
	}
	nodes := make([]graph.Node, 0, len(ids))
	for _, id := range ids {
		if docIndex, ok := idx.byID[id]; ok {
			nodes = append(nodes, cloneNode(idx.docs[docIndex].node))
		}
	}
	return PathResult{Nodes: nodes, Hops: reversedHops}
}

// WritePathResult emits explicit mode, edge kind, edge orientation, and hop
// direction. It never renders a reverse fallback hop as a directed arrow.
func WritePathResult(w io.Writer, result PathResult, jsonOut bool) error {
	if jsonOut {
		return writeJSON(w, result)
	}
	if len(result.Nodes) == 0 {
		_, err := fmt.Fprintln(w, "No path found.")
		return err
	}
	if len(result.Hops) != len(result.Nodes)-1 {
		return fmt.Errorf("invalid path result: %d nodes require %d hops, got %d", len(result.Nodes), len(result.Nodes)-1, len(result.Hops))
	}
	if _, err := fmt.Fprintf(w, "RAVEL_PATH\tmode=%s\thops=%d\n", result.Mode, len(result.Hops)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "NODE\t%s\t%s\t%s\n", safeText(string(result.Nodes[0].Kind)), compactID(result.Nodes[0].ID), safeText(display(result.Nodes[0]))); err != nil {
		return err
	}
	for index, hop := range result.Hops {
		parts := []string{
			"HOP", "direction=" + string(hop.Direction), "kind=" + safeText(string(hop.Edge.Kind)),
			"edge=" + compactID(stableEdgeID(hop.Edge)), "graph=" + compactID(hop.Edge.From) + "->" + compactID(hop.Edge.To),
		}
		if evidence := relationEvidence(hop.Edge.Meta); evidence != "" {
			parts = append(parts, "evidence="+quoteField(evidence))
		}
		if _, err := fmt.Fprintln(w, strings.Join(parts, "\t")); err != nil {
			return err
		}
		node := result.Nodes[index+1]
		if _, err := fmt.Fprintf(w, "NODE\t%s\t%s\t%s\n", safeText(string(node.Kind)), compactID(node.ID), safeText(display(node))); err != nil {
			return err
		}
	}
	return nil
}

func cloneEdge(edge graph.Edge) graph.Edge {
	clone := edge
	if edge.Meta != nil {
		clone.Meta = make(map[string]string, len(edge.Meta))
		for key, value := range edge.Meta {
			clone.Meta[key] = value
		}
	}
	return clone
}
