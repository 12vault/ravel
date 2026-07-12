package query

import (
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/12vault/ravel/internal/graph"
)

const (
	bm25K1               = 1.2
	bm25B                = 0.75
	candidateGuard       = 0.10
	exactPhraseBonus     = 10_000.0
	prefixPhraseBonus    = 1_000.0
	exactNameBonus       = 1_000.0
	qualifiedNameBonus   = 10_000.0
	compoundNameBonus    = 1_000.0
	prefixNameBonus      = 100.0
	substringNameBonus   = 1.0
	substringSourceBonus = 0.5
)

// Index is an immutable, reusable query index over a graph. It keeps retrieval
// state out of graph.json while avoiding repeated normalization and adjacency
// construction for benchmark suites and long-running integrations.
type Index struct {
	graph         graph.Graph
	docs          []indexedNode
	byID          map[string]int
	communityByID map[string]string

	documentFrequency map[string]int
	trigramPostings   map[string][]int
	averageLength     fieldLengths

	edgeKinds map[graph.EdgeKind]bool

	idfMu    sync.RWMutex
	idfCache map[string]float64
}

type indexedNode struct {
	node graph.Node

	nameText    string
	pathText    string
	packageText string
	idText      string
	metaText    string
	searchText  string

	nameTerms    map[string]int
	pathTerms    map[string]int
	packageTerms map[string]int
	idTerms      map[string]int
	metaTerms    map[string]int
	allTerms     map[string]bool
	lengths      fieldLengths
}

type fieldLengths struct {
	name, path, packageName, id, meta float64
}

type adjacentEdge struct {
	nodeID   string
	edge     graph.Edge
	outgoing bool
}

type rankedNode struct {
	index        int
	score        float64
	matchedTerms map[string]bool
}

// NewIndex constructs a deterministic, in-memory retrieval index.
func NewIndex(g graph.Graph) *Index {
	g = cloneGraph(g)
	idx := &Index{
		graph:             g,
		byID:              make(map[string]int, len(g.Nodes)),
		communityByID:     make(map[string]string, len(g.Nodes)),
		documentFrequency: map[string]int{},
		trigramPostings:   map[string][]int{},
		edgeKinds:         map[graph.EdgeKind]bool{},
		idfCache:          map[string]float64{},
	}
	for _, node := range g.Nodes {
		doc := indexNode(node)
		idx.byID[node.ID] = len(idx.docs)
		if node.Meta != nil {
			idx.communityByID[node.ID] = node.Meta["community"]
		}
		idx.docs = append(idx.docs, doc)
		idx.averageLength.name += doc.lengths.name
		idx.averageLength.path += doc.lengths.path
		idx.averageLength.packageName += doc.lengths.packageName
		idx.averageLength.id += doc.lengths.id
		idx.averageLength.meta += doc.lengths.meta
		for term := range doc.allTerms {
			idx.documentFrequency[term]++
		}
		for trigram := range trigrams(doc.searchText) {
			idx.trigramPostings[trigram] = append(idx.trigramPostings[trigram], len(idx.docs)-1)
		}
	}
	if count := float64(len(idx.docs)); count > 0 {
		idx.averageLength.name /= count
		idx.averageLength.path /= count
		idx.averageLength.packageName /= count
		idx.averageLength.id /= count
		idx.averageLength.meta /= count
	}
	for _, edge := range g.Edges {
		if _, fromOK := idx.byID[edge.From]; !fromOK {
			continue
		}
		if _, toOK := idx.byID[edge.To]; !toOK {
			continue
		}
		idx.edgeKinds[edge.Kind] = true
	}
	return idx
}

// Search performs ranked lexical retrieval without graph expansion.
func (idx *Index) Search(text string, limit int) []SearchResult {
	ranked := idx.rank(text)
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	results := make([]SearchResult, 0, len(ranked))
	for _, item := range ranked {
		scaled := int(math.Round(item.score * 1_000))
		if scaled < 1 {
			scaled = 1
		}
		results = append(results, SearchResult{Node: cloneNode(idx.docs[item.index].node), Score: scaled})
	}
	return results
}

// FindBest resolves exact IDs, paths, and names before falling back to ranked
// retrieval. Exact matching remains stable for explain and path compatibility.
func (idx *Index) FindBest(query string) (graph.Node, bool) {
	value := strings.TrimSpace(query)
	if value == "" {
		return graph.Node{}, false
	}
	for i := range idx.docs {
		node := idx.docs[i].node
		if node.ID == value || node.Path == value || node.Name == value {
			return cloneNode(node), true
		}
	}
	results := idx.Search(value, 1)
	if len(results) == 0 {
		return graph.Node{}, false
	}
	return cloneNode(results[0].Node), true
}

func (idx *Index) rank(text string) []rankedNode {
	return idx.rankWithAnchors(text, text)
}

func (idx *Index) rankWithAnchors(text, anchorText string) []rankedNode {
	terms := queryTerms(text)
	if len(terms) == 0 {
		return nil
	}
	candidates := idx.candidates(terms)
	phrase := strings.Join(terms, " ")
	rawTermCounts := termCounts(searchTokens(anchorText))
	ranked := make([]rankedNode, 0, len(candidates))
	for _, docIndex := range candidates {
		doc := &idx.docs[docIndex]
		score, matched := idx.score(doc, terms, phrase, rawTermCounts)
		if score > 0 {
			ranked = append(ranked, rankedNode{index: docIndex, score: score, matchedTerms: matched})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		left := idx.docs[ranked[i].index].node
		right := idx.docs[ranked[j].index].node
		if len([]rune(left.Name)) != len([]rune(right.Name)) {
			return len([]rune(left.Name)) < len([]rune(right.Name))
		}
		return left.ID < right.ID
	})
	return ranked
}

func (idx *Index) score(doc *indexedNode, terms []string, phrase string, rawTermCounts map[string]int) (float64, map[string]bool) {
	matched := map[string]bool{}
	score := 0.0
	phraseWeight := 1.0
	for _, term := range terms {
		phraseWeight = math.Max(phraseWeight, idx.idf(term))
	}
	if phrase != "" {
		switch {
		case phrase == doc.nameText || phrase == strings.TrimSuffix(doc.nameText, " ()") || phrase == doc.idText:
			score += exactPhraseBonus * phraseWeight
		case strings.HasPrefix(doc.nameText, phrase) || strings.HasPrefix(doc.idText, phrase):
			score += prefixPhraseBonus * phraseWeight
		}
	}

	tiered := 0.0
	for _, term := range terms {
		idf := idx.idf(term)
		nameMatch := false
		switch {
		case doc.nameText == term || strings.TrimSuffix(doc.nameText, " ()") == term:
			tiered += exactNameBonus * idf
			qualified := rawTermCounts[term] > 1 && (doc.pathTerms[term] > 0 || doc.packageTerms[term] > 0)
			if !qualified {
				for sourceTerm := range rawTermCounts {
					if sourceTerm != term && doc.packageTerms[sourceTerm] > 0 {
						qualified = true
						break
					}
				}
			}
			if graph.SymbolKind(doc.node.Kind) && qualified {
				// A repeated package/path + symbol token (for example
				// "scan Scan"), or a package plus exact symbol (for
				// example "query Search"), is a qualified-name signal.
				// Keep it outside the broad-question coverage penalty so a
				// verbose test name cannot displace the named symbol.
				score += qualifiedNameBonus * idf
			}
			nameMatch = true
		case strings.HasPrefix(doc.nameText, term):
			tiered += prefixNameBonus * idf
			nameMatch = true
		case strings.Contains(doc.nameText, term):
			score += substringNameBonus * idf
			nameMatch = true
		}
		if nameMatch {
			matched[term] = true
		}

		fieldTF := bm25FieldTF(doc.nameTerms[term], doc.lengths.name, idx.averageLength.name, 5) +
			bm25FieldTF(doc.pathTerms[term], doc.lengths.path, idx.averageLength.path, 2) +
			bm25FieldTF(doc.packageTerms[term], doc.lengths.packageName, idx.averageLength.packageName, 2) +
			bm25FieldTF(doc.idTerms[term], doc.lengths.id, idx.averageLength.id, 1) +
			bm25FieldTF(doc.metaTerms[term], doc.lengths.meta, idx.averageLength.meta, 1)
		if fieldTF > 0 {
			score += idf * ((fieldTF * (bm25K1 + 1)) / (fieldTF + bm25K1))
			matched[term] = true
		} else {
			if strings.Contains(doc.pathText, term) || strings.Contains(doc.packageText, term) || strings.Contains(doc.idText, term) {
				score += substringSourceBonus * idf
				matched[term] = true
			}
			if strings.Contains(doc.metaText, term) {
				score += 0.25 * idf
				matched[term] = true
			}
		}
	}
	if len(doc.nameTerms) > 1 && len(doc.nameTerms) <= len(terms) {
		querySet := map[string]bool{}
		for _, term := range terms {
			querySet[term] = true
		}
		allNameTermsMatched := true
		nameWeight := 0.0
		for term := range doc.nameTerms {
			if !querySet[term] {
				allNameTermsMatched = false
				break
			}
			nameWeight += idx.idf(term)
		}
		if allNameTermsMatched {
			score += compoundNameBonus * nameWeight
		}
	}
	coverage := float64(len(matched)) / float64(len(terms))
	if tiered > 0 {
		score += tiered * coverage * coverage
	}
	// Prefer nodes that explain more of a natural-language question without
	// weakening exact single-identifier lookup.
	if len(terms) > 1 {
		score *= 0.5 + coverage*coverage
	}
	return score, matched
}

func bm25FieldTF(count int, length, average, weight float64) float64 {
	if count == 0 {
		return 0
	}
	if average < 1 {
		average = 1
	}
	normalized := float64(count) / (1 - bm25B + bm25B*(length/average))
	return normalized * weight
}

func (idx *Index) idf(term string) float64 {
	idx.idfMu.RLock()
	value, ok := idx.idfCache[term]
	idx.idfMu.RUnlock()
	if ok {
		return value
	}
	df := idx.documentFrequency[term]
	if df == 0 {
		for i := range idx.docs {
			if strings.Contains(idx.docs[i].nameText, term) {
				df++
			}
		}
	}
	value = math.Log(1 + float64(max(1, len(idx.docs)))/float64(1+df))
	idx.idfMu.Lock()
	idx.idfCache[term] = value
	idx.idfMu.Unlock()
	return value
}

func (idx *Index) candidates(terms []string) []int {
	all := func() []int {
		result := make([]int, len(idx.docs))
		for i := range result {
			result[i] = i
		}
		return result
	}
	if len(idx.docs) == 0 {
		return nil
	}
	union := map[int]bool{}
	for _, term := range terms {
		termTrigrams := trigrams(term)
		if len([]rune(term)) < 3 || len(termTrigrams) == 0 {
			return all()
		}
		var intersection map[int]bool
		keys := make([]string, 0, len(termTrigrams))
		for key := range termTrigrams {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			return len(idx.trigramPostings[keys[i]]) < len(idx.trigramPostings[keys[j]])
		})
		for _, key := range keys {
			posting := idx.trigramPostings[key]
			if len(posting) == 0 {
				intersection = nil
				break
			}
			if intersection == nil {
				intersection = make(map[int]bool, len(posting))
				for _, item := range posting {
					intersection[item] = true
				}
				continue
			}
			present := make(map[int]bool, len(posting))
			for _, item := range posting {
				present[item] = true
			}
			for item := range intersection {
				if !present[item] {
					delete(intersection, item)
				}
			}
		}
		for item := range intersection {
			union[item] = true
		}
	}
	if len(union) == 0 {
		return nil
	}
	if float64(len(union)) > float64(len(idx.docs))*candidateGuard {
		return all()
	}
	result := make([]int, 0, len(union))
	for item := range union {
		result = append(result, item)
	}
	sort.Ints(result)
	return result
}

func indexNode(node graph.Node) indexedNode {
	name := normalizeSearchText(node.Name)
	path := normalizeSearchText(node.Path)
	packageName := normalizeSearchText(node.Package)
	id := normalizeSearchText(node.ID)
	meta := normalizeSearchText(searchableMeta(node.Meta))
	doc := indexedNode{
		node:         node,
		nameText:     name,
		pathText:     path,
		packageText:  packageName,
		idText:       id,
		metaText:     meta,
		nameTerms:    termCounts(searchTokens(node.Name)),
		pathTerms:    termCounts(searchTokens(node.Path)),
		packageTerms: termCounts(searchTokens(node.Package)),
		idTerms:      termCounts(searchTokens(node.ID)),
		metaTerms:    termCounts(searchTokens(meta)),
		allTerms:     map[string]bool{},
	}
	doc.searchText = strings.Join([]string{name, path, packageName, id, meta}, " ")
	for term := range doc.nameTerms {
		doc.allTerms[term] = true
	}
	for term := range doc.pathTerms {
		doc.allTerms[term] = true
	}
	for term := range doc.packageTerms {
		doc.allTerms[term] = true
	}
	for term := range doc.idTerms {
		doc.allTerms[term] = true
	}
	for term := range doc.metaTerms {
		doc.allTerms[term] = true
	}
	doc.lengths = fieldLengths{
		name:        countTerms(doc.nameTerms),
		path:        countTerms(doc.pathTerms),
		packageName: countTerms(doc.packageTerms),
		id:          countTerms(doc.idTerms),
		meta:        countTerms(doc.metaTerms),
	}
	return doc
}

func searchableMeta(meta map[string]string) string {
	if len(meta) == 0 {
		return ""
	}
	ignored := map[string]bool{
		"confidence": true, "evidence": true, "rationale": true, "hash": true,
		"sourcehash": true, "sourcehashes": true, "resolved": true, "line": true,
		"path": true, "size": true,
		"community": true, "communitysize": true,
		"communitygranularity": true, "communityhubthreshold": true,
		"communitydescriptionsource": true, "communitydescriptionconfidence": true, "communitydescriptionrationale": true,
	}
	keys := make([]string, 0, len(meta))
	for key := range meta {
		if !ignored[strings.ToLower(key)] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var parts []string
	for _, key := range keys {
		parts = append(parts, key, meta[key])
	}
	return strings.Join(parts, " ")
}

func queryTerms(value string) []string {
	stop := map[string]bool{
		"a": true, "about": true, "all": true, "an": true, "and": true, "any": true,
		"are": true, "be": true, "been": true, "being": true, "but": true, "can": true,
		"could": true, "did": true, "do": true, "does": true, "for": true, "from": true,
		"had": true, "has": true, "have": true, "here": true, "how": true, "in": true,
		"into": true, "is": true, "its": true, "may": true, "might": true, "must": true,
		"not": true, "of": true, "off": true, "on": true, "onto": true, "or": true,
		"shall": true, "should": true, "some": true, "that": true, "the": true,
		"their": true, "them": true, "there": true, "these": true, "they": true,
		"this": true, "those": true, "to": true, "was": true, "were": true,
		"what": true, "when": true, "where": true, "which": true, "who": true,
		"whom": true, "whose": true, "why": true, "will": true, "with": true,
		"without": true, "work": true, "working": true, "works": true, "would": true,
	}
	seen := map[string]bool{}
	var searchable []string
	var terms []string
	for _, term := range searchTokens(value) {
		if seen[term] {
			continue
		}
		if len([]rune(term)) < 2 && !isEastAsian(term) {
			continue
		}
		seen[term] = true
		searchable = append(searchable, term)
		if !stop[term] {
			terms = append(terms, term)
		}
	}
	if len(terms) == 0 {
		return searchable
	}
	return terms
}

func normalizeSearchText(value string) string {
	return strings.Join(searchTokens(value), " ")
}

func searchTokens(value string) []string {
	runes := []rune(value)
	var normalized strings.Builder
	for i, r := range runes {
		if i > 0 && isIdentifierBoundary(runes[i-1], r, nextRune(runes, i)) {
			normalized.WriteByte(' ')
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			normalized.WriteRune(unicode.ToLower(r))
		} else {
			normalized.WriteByte(' ')
		}
	}
	fields := strings.Fields(normalized.String())
	var tokens []string
	for _, field := range fields {
		tokens = append(tokens, field)
		if isEastAsian(field) {
			characters := []rune(field)
			for i := 0; i+1 < len(characters); i++ {
				tokens = append(tokens, string(characters[i:i+2]))
			}
		}
	}
	return tokens
}

func isIdentifierBoundary(previous, current, next rune) bool {
	return unicode.IsUpper(current) && (unicode.IsLower(previous) || unicode.IsDigit(previous) || (unicode.IsUpper(previous) && unicode.IsLower(next)))
}

func nextRune(runes []rune, index int) rune {
	if index+1 >= len(runes) {
		return 0
	}
	return runes[index+1]
}

func isEastAsian(value string) bool {
	runes := []rune(value)
	if len(runes) == 0 {
		return false
	}
	for _, r := range runes {
		if !(unicode.In(r, unicode.Han, unicode.Hangul, unicode.Hiragana, unicode.Katakana)) {
			return false
		}
	}
	return true
}

func termCounts(terms []string) map[string]int {
	counts := map[string]int{}
	for _, term := range terms {
		counts[term]++
	}
	return counts
}

func countTerms(terms map[string]int) float64 {
	count := 0
	for _, occurrences := range terms {
		count += occurrences
	}
	return float64(max(1, count))
}

func trigrams(value string) map[string]bool {
	runes := []rune(value)
	result := map[string]bool{}
	if len(runes) == 0 {
		return result
	}
	if len(runes) < 3 {
		result[string(runes)] = true
		return result
	}
	for i := 0; i+2 < len(runes); i++ {
		result[string(runes[i:i+3])] = true
	}
	return result
}

func cloneGraph(g graph.Graph) graph.Graph {
	cloned := g
	cloned.Nodes = make([]graph.Node, len(g.Nodes))
	for i, node := range g.Nodes {
		cloned.Nodes[i] = cloneNode(node)
	}
	cloned.Edges = make([]graph.Edge, len(g.Edges))
	for i, edge := range g.Edges {
		cloned.Edges[i] = edge
		cloned.Edges[i].Meta = cloneStringMap(edge.Meta)
	}
	cloned.Diagnostics = append([]graph.Diagnostic(nil), g.Diagnostics...)
	cloned.Metrics.NodesByKind = make(map[graph.NodeKind]int, len(g.Metrics.NodesByKind))
	for kind, count := range g.Metrics.NodesByKind {
		cloned.Metrics.NodesByKind[kind] = count
	}
	cloned.Metrics.EdgesByKind = make(map[graph.EdgeKind]int, len(g.Metrics.EdgesByKind))
	for kind, count := range g.Metrics.EdgesByKind {
		cloned.Metrics.EdgesByKind[kind] = count
	}
	cloned.Metrics.Languages = cloneIntMap(g.Metrics.Languages)
	return cloned
}

func cloneNode(node graph.Node) graph.Node {
	cloned := node
	cloned.Meta = cloneStringMap(node.Meta)
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneIntMap(values map[string]int) map[string]int {
	if values == nil {
		return nil
	}
	cloned := make(map[string]int, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
