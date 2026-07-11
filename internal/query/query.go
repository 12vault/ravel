package query

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/12ya/reporavel/internal/graph"
)

type SearchResult struct {
	Node  graph.Node `json:"node"`
	Score int        `json:"score"`
}

type Explanation struct {
	Target    graph.Node   `json:"target"`
	Defines   []graph.Node `json:"defines,omitempty"`
	Imports   []graph.Node `json:"imports,omitempty"`
	Calls     []graph.Node `json:"calls,omitempty"`
	CalledBy  []graph.Node `json:"calledBy,omitempty"`
	Contained []graph.Node `json:"contained,omitempty"`
	DefinedIn []graph.Node `json:"definedIn,omitempty"`
	Outgoing  []Relation   `json:"outgoing,omitempty"`
	Incoming  []Relation   `json:"incoming,omitempty"`
}

type Relation struct {
	Kind graph.EdgeKind    `json:"kind"`
	Node graph.Node        `json:"node"`
	Meta map[string]string `json:"meta,omitempty"`
}

func Search(g graph.Graph, term string, limit int) []SearchResult {
	return NewIndex(g).Search(term, limit)
}

func WriteSearch(w io.Writer, results []SearchResult, jsonOut bool) error {
	if jsonOut {
		return writeJSON(w, results)
	}
	if len(results) == 0 {
		_, err := fmt.Fprintln(w, "No matches.")
		return err
	}
	for _, r := range results {
		fmt.Fprintf(w, "%s\t%s\t%s", safeText(string(r.Node.Kind)), compactID(r.Node.ID), safeText(display(r.Node)))
		if r.Node.Path != "" && !strings.Contains(display(r.Node), r.Node.Path) {
			fmt.Fprintf(w, "\t%s", safeText(r.Node.Path))
		}
		fmt.Fprintln(w)
	}
	return nil
}

func Explain(g graph.Graph, target string) (Explanation, bool) {
	return NewIndex(g).Explain(target)
}

// Explain returns the immediate relationships for the best matching target
// while reusing the index's immutable graph snapshot.
func (idx *Index) Explain(target string) (Explanation, bool) {
	n, ok := idx.FindBest(target)
	if !ok {
		return Explanation{}, false
	}
	g := idx.graph
	byID := nodesByID(g)
	ex := Explanation{Target: n}
	for _, e := range g.Edges {
		if e.From == n.ID {
			if related := byID[e.To]; related.ID != "" {
				ex.Outgoing = append(ex.Outgoing, Relation{Kind: e.Kind, Node: related, Meta: e.Meta})
			}
			switch e.Kind {
			case graph.EdgeDefines:
				ex.Defines = appendNode(ex.Defines, byID[e.To])
			case graph.EdgeImports:
				ex.Imports = appendNode(ex.Imports, byID[e.To])
			case graph.EdgeCalls:
				ex.Calls = appendNode(ex.Calls, byID[e.To])
			case graph.EdgeContains:
				ex.Contained = appendNode(ex.Contained, byID[e.To])
			}
		}
		if e.To == n.ID {
			if related := byID[e.From]; related.ID != "" {
				ex.Incoming = append(ex.Incoming, Relation{Kind: e.Kind, Node: related, Meta: e.Meta})
			}
			switch e.Kind {
			case graph.EdgeCalls:
				ex.CalledBy = appendNode(ex.CalledBy, byID[e.From])
			case graph.EdgeDefines:
				ex.DefinedIn = appendNode(ex.DefinedIn, byID[e.From])
			}
		}
	}
	sortExplanation(&ex)
	return ex, true
}

func WriteExplanation(w io.Writer, ex Explanation, jsonOut bool) error {
	if jsonOut {
		return writeJSON(w, ex)
	}
	fmt.Fprintf(w, "%s: %s\n", safeText(string(ex.Target.Kind)), safeText(display(ex.Target)))
	if ex.Target.Path != "" {
		fmt.Fprintf(w, "Path: %s", safeText(ex.Target.Path))
		if ex.Target.StartLine > 0 {
			fmt.Fprintf(w, ":%d", ex.Target.StartLine)
		}
		fmt.Fprintln(w)
	}
	writeNodeSection(w, "Defines", ex.Defines)
	writeNodeSection(w, "Imports", ex.Imports)
	writeNodeSection(w, "Calls", ex.Calls)
	writeNodeSection(w, "Called by", ex.CalledBy)
	writeNodeSection(w, "Contained", ex.Contained)
	writeNodeSection(w, "Defined in", ex.DefinedIn)
	writeRelationSection(w, "Outgoing relationships", ex.Outgoing)
	writeRelationSection(w, "Incoming relationships", ex.Incoming)
	return nil
}

func ShortestPath(g graph.Graph, fromQuery, toQuery string) ([]graph.Node, bool) {
	return NewIndex(g).ShortestPath(fromQuery, toQuery)
}

// ShortestPath finds a directed path first and then an undirected fallback
// while reusing the index's immutable graph snapshot.
func (idx *Index) ShortestPath(fromQuery, toQuery string) ([]graph.Node, bool) {
	from, fromOK := idx.FindBest(fromQuery)
	to, toOK := idx.FindBest(toQuery)
	if !fromOK || !toOK {
		return nil, false
	}
	g := idx.graph
	if path, ok := bfs(g, from.ID, to.ID, true); ok {
		return path, true
	}
	return bfs(g, from.ID, to.ID, false)
}

func WritePath(w io.Writer, nodes []graph.Node, jsonOut bool) error {
	if jsonOut {
		return writeJSON(w, nodes)
	}
	if len(nodes) == 0 {
		_, err := fmt.Fprintln(w, "No path found.")
		return err
	}
	for i, n := range nodes {
		if i > 0 {
			fmt.Fprintln(w, "  ->")
		}
		fmt.Fprintf(w, "%s\t%s\n", safeText(string(n.Kind)), safeText(display(n)))
	}
	return nil
}

func FindBest(g graph.Graph, query string) (graph.Node, bool) {
	if node, ok := exactMatch(g, query); ok {
		return node, true
	}
	return NewIndex(g).FindBest(query)
}

func exactMatch(g graph.Graph, query string) (graph.Node, bool) {
	value := strings.TrimSpace(query)
	if value == "" {
		return graph.Node{}, false
	}
	for _, node := range g.Nodes {
		if node.ID == value || node.Path == value || node.Name == value {
			return node, true
		}
	}
	return graph.Node{}, false
}

func bfs(g graph.Graph, fromID, toID string, directed bool) ([]graph.Node, bool) {
	byID := nodesByID(g)
	adj := map[string][]string{}
	for _, e := range g.Edges {
		adj[e.From] = append(adj[e.From], e.To)
		if !directed {
			adj[e.To] = append(adj[e.To], e.From)
		}
	}
	queue := []string{fromID}
	prev := map[string]string{fromID: ""}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == toID {
			var ids []string
			for id := toID; id != ""; id = prev[id] {
				ids = append(ids, id)
			}
			for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
				ids[i], ids[j] = ids[j], ids[i]
			}
			nodes := make([]graph.Node, 0, len(ids))
			for _, id := range ids {
				nodes = append(nodes, byID[id])
			}
			return nodes, true
		}
		for _, next := range adj[cur] {
			if _, seen := prev[next]; seen {
				continue
			}
			prev[next] = cur
			queue = append(queue, next)
		}
	}
	return nil, false
}

func nodesByID(g graph.Graph) map[string]graph.Node {
	out := map[string]graph.Node{}
	for _, n := range g.Nodes {
		out[n.ID] = n
	}
	return out
}

func appendNode(nodes []graph.Node, n graph.Node) []graph.Node {
	if n.ID == "" {
		return nodes
	}
	for _, existing := range nodes {
		if existing.ID == n.ID {
			return nodes
		}
	}
	return append(nodes, n)
}

func sortExplanation(ex *Explanation) {
	sortNodes(ex.Defines)
	sortNodes(ex.Imports)
	sortNodes(ex.Calls)
	sortNodes(ex.CalledBy)
	sortNodes(ex.Contained)
	sortNodes(ex.DefinedIn)
	sortRelations(ex.Outgoing)
	sortRelations(ex.Incoming)
}

func sortNodes(nodes []graph.Node) {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
}

func writeNodeSection(w io.Writer, title string, nodes []graph.Node) {
	if len(nodes) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s:\n", title)
	for _, n := range nodes {
		fmt.Fprintf(w, "- %s\t%s\n", safeText(string(n.Kind)), safeText(display(n)))
	}
}

func writeRelationSection(w io.Writer, title string, relations []Relation) {
	if len(relations) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s:\n", title)
	for _, relation := range relations {
		fmt.Fprintf(w, "- %s\t%s\n", safeText(string(relation.Kind)), safeText(display(relation.Node)))
	}
}

func sortRelations(relations []Relation) {
	sort.Slice(relations, func(i, j int) bool {
		if relations[i].Kind == relations[j].Kind {
			return relations[i].Node.ID < relations[j].Node.ID
		}
		return relations[i].Kind < relations[j].Kind
	})
}

func display(n graph.Node) string {
	if n.Path != "" && n.StartLine > 0 && graph.SymbolKind(n.Kind) {
		return fmt.Sprintf("%s:%d %s", n.Path, n.StartLine, n.Name)
	}
	if n.Path != "" {
		return n.Path
	}
	return n.Name
}

func writeJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}
