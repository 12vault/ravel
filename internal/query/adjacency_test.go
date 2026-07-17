package query

import (
	"reflect"
	"sort"
	"testing"

	"github.com/12vault/ravel/internal/graph"
)

type legacyAdjacentEdge struct {
	nodeID   string
	edge     graph.Edge
	outgoing bool
}

func TestQueryAdjacencyMatchesLegacyConstruction(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "a", Kind: graph.NodeFunction, Name: "A", Meta: map[string]string{"community": "one"}},
			{ID: "b", Kind: graph.NodeFunction, Name: "B", Meta: map[string]string{"community": "one"}},
			{ID: "c", Kind: graph.NodeFunction, Name: "C", Meta: map[string]string{"community": "two"}},
			{ID: "d", Kind: graph.NodeFunction, Name: "D", Meta: map[string]string{"community": "two"}},
		},
		Edges: []graph.Edge{
			{ID: "calls-a-b", Kind: graph.EdgeCalls, From: "a", To: "b"},
			{ID: "imports-c-a", Kind: graph.EdgeImports, From: "c", To: "a"},
			{ID: "references-a-a", Kind: graph.EdgeReferences, From: "a", To: "a"},
			{ID: "calls-d-a", Kind: graph.EdgeCalls, From: "d", To: "a"},
			{ID: "calls-a-c", Kind: graph.EdgeCalls, From: "a", To: "c"},
			{ID: "invalid", Kind: graph.EdgeCalls, From: "a", To: "missing"},
		},
	}
	idx := NewIndex(g)
	scores := map[string]int{"b": 30, "c": 20, "d": 10}
	cases := []struct {
		name    string
		options normalizedRetrieveOptions
	}{
		{name: "out", options: normalizedRetrieveOptions{RetrieveOptions: RetrieveOptions{Direction: DirectionOut}}},
		{name: "in calls", options: normalizedRetrieveOptions{
			RetrieveOptions: RetrieveOptions{Direction: DirectionIn},
			filterEdges:     true, relationSet: map[graph.EdgeKind]bool{graph.EdgeCalls: true},
		}},
		{name: "both prefer incoming with community", options: normalizedRetrieveOptions{
			RetrieveOptions:     RetrieveOptions{Direction: DirectionBoth, CommunityBoost: true},
			directionPreference: DirectionIn,
		}},
		{name: "both calls prefer outgoing", options: normalizedRetrieveOptions{
			RetrieveOptions: RetrieveOptions{Direction: DirectionBoth},
			filterEdges:     true, relationSet: map[graph.EdgeKind]bool{graph.EdgeCalls: true},
			directionPreference: DirectionOut,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantAdjacency, wantDegree := legacyFilteredAdjacency(idx, tc.options, scores)
			got := idx.newQueryAdjacency(tc.options, scores)
			for docIndex, doc := range idx.docs {
				if got.degree[docIndex] != wantDegree[doc.node.ID] {
					t.Fatalf("degree[%q] = %d, want %d", doc.node.ID, got.degree[docIndex], wantDegree[doc.node.ID])
				}
				gotEdges := make([]string, 0)
				for _, adjacent := range got.neighborsOf(doc.node.ID) {
					gotEdges = append(gotEdges, adjacencyIdentity(got.nodeID(adjacent), adjacent.edge.ID, adjacent.outgoing))
				}
				wantEdges := make([]string, 0, len(wantAdjacency[doc.node.ID]))
				for _, adjacent := range wantAdjacency[doc.node.ID] {
					wantEdges = append(wantEdges, adjacencyIdentity(adjacent.nodeID, adjacent.edge.ID, adjacent.outgoing))
				}
				if !reflect.DeepEqual(gotEdges, wantEdges) {
					t.Fatalf("neighbors[%q] = %v, want %v", doc.node.ID, gotEdges, wantEdges)
				}
			}
		})
	}
}

func TestQueryAdjacencySortsOnlyExpandedNodes(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "root", Kind: graph.NodeFunction, Name: "Root"},
			{ID: "child", Kind: graph.NodeFunction, Name: "Child"},
			{ID: "grandchild", Kind: graph.NodeFunction, Name: "Grandchild"},
			{ID: "disconnected", Kind: graph.NodeFunction, Name: "Disconnected"},
		},
		Edges: []graph.Edge{
			{ID: "root-child", Kind: graph.EdgeCalls, From: "root", To: "child"},
			{ID: "child-grandchild", Kind: graph.EdgeCalls, From: "child", To: "grandchild"},
		},
	}
	idx := NewIndex(g)
	options := normalizedRetrieveOptions{RetrieveOptions: RetrieveOptions{
		Traversal: TraversalBFS, Direction: DirectionOut, MaxDepth: 1, MaxNodes: 10,
	}}
	adjacency := idx.newQueryAdjacency(options, nil)
	idx.traverse([]string{"root"}, map[string]bool{"root": true}, adjacency, options)
	if got := len(adjacency.neighbors); got != 1 {
		t.Fatalf("sorted neighbor lists = %d, want only the expanded root", got)
	}
}

func TestHubThresholdUsesExactNinetyNinthPercentile(t *testing.T) {
	degree := make([]int, 100)
	degree[98] = 51
	degree[99] = 100
	if got := hubThreshold(degree, 0); got != 51 {
		t.Fatalf("hubThreshold() = %d, want 51", got)
	}
}

func adjacencyIdentity(nodeID, edgeID string, outgoing bool) string {
	if outgoing {
		return "out:" + nodeID + ":" + edgeID
	}
	return "in:" + nodeID + ":" + edgeID
}

func legacyFilteredAdjacency(idx *Index, options normalizedRetrieveOptions, scores map[string]int) (map[string][]legacyAdjacentEdge, map[string]int) {
	adjacency := map[string][]legacyAdjacentEdge{}
	add := func(from string, edge legacyAdjacentEdge) {
		if options.filterEdges && !options.relationSet[edge.edge.Kind] {
			return
		}
		adjacency[from] = append(adjacency[from], edge)
	}
	for _, edge := range idx.graph.Edges {
		if _, fromOK := idx.byID[edge.From]; !fromOK {
			continue
		}
		if _, toOK := idx.byID[edge.To]; !toOK {
			continue
		}
		switch options.Direction {
		case DirectionOut:
			add(edge.From, legacyAdjacentEdge{nodeID: edge.To, edge: edge, outgoing: true})
		case DirectionIn:
			add(edge.To, legacyAdjacentEdge{nodeID: edge.From, edge: edge, outgoing: false})
		case DirectionBoth:
			add(edge.From, legacyAdjacentEdge{nodeID: edge.To, edge: edge, outgoing: true})
			if edge.From != edge.To {
				add(edge.To, legacyAdjacentEdge{nodeID: edge.From, edge: edge, outgoing: false})
			}
		}
	}
	degree := map[string]int{}
	for _, doc := range idx.docs {
		degree[doc.node.ID] = len(adjacency[doc.node.ID])
	}
	for nodeID := range adjacency {
		sort.SliceStable(adjacency[nodeID], func(i, j int) bool {
			left := adjacency[nodeID][i]
			right := adjacency[nodeID][j]
			if options.directionPreference != "" && left.outgoing != right.outgoing {
				if options.directionPreference == DirectionOut {
					return left.outgoing
				}
				return !left.outgoing
			}
			if scores[left.nodeID] != scores[right.nodeID] {
				return scores[left.nodeID] > scores[right.nodeID]
			}
			if relationPriority(left.edge.Kind) != relationPriority(right.edge.Kind) {
				return relationPriority(left.edge.Kind) < relationPriority(right.edge.Kind)
			}
			if degree[left.nodeID] != degree[right.nodeID] {
				return degree[left.nodeID] < degree[right.nodeID]
			}
			if options.CommunityBoost {
				origin := idx.nodeCommunity(nodeID)
				leftSame := origin != "" && idx.nodeCommunity(left.nodeID) == origin
				rightSame := origin != "" && idx.nodeCommunity(right.nodeID) == origin
				if leftSame != rightSame {
					return leftSame
				}
			}
			if left.nodeID != right.nodeID {
				return left.nodeID < right.nodeID
			}
			return left.edge.ID < right.edge.ID
		})
	}
	return adjacency, degree
}
