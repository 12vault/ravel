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
	needles := queryTerms(term)
	if len(needles) == 0 {
		return nil
	}
	var out []SearchResult
	for _, n := range g.Nodes {
		score := 0
		for _, needle := range needles {
			score += nodeScore(n, needle)
		}
		if score == 0 {
			continue
		}
		out = append(out, SearchResult{Node: n, Score: score})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].Node.ID < out[j].Node.ID
		}
		return out[i].Score > out[j].Score
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
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
		fmt.Fprintf(w, "%s\t%s\t%s", r.Node.Kind, r.Node.ID, display(r.Node))
		if r.Node.Path != "" && !strings.Contains(display(r.Node), r.Node.Path) {
			fmt.Fprintf(w, "\t%s", r.Node.Path)
		}
		fmt.Fprintln(w)
	}
	return nil
}

func Explain(g graph.Graph, target string) (Explanation, bool) {
	n, ok := FindBest(g, target)
	if !ok {
		return Explanation{}, false
	}
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
	fmt.Fprintf(w, "%s: %s\n", ex.Target.Kind, display(ex.Target))
	if ex.Target.Path != "" {
		fmt.Fprintf(w, "Path: %s", ex.Target.Path)
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
	from, ok := FindBest(g, fromQuery)
	if !ok {
		return nil, false
	}
	to, ok := FindBest(g, toQuery)
	if !ok {
		return nil, false
	}
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
		fmt.Fprintf(w, "%s\t%s\n", n.Kind, display(n))
	}
	return nil
}

func FindBest(g graph.Graph, query string) (graph.Node, bool) {
	q := strings.TrimSpace(query)
	if q == "" {
		return graph.Node{}, false
	}
	for _, n := range g.Nodes {
		if n.ID == q || n.Path == q || n.Name == q {
			return n, true
		}
	}
	needles := queryTerms(q)
	var matches []SearchResult
	for _, n := range g.Nodes {
		score := 0
		for _, needle := range needles {
			score += nodeScore(n, needle)
		}
		if score > 0 {
			matches = append(matches, SearchResult{Node: n, Score: score})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return matches[i].Node.ID < matches[j].Node.ID
		}
		return matches[i].Score > matches[j].Score
	})
	if len(matches) == 0 {
		return graph.Node{}, false
	}
	return matches[0].Node, true
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

func nodeScore(n graph.Node, needle string) int {
	score := 0
	lowerName := strings.ToLower(n.Name)
	lowerPath := strings.ToLower(n.Path)
	lowerPackage := strings.ToLower(n.Package)
	lowerID := strings.ToLower(n.ID)
	switch {
	case lowerName == needle:
		score += 100
	case strings.Contains(lowerName, needle):
		score += 50
	}
	if lowerPath == needle {
		score += 90
	} else if strings.Contains(lowerPath, needle) {
		score += 35
	}
	if lowerPackage == needle {
		score += 60
	} else if strings.Contains(lowerPackage, needle) {
		score += 20
	}
	if strings.Contains(lowerID, needle) {
		score += 10
	}
	for k, v := range n.Meta {
		if strings.Contains(strings.ToLower(k), needle) || strings.Contains(strings.ToLower(v), needle) {
			score += 5
		}
	}
	return score
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
		fmt.Fprintf(w, "- %s\t%s\n", n.Kind, display(n))
	}
}

func writeRelationSection(w io.Writer, title string, relations []Relation) {
	if len(relations) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s:\n", title)
	for _, relation := range relations {
		fmt.Fprintf(w, "- %s\t%s\n", relation.Kind, display(relation.Node))
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

func queryTerms(value string) []string {
	stop := map[string]bool{"a": true, "an": true, "and": true, "are": true, "do": true, "does": true, "how": true, "in": true, "is": true, "of": true, "the": true, "to": true, "what": true, "where": true, "which": true, "with": true}
	seen := map[string]bool{}
	var terms []string
	for _, field := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}) {
		if len(field) < 2 || stop[field] || seen[field] {
			continue
		}
		seen[field] = true
		terms = append(terms, field)
	}
	return terms
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
