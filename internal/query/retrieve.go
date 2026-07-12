package query

import (
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/12vault/ravel/internal/graph"
)

type Traversal string

const (
	TraversalBFS Traversal = "bfs"
	TraversalDFS Traversal = "dfs"
)

type Direction string

const (
	DirectionOut  Direction = "out"
	DirectionIn   Direction = "in"
	DirectionBoth Direction = "both"
)

const (
	defaultSeedLimit    = 3
	defaultMaxDepth     = 2
	defaultMaxNodes     = 100
	defaultTokenBudget  = 2_000
	minimumTokenBudget  = 128
	maximumTokenBudget  = 100_000
	maximumBranchFanout = 10_000
	// Affected bootstrap seeds include the target plus bounded source/container
	// and symbol nodes. This prevents very large files or packages from turning
	// reverse-impact lookup into an unbounded traversal origin set.
	maximumAffectedBootstrapSeeds = 20
)

// RetrieveOptions controls ranked seeding, graph traversal, and output bounds.
// Zero values select the documented defaults. Set HubDegreeThreshold to -1 to
// disable hub suppression.
type RetrieveOptions struct {
	Traversal                Traversal        `json:"traversal"`
	Direction                Direction        `json:"direction"`
	Relations                []graph.EdgeKind `json:"relations,omitempty"`
	DisableRelationInference bool             `json:"disableRelationInference,omitempty"`
	SeedLimit                int              `json:"seedLimit"`
	MaxDepth                 int              `json:"maxDepth"`
	MaxNodes                 int              `json:"maxNodes"`
	BranchFanout             int              `json:"branchFanout"`
	HubDegreeThreshold       int              `json:"hubDegreeThreshold"`
	TokenBudget              int              `json:"tokenBudget"`
}

type Retrieval struct {
	Version int            `json:"version"`
	Query   string         `json:"query"`
	Nodes   []ContextNode  `json:"nodes"`
	Edges   []ContextEdge  `json:"edges"`
	Stats   RetrievalStats `json:"stats"`
}

type ContextNode struct {
	ID         string         `json:"id"`
	Kind       graph.NodeKind `json:"kind"`
	Name       string         `json:"name"`
	Path       string         `json:"path,omitempty"`
	Package    string         `json:"package,omitempty"`
	StartLine  int            `json:"startLine,omitempty"`
	EndLine    int            `json:"endLine,omitempty"`
	Score      int            `json:"score,omitempty"`
	Depth      int            `json:"depth"`
	Degree     int            `json:"degree"`
	Seed       bool           `json:"seed,omitempty"`
	ViaEdgeID  string         `json:"viaEdgeId,omitempty"`
	Confidence string         `json:"confidence,omitempty"`
	Resolved   *bool          `json:"resolved,omitempty"`
}

type ContextEdge struct {
	ID         string         `json:"id"`
	Kind       graph.EdgeKind `json:"kind"`
	From       string         `json:"from"`
	To         string         `json:"to"`
	Confidence string         `json:"confidence,omitempty"`
	Evidence   string         `json:"evidence,omitempty"`
	Rationale  string         `json:"rationale,omitempty"`
	Path       string         `json:"path,omitempty"`
	Line       int            `json:"line,omitempty"`
	Resolved   *bool          `json:"resolved,omitempty"`
}

type RetrievalStats struct {
	Traversal           Traversal        `json:"traversal"`
	Direction           Direction        `json:"direction"`
	DirectionPreference Direction        `json:"directionPreference,omitempty"`
	Depth               int              `json:"depth"`
	SeedIDs             []string         `json:"seedIds"`
	RelationFilters     []graph.EdgeKind `json:"relationFilters,omitempty"`
	RelationFilterFrom  string           `json:"relationFilterFrom,omitempty"`
	TokenBudget         int              `json:"tokenBudget"`
	EstimatedTokens     int              `json:"estimatedTokens"`
	HubThreshold        int              `json:"hubThreshold,omitempty"`
	BranchFanout        int              `json:"branchFanout,omitempty"`
	HubsSuppressed      int              `json:"hubsSuppressed,omitempty"`
	BranchesPruned      int              `json:"branchesPruned,omitempty"`
	ExploredNodes       int              `json:"exploredNodes"`
	OmittedNodes        int              `json:"omittedNodes,omitempty"`
	OmittedEdges        int              `json:"omittedEdges,omitempty"`
	Truncated           bool             `json:"truncated"`
	TruncatedReason     []string         `json:"truncatedReason,omitempty"`
}

type normalizedRetrieveOptions struct {
	RetrieveOptions
	relationSet         map[graph.EdgeKind]bool
	filterFrom          string
	filterEdges         bool
	directionPreference Direction
}

type traversalResult struct {
	order          []string
	distance       map[string]int
	via            map[string]graph.Edge
	hubThreshold   int
	branchFanout   int
	hubsSuppressed int
	branchesPruned int
	exploredLimit  bool
}

// Retrieve is the graph-level compatibility wrapper around Index.Retrieve.
func Retrieve(g graph.Graph, text string, options RetrieveOptions) (Retrieval, error) {
	return NewIndex(g).Retrieve(text, options)
}

// Affected retrieves dependents for one resolved graph target. Dependency
// relations are traversed in reverse. Explicit affects/flows_to queries are
// traversed forward because those edge kinds already point at affected nodes.
func Affected(g graph.Graph, target string, options RetrieveOptions) (Retrieval, error) {
	return NewIndex(g).Affected(target, options)
}

// Affected is the reusable-index form of impact retrieval.
func (idx *Index) Affected(target string, options RetrieveOptions) (Retrieval, error) {
	node, err := idx.ResolveTarget(target)
	if err != nil {
		return Retrieval{}, fmt.Errorf("affected: %w", err)
	}
	options.Traversal = TraversalBFS
	options.DisableRelationInference = true
	defaultFilter := len(options.Relations) == 0
	if defaultFilter {
		impactKinds := []graph.EdgeKind{
			graph.EdgeCalls, graph.EdgeReferences, graph.EdgeImplements, graph.EdgeInherits,
			graph.EdgeUsesType, graph.EdgeImports, graph.EdgeDependsOn,
		}
		for _, kind := range impactKinds {
			if idx.edgeKinds[kind] {
				options.Relations = append(options.Relations, kind)
			}
		}
	}
	direction, err := affectedDirection(options.Relations)
	if err != nil {
		return Retrieval{}, err
	}
	options.Direction = direction
	maxNodes := options.MaxNodes
	if maxNodes == 0 {
		maxNodes = defaultMaxNodes
	}
	tokenBudget := options.TokenBudget
	if tokenBudget == 0 {
		tokenBudget = defaultTokenBudget
	}
	// Keep at least half of a compact context available for actual dependents.
	// A bootstrapped definition plus its evidence is conservatively budgeted at
	// about 125 tokens, so one seed per 250 tokens leaves comparable headroom.
	budgetSeedLimit := max(2, tokenBudget/250)
	seedLimit := max(1, min(maximumAffectedBootstrapSeeds, maxNodes, budgetSeedLimit))
	seedIDs, seedEvidence := idx.affectedBootstrap(node, seedLimit, idx.affectedImpactCounts(options.Relations, direction))
	options.SeedLimit = min(20, max(1, len(seedIDs)))
	return idx.retrieve(node.ID, options, seedIDs, seedEvidence, defaultFilter)
}

// Retrieve ranks multiple lexical seeds and expands their graph neighborhood.
func (idx *Index) Retrieve(text string, options RetrieveOptions) (Retrieval, error) {
	return idx.retrieve(text, options, nil, nil, false)
}

func (idx *Index) retrieve(text string, options RetrieveOptions, forcedSeedIDs []string, seedEvidence map[string]graph.Edge, forceRelationFilter bool) (Retrieval, error) {
	question := strings.TrimSpace(text)
	if question == "" {
		return Retrieval{}, errors.New("context query must not be empty")
	}
	normalized, err := idx.normalizeRetrieveOptions(question, options)
	if err != nil {
		return Retrieval{}, err
	}
	if forceRelationFilter {
		normalized.filterEdges = true
		normalized.filterFrom = "affected-default"
	}
	terms := retrievalTerms(question, normalized.Relations)
	ranked := idx.rankWithAnchors(strings.Join(terms, " "), question)
	if len(ranked) == 0 && len(forcedSeedIDs) == 0 {
		return Retrieval{
			Version: 1,
			Query:   safeTextBytes(question, 256),
			Stats: RetrievalStats{
				Traversal: normalized.Traversal, Direction: normalized.Direction,
				Depth: normalized.MaxDepth, TokenBudget: normalized.TokenBudget,
				RelationFilters: normalized.Relations, RelationFilterFrom: normalized.filterFrom,
			},
		}, nil
	}
	seedIDs := append([]string(nil), forcedSeedIDs...)
	seedSet := map[string]bool{}
	if len(seedIDs) == 0 {
		seedIndexes := idx.pickSeeds(ranked, terms, normalized.SeedLimit)
		for _, docIndex := range seedIndexes {
			seedIDs = append(seedIDs, idx.docs[docIndex].node.ID)
		}
	}
	for _, id := range seedIDs {
		seedSet[id] = true
	}
	scores := map[string]int{}
	for _, item := range ranked {
		scores[idx.docs[item.index].node.ID] = max(1, int(math.Round(item.score*1_000)))
	}
	for _, id := range seedIDs {
		if scores[id] == 0 {
			scores[id] = 1
		}
	}

	adjacency, degree := idx.filteredAdjacency(normalized, scores)
	walk := idx.traverse(seedIDs, seedSet, adjacency, degree, normalized)
	for seedID, edge := range seedEvidence {
		if seedSet[seedID] {
			walk.via[seedID] = cloneEdge(edge)
		}
	}
	return idx.fitRetrieval(question, normalized, seedIDs, seedSet, scores, degree, walk), nil
}

func affectedDirection(relations []graph.EdgeKind) (Direction, error) {
	forward := false
	reverse := false
	for _, relation := range relations {
		relation = graph.EdgeKind(strings.TrimSpace(string(relation)))
		switch relation {
		case graph.EdgeAffects, graph.EdgeFlowsTo:
			forward = true
		default:
			reverse = true
		}
	}
	if forward && reverse {
		return "", errors.New("affected cannot mix forward-oriented affects/flows_to with reverse dependency relations")
	}
	if forward {
		return DirectionOut, nil
	}
	return DirectionIn, nil
}

type affectedFileBranch struct {
	file        graph.Node
	contains    graph.Edge
	definitions []graph.Edge
	admitted    bool
}

// affectedBootstrap crosses source ownership edges only while selecting
// traversal origins. Those edges are kept as evidence but are not added to the
// reverse-impact relation filter, so containment noise cannot spread further.
func (idx *Index) affectedBootstrap(target graph.Node, limit int, impactCounts map[string]int) ([]string, map[string]graph.Edge) {
	seedIDs := []string{target.ID}
	evidence := map[string]graph.Edge{}
	if limit <= 1 {
		return seedIDs, evidence
	}
	add := func(nodeID string, edge graph.Edge) bool {
		if len(seedIDs) >= limit {
			return false
		}
		if _, ok := idx.byID[nodeID]; !ok {
			return true
		}
		for _, existing := range seedIDs {
			if existing == nodeID {
				return true
			}
		}
		seedIDs = append(seedIDs, nodeID)
		evidence[nodeID] = cloneEdge(edge)
		return true
	}

	directDefinitions := idx.outgoingEdges(target.ID, graph.EdgeDefines)
	sortAffectedDefinitions(directDefinitions, impactCounts)
	if target.Kind != graph.NodePackage && target.Kind != graph.NodeModule && target.Kind != graph.NodeDir {
		for _, edge := range directDefinitions {
			if !add(edge.To, edge) {
				break
			}
		}
		return seedIDs, evidence
	}
	for _, edge := range directDefinitions {
		if !add(edge.To, edge) {
			return seedIDs, evidence
		}
	}

	contains := idx.outgoingEdges(target.ID, graph.EdgeContains)
	branches := make([]affectedFileBranch, 0, len(contains))
	for _, edge := range contains {
		docIndex, ok := idx.byID[edge.To]
		if !ok || idx.docs[docIndex].node.Kind != graph.NodeFile {
			continue
		}
		branches = append(branches, affectedFileBranch{
			file: idx.docs[docIndex].node, contains: edge,
			definitions: idx.outgoingEdges(edge.To, graph.EdgeDefines),
		})
		sortAffectedDefinitions(branches[len(branches)-1].definitions, impactCounts)
	}
	// Cover multiple package files before taking additional symbols from one
	// file. Each admitted file is immediately followed by its first definition,
	// preserving an explanatory package -> file -> symbol chain.
	for branchIndex := range branches {
		branch := &branches[branchIndex]
		if len(branch.definitions) == 0 || len(seedIDs)+2 > limit {
			continue
		}
		add(branch.file.ID, branch.contains)
		add(branch.definitions[0].To, branch.definitions[0])
		branch.definitions = branch.definitions[1:]
		branch.admitted = true
	}
	for definitionIndex := 0; len(seedIDs) < limit; definitionIndex++ {
		added := false
		for _, branch := range branches {
			if !branch.admitted || definitionIndex >= len(branch.definitions) {
				continue
			}
			if !add(branch.definitions[definitionIndex].To, branch.definitions[definitionIndex]) {
				return seedIDs, evidence
			}
			added = true
		}
		if !added {
			break
		}
	}
	return seedIDs, evidence
}

func (idx *Index) affectedImpactCounts(relations []graph.EdgeKind, direction Direction) map[string]int {
	relationSet := make(map[graph.EdgeKind]bool, len(relations))
	for _, relation := range relations {
		relationSet[relation] = true
	}
	counts := map[string]int{}
	for _, edge := range idx.graph.Edges {
		if !relationSet[edge.Kind] {
			continue
		}
		if direction == DirectionOut {
			counts[edge.From]++
		} else {
			counts[edge.To]++
		}
	}
	return counts
}

func sortAffectedDefinitions(definitions []graph.Edge, impactCounts map[string]int) {
	sort.SliceStable(definitions, func(i, j int) bool {
		left, right := impactCounts[definitions[i].To], impactCounts[definitions[j].To]
		if left != right {
			return left > right
		}
		if definitions[i].To != definitions[j].To {
			return definitions[i].To < definitions[j].To
		}
		return stableEdgeID(definitions[i]) < stableEdgeID(definitions[j])
	})
}

func (idx *Index) outgoingEdges(from string, kind graph.EdgeKind) []graph.Edge {
	var result []graph.Edge
	for _, edge := range idx.graph.Edges {
		if edge.From == from && edge.Kind == kind {
			if _, ok := idx.byID[edge.To]; ok {
				result = append(result, cloneEdge(edge))
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].To != result[j].To {
			return result[i].To < result[j].To
		}
		return stableEdgeID(result[i]) < stableEdgeID(result[j])
	})
	return result
}

func (idx *Index) normalizeRetrieveOptions(question string, options RetrieveOptions) (normalizedRetrieveOptions, error) {
	if options.Traversal == "" {
		options.Traversal = TraversalBFS
	}
	if options.Direction == "" {
		options.Direction = DirectionBoth
	}
	if options.SeedLimit == 0 {
		options.SeedLimit = defaultSeedLimit
	}
	if options.MaxDepth == 0 {
		options.MaxDepth = defaultMaxDepth
	}
	if options.MaxNodes == 0 {
		options.MaxNodes = defaultMaxNodes
	}
	if options.TokenBudget == 0 {
		options.TokenBudget = defaultTokenBudget
	}
	if options.Traversal != TraversalBFS && options.Traversal != TraversalDFS {
		return normalizedRetrieveOptions{}, fmt.Errorf("unsupported traversal %q (want bfs or dfs)", options.Traversal)
	}
	if options.Direction != DirectionOut && options.Direction != DirectionIn && options.Direction != DirectionBoth {
		return normalizedRetrieveOptions{}, fmt.Errorf("unsupported direction %q (want out, in, or both)", options.Direction)
	}
	if options.SeedLimit < 1 || options.SeedLimit > 20 {
		return normalizedRetrieveOptions{}, errors.New("seed limit must be between 1 and 20")
	}
	if options.MaxDepth < 1 || options.MaxDepth > 8 {
		return normalizedRetrieveOptions{}, errors.New("max depth must be between 1 and 8")
	}
	if options.MaxNodes < 1 || options.MaxNodes > 10_000 {
		return normalizedRetrieveOptions{}, errors.New("max nodes must be between 1 and 10000")
	}
	if options.BranchFanout < 0 || options.BranchFanout > maximumBranchFanout {
		return normalizedRetrieveOptions{}, fmt.Errorf("branch fanout must be 0 (automatic) or between 1 and %d", maximumBranchFanout)
	}
	if options.HubDegreeThreshold < -1 {
		return normalizedRetrieveOptions{}, errors.New("hub degree threshold must be -1, 0, or a positive integer")
	}
	if options.TokenBudget < minimumTokenBudget || options.TokenBudget > maximumTokenBudget {
		return normalizedRetrieveOptions{}, fmt.Errorf("token budget must be between %d and %d", minimumTokenBudget, maximumTokenBudget)
	}

	result := normalizedRetrieveOptions{RetrieveOptions: options, relationSet: map[graph.EdgeKind]bool{}}
	if options.Direction == DirectionBoth {
		result.directionPreference = inferDirectionPreference(question)
	}
	result.Relations = nil
	seen := map[graph.EdgeKind]bool{}
	for _, relation := range options.Relations {
		relation = graph.EdgeKind(strings.TrimSpace(string(relation)))
		if relation == "" || seen[relation] {
			continue
		}
		if !idx.edgeKinds[relation] {
			return normalizedRetrieveOptions{}, fmt.Errorf("relation %q is not present in this graph (available: %s)", relation, strings.Join(idx.availableEdgeKinds(), ", "))
		}
		seen[relation] = true
		result.Relations = append(result.Relations, relation)
		result.relationSet[relation] = true
	}
	if len(result.Relations) > 0 {
		result.filterFrom = "explicit"
		result.filterEdges = true
		return result, nil
	}
	if !options.DisableRelationInference {
		result.Relations = idx.inferRelations(question)
		for _, relation := range result.Relations {
			result.relationSet[relation] = true
		}
		if len(result.Relations) > 0 {
			result.filterFrom = "inferred"
			result.filterEdges = true
		}
	}
	return result, nil
}

func (idx *Index) availableEdgeKinds() []string {
	result := make([]string, 0, len(idx.edgeKinds))
	for kind := range idx.edgeKinds {
		result = append(result, safeTextBytes(string(kind), 80))
	}
	sort.Strings(result)
	return result
}

func (idx *Index) inferRelations(question string) []graph.EdgeKind {
	terms := map[string]bool{}
	for _, term := range searchTokens(question) {
		terms[term] = true
	}
	groups := []struct {
		hints []string
		kinds []graph.EdgeKind
	}{
		{[]string{"call", "calls", "called", "calling", "caller", "callers", "callee", "callees", "invoke", "invokes", "invoked", "invocation"}, []graph.EdgeKind{graph.EdgeCalls}},
		{[]string{"import", "imports", "imported", "module", "modules", "dependency", "dependencies", "depend", "depends"}, []graph.EdgeKind{graph.EdgeImports, graph.EdgeDependsOn}},
		{[]string{"inherit", "inherits", "extend", "extends", "implement", "implements"}, []graph.EdgeKind{graph.EdgeInherits, graph.EdgeImplements}},
		{[]string{"reference", "references", "referenced", "type", "types", "parameter", "parameters", "argument", "arguments", "return", "returns", "returned"}, []graph.EdgeKind{graph.EdgeReferences, graph.EdgeUsesType}},
		{[]string{"test", "tests", "tested"}, []graph.EdgeKind{graph.EdgeTestedBy}},
		{[]string{"define", "defines", "defined", "contain", "contains", "member", "members", "field", "fields"}, []graph.EdgeKind{graph.EdgeDefines, graph.EdgeContains}},
		{[]string{"cite", "cites", "citation", "explain", "rationale", "why"}, []graph.EdgeKind{graph.EdgeCites, graph.EdgeExplains}},
		{[]string{"flow", "flows", "impact", "impacts", "affect", "affects", "affected"}, []graph.EdgeKind{graph.EdgeFlowsTo, graph.EdgeAffects}},
	}
	seen := map[graph.EdgeKind]bool{}
	var result []graph.EdgeKind
	for _, group := range groups {
		matched := false
		for _, hint := range group.hints {
			if terms[hint] {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		for _, kind := range group.kinds {
			if idx.edgeKinds[kind] && !seen[kind] {
				seen[kind] = true
				result = append(result, kind)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func inferDirectionPreference(question string) Direction {
	text := " " + normalizeSearchText(question) + " "
	terms := map[string]bool{}
	for _, term := range searchTokens(question) {
		terms[term] = true
	}
	hasDirectionalRelation := false
	for _, term := range []string{
		"call", "calls", "called", "caller", "import", "imports", "imported",
		"reference", "references", "referenced", "use", "uses", "used",
		"depend", "depends", "dependency", "dependencies", "implement", "implements",
		"inherit", "inherits", "affect", "affects", "flow", "flows",
	} {
		if terms[term] {
			hasDirectionalRelation = true
			break
		}
	}
	if !hasDirectionalRelation {
		return ""
	}
	incoming := containsAnyPhrase(text,
		" who calls ", " what calls ", " which calls ", " called by ", " callers of ",
		" who imports ", " what imports ", " which imports ", " imported by ",
		" who references ", " what references ", " which references ", " referenced by ",
		" who uses ", " what uses ", " which uses ", " used by ",
		" who depends ", " what depends ", " which depends ", " dependents of ",
		" implemented by ", " inherited by ", " affected by ",
	)
	outgoing := containsAnyPhrase(text,
		" what does ", " which does ", " how does ", " calls from ", " imports from ",
		" references from ", " depends on ", " dependencies of ",
	)
	if incoming == outgoing {
		return ""
	}
	if incoming {
		return DirectionIn
	}
	return DirectionOut
}

func containsAnyPhrase(value string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}

func retrievalTerms(question string, relations []graph.EdgeKind) []string {
	terms := queryTerms(question)
	if len(relations) == 0 {
		return terms
	}
	relationWords := map[string]bool{
		"call": true, "calls": true, "called": true, "calling": true, "caller": true, "callers": true, "callee": true, "callees": true,
		"invoke": true, "invokes": true, "invoked": true, "invocation": true, "import": true, "imports": true, "imported": true,
		"module": true, "modules": true, "dependency": true, "dependencies": true, "depend": true, "depends": true, "inherit": true, "inherits": true,
		"extend": true, "extends": true, "implement": true, "implements": true, "reference": true,
		"references": true, "referenced": true, "parameter": true, "parameters": true, "argument": true, "arguments": true,
		"return": true, "returns": true, "returned": true, "test": true, "tests": true, "tested": true,
		"define": true, "defines": true, "defined": true, "contain": true, "contains": true,
		"cite": true, "cites": true, "citation": true, "explain": true, "flow": true, "flows": true,
		"impact": true, "impacts": true, "affect": true, "affects": true, "affected": true,
	}
	filtered := terms[:0]
	for _, term := range terms {
		if !relationWords[term] {
			filtered = append(filtered, term)
		}
	}
	if len(filtered) == 0 {
		return terms
	}
	return filtered
}

func (idx *Index) pickSeeds(ranked []rankedNode, terms []string, limit int) []int {
	if len(ranked) == 0 {
		return nil
	}
	selected := []int{ranked[0].index}
	selectedSet := map[int]bool{ranked[0].index: true}
	covered := map[string]bool{}
	candidateTerms := make([]map[string]bool, len(ranked))
	for i, candidate := range ranked {
		candidateTerms[i] = idx.seedMatchedTerms(candidate, terms)
	}
	for term := range candidateTerms[0] {
		covered[term] = true
	}
	termOrder := append([]string(nil), terms...)
	sort.SliceStable(termOrder, func(i, j int) bool {
		return idx.idf(termOrder[i]) > idx.idf(termOrder[j])
	})

	// Keep the best global match, then cover informative matched terms before
	// spending any remaining seed slots. Rare terms go first when the hard seed
	// limit cannot represent every independently matched term.
	for _, term := range termOrder {
		if covered[term] || len(selected) >= limit {
			continue
		}
		best := -1
		bestCount := 0
		bestWeight := 0.0
		bestTotal := 0
		bestScore := 0.0
		for candidateIndex, candidate := range ranked {
			if selectedSet[candidate.index] || !candidateTerms[candidateIndex][term] {
				continue
			}
			count := 0
			total := 0
			weight := 0.0
			for _, matchedTerm := range terms {
				if !candidateTerms[candidateIndex][matchedTerm] {
					continue
				}
				total++
				if !covered[matchedTerm] {
					count++
					weight += idx.idf(matchedTerm)
				}
			}
			if count > bestCount ||
				(count == bestCount && weight > bestWeight) ||
				(count == bestCount && weight == bestWeight && total > bestTotal) ||
				(count == bestCount && weight == bestWeight && total == bestTotal && candidate.score > bestScore) {
				best = candidateIndex
				bestCount = count
				bestWeight = weight
				bestTotal = total
				bestScore = candidate.score
			}
		}
		if best < 0 {
			continue
		}
		candidate := ranked[best]
		selected = append(selected, candidate.index)
		selectedSet[candidate.index] = true
		for matched := range candidateTerms[best] {
			covered[matched] = true
		}
	}
	// A one-term query may be genuinely ambiguous, so keep comparable matches.
	// Multi-term questions already earned one seed per viable informative term;
	// filling unused slots with covered-term duplicates creates noisy expansion.
	if len(terms) == 1 {
		threshold := ranked[0].score * 0.20
		for _, candidate := range ranked {
			if len(selected) >= limit || candidate.score < threshold {
				break
			}
			if !selectedSet[candidate.index] {
				selected = append(selected, candidate.index)
				selectedSet[candidate.index] = true
			}
		}
	}
	// Retrieval and serialization expose seeds in global rank order even though
	// coverage, not rank, decides which nodes win the limited seed slots.
	rankOrder := make(map[int]int, len(ranked))
	for position, candidate := range ranked {
		rankOrder[candidate.index] = position
	}
	sort.Slice(selected, func(i, j int) bool {
		return rankOrder[selected[i]] < rankOrder[selected[j]]
	})
	return selected
}

func (idx *Index) seedMatchedTerms(candidate rankedNode, terms []string) map[string]bool {
	doc := idx.docs[candidate.index]
	matched := map[string]bool{}
	for _, term := range terms {
		if strings.Contains(doc.nameText, term) {
			matched[term] = true
		}
	}
	if len(matched) == 0 {
		return candidate.matchedTerms
	}
	return matched
}

func (idx *Index) filteredAdjacency(options normalizedRetrieveOptions, scores map[string]int) (map[string][]adjacentEdge, map[string]int) {
	adjacency := map[string][]adjacentEdge{}
	add := func(from string, edge adjacentEdge) {
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
			add(edge.From, adjacentEdge{nodeID: edge.To, edge: edge, outgoing: true})
		case DirectionIn:
			add(edge.To, adjacentEdge{nodeID: edge.From, edge: edge, outgoing: false})
		case DirectionBoth:
			add(edge.From, adjacentEdge{nodeID: edge.To, edge: edge, outgoing: true})
			if edge.From != edge.To {
				add(edge.To, adjacentEdge{nodeID: edge.From, edge: edge, outgoing: false})
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
			if left.nodeID != right.nodeID {
				return left.nodeID < right.nodeID
			}
			return left.edge.ID < right.edge.ID
		})
	}
	return adjacency, degree
}

func relationPriority(kind graph.EdgeKind) int {
	switch kind {
	case graph.EdgeCalls, graph.EdgeReferences, graph.EdgeImplements, graph.EdgeInherits, graph.EdgeUsesType, graph.EdgeTestedBy:
		return 0
	case graph.EdgeImports, graph.EdgeDependsOn, graph.EdgeAffects, graph.EdgeFlowsTo:
		return 1
	case graph.EdgeDefines, graph.EdgeContains, graph.EdgeBelongsTo, graph.EdgePartOf:
		return 2
	default:
		return 3
	}
}

func (idx *Index) traverse(seedIDs []string, seedSet map[string]bool, adjacency map[string][]adjacentEdge, degree map[string]int, options normalizedRetrieveOptions) traversalResult {
	result := traversalResult{distance: map[string]int{}, via: map[string]graph.Edge{}}
	result.hubThreshold = hubThreshold(degree, options.HubDegreeThreshold)
	result.branchFanout = traversalFanout(options.MaxNodes, len(seedIDs), options.MaxDepth, options.BranchFanout)
	exploreLimit := max(1_000, options.MaxNodes*20)
	exploreLimit = min(exploreLimit, 50_000)
	for _, seed := range seedIDs {
		if _, seen := result.distance[seed]; seen {
			continue
		}
		result.distance[seed] = 0
		result.order = append(result.order, seed)
	}
	if options.Traversal == TraversalDFS {
		idx.traverseDFS(&result, seedIDs, seedSet, adjacency, degree, options.MaxDepth, exploreLimit, result.branchFanout)
	} else {
		idx.traverseBFS(&result, seedIDs, seedSet, adjacency, degree, options.MaxDepth, exploreLimit, result.branchFanout)
	}
	return result
}

func traversalFanout(maxNodes, seeds, depth, configured int) int {
	if configured > 0 {
		return configured
	}
	layers := max(1, depth+1)
	perBranch := maxNodes / max(1, seeds*layers)
	return max(4, min(16, perBranch))
}

func (idx *Index) traverseBFS(result *traversalResult, seeds []string, seedSet map[string]bool, adjacency map[string][]adjacentEdge, degree map[string]int, depth, exploreLimit, fanout int) {
	queue := append([]string(nil), seeds...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		currentDepth := result.distance[current]
		if currentDepth >= depth {
			continue
		}
		if !seedSet[current] && result.hubThreshold >= 0 && degree[current] >= result.hubThreshold {
			result.hubsSuppressed++
			continue
		}
		expanded := 0
		for _, adjacent := range adjacency[current] {
			if _, seen := result.distance[adjacent.nodeID]; seen {
				continue
			}
			if expanded >= fanout {
				result.branchesPruned++
				continue
			}
			if len(result.order) >= exploreLimit {
				result.exploredLimit = true
				return
			}
			result.distance[adjacent.nodeID] = currentDepth + 1
			result.via[adjacent.nodeID] = adjacent.edge
			result.order = append(result.order, adjacent.nodeID)
			queue = append(queue, adjacent.nodeID)
			expanded++
		}
	}
}

func (idx *Index) traverseDFS(result *traversalResult, seeds []string, seedSet map[string]bool, adjacency map[string][]adjacentEdge, degree map[string]int, depth, exploreLimit, fanout int) {
	type item struct {
		id    string
		depth int
	}
	emitted := map[string]bool{}
	suppressed := map[string]bool{}
	for _, seed := range seeds {
		emitted[seed] = true
	}
	for _, seed := range seeds {
		stack := []item{{id: seed, depth: 0}}
		for len(stack) > 0 {
			current := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if bestDepth, ok := result.distance[current.id]; ok && current.depth > bestDepth {
				continue
			}
			if !emitted[current.id] {
				emitted[current.id] = true
				result.order = append(result.order, current.id)
			}
			if current.depth >= depth {
				continue
			}
			if !seedSet[current.id] && result.hubThreshold >= 0 && degree[current.id] >= result.hubThreshold {
				if !suppressed[current.id] {
					suppressed[current.id] = true
					result.hubsSuppressed++
				}
				continue
			}
			edges := adjacency[current.id]
			chosen := make([]adjacentEdge, 0, min(fanout, len(edges)))
			for _, adjacent := range edges {
				nextDepth := current.depth + 1
				previousDepth, seen := result.distance[adjacent.nodeID]
				if seen && previousDepth <= nextDepth {
					continue
				}
				if len(chosen) >= fanout {
					result.branchesPruned++
					continue
				}
				chosen = append(chosen, adjacent)
			}
			for i := len(chosen) - 1; i >= 0; i-- {
				adjacent := chosen[i]
				nextDepth := current.depth + 1
				if len(result.distance) >= exploreLimit {
					result.exploredLimit = true
					return
				}
				result.distance[adjacent.nodeID] = nextDepth
				result.via[adjacent.nodeID] = adjacent.edge
				stack = append(stack, item{id: adjacent.nodeID, depth: nextDepth})
			}
		}
	}
}

func hubThreshold(degree map[string]int, configured int) int {
	if configured == -1 {
		return -1
	}
	if configured > 0 {
		return configured
	}
	values := make([]int, 0, len(degree))
	for _, value := range degree {
		values = append(values, value)
	}
	if len(values) == 0 {
		return 50
	}
	sort.Ints(values)
	index := int(math.Ceil(float64(len(values))*0.99)) - 1
	index = max(0, min(index, len(values)-1))
	return max(50, values[index])
}

func (idx *Index) fitRetrieval(question string, options normalizedRetrieveOptions, seedIDs []string, seedSet map[string]bool, scores, degree map[string]int, walk traversalResult) Retrieval {
	stats := RetrievalStats{
		Traversal: options.Traversal, Direction: options.Direction, DirectionPreference: options.directionPreference, Depth: options.MaxDepth,
		SeedIDs: append([]string(nil), seedIDs...), RelationFilters: append([]graph.EdgeKind(nil), options.Relations...),
		RelationFilterFrom: options.filterFrom, TokenBudget: options.TokenBudget,
		HubThreshold: walk.hubThreshold, BranchFanout: walk.branchFanout, HubsSuppressed: walk.hubsSuppressed, BranchesPruned: walk.branchesPruned, ExploredNodes: len(walk.order),
	}
	result := Retrieval{Version: 1, Query: safeTextBytes(question, 256), Stats: stats}
	if len(walk.order) == 0 {
		return result
	}
	budgetUsed := lineTokens(retrievalHeader(question, stats))
	reserve := lineTokens(truncationLine(RetrievalStats{
		TruncatedReason: []string{"token_budget", "max_nodes", "exploration_limit", "branch_limit"},
		OmittedNodes:    50_000,
		OmittedEdges:    999_999_999,
	}))
	included := map[string]bool{}
	includedEdges := map[string]bool{}
	maxCandidates := min(len(walk.order), options.MaxNodes)
	seedEdgesHandled := false

	for position, nodeID := range walk.order[:maxCandidates] {
		if !seedSet[nodeID] && !seedEdgesHandled {
			seedEdges := idx.edgesWithin(included, options.relationSet)
			seedEdgesHandled = true
			for _, edge := range seedEdges {
				if includedEdges[edge.ID] {
					continue
				}
				cost := lineTokens(edgeLine(edge))
				if budgetUsed+cost+reserve > options.TokenBudget {
					break
				}
				result.Edges = append(result.Edges, edge)
				includedEdges[edge.ID] = true
				budgetUsed += cost
				break
			}
		}
		docIndex, ok := idx.byID[nodeID]
		if !ok {
			continue
		}
		node := contextNode(idx.docs[docIndex].node, scores[nodeID], walk.distance[nodeID], degree[nodeID], seedSet[nodeID])
		var via ContextEdge
		viaCost := 0
		if edge, hasVia := walk.via[nodeID]; hasVia {
			via = contextEdge(edge)
			node.ViaEdgeID = via.ID
			viaCost = lineTokens(edgeLine(via))
		}
		cost := lineTokens(nodeLine(node)) + viaCost
		if budgetUsed+cost+reserve > options.TokenBudget {
			result.Stats.Truncated = true
			result.Stats.TruncatedReason = appendReason(result.Stats.TruncatedReason, "token_budget")
			result.Stats.OmittedNodes += maxCandidates - position
			break
		}
		result.Nodes = append(result.Nodes, node)
		included[nodeID] = true
		budgetUsed += lineTokens(nodeLine(node))
		if via.ID != "" {
			result.Edges = append(result.Edges, via)
			includedEdges[via.ID] = true
			budgetUsed += viaCost
		}
	}
	if !seedEdgesHandled {
		seedEdgesHandled = true
		seedEdges := idx.edgesWithin(included, options.relationSet)
		for _, edge := range seedEdges {
			if includedEdges[edge.ID] {
				continue
			}
			cost := lineTokens(edgeLine(edge))
			if budgetUsed+cost+reserve > options.TokenBudget {
				break
			}
			result.Edges = append(result.Edges, edge)
			includedEdges[edge.ID] = true
			budgetUsed += cost
			break
		}
	}
	if len(walk.order) > options.MaxNodes {
		result.Stats.Truncated = true
		result.Stats.TruncatedReason = appendReason(result.Stats.TruncatedReason, "max_nodes")
		result.Stats.OmittedNodes += len(walk.order) - options.MaxNodes
	}
	if walk.exploredLimit {
		result.Stats.Truncated = true
		result.Stats.TruncatedReason = appendReason(result.Stats.TruncatedReason, "exploration_limit")
	}
	if walk.branchesPruned > 0 {
		result.Stats.Truncated = true
		result.Stats.TruncatedReason = appendReason(result.Stats.TruncatedReason, "branch_limit")
	}

	// Add non-discovery relationships only after every included node retains the
	// edge that explains why it was retrieved.
	var extraEdges []ContextEdge
	for _, edge := range idx.graph.Edges {
		if !included[edge.From] || !included[edge.To] || includedEdges[stableEdgeID(edge)] {
			continue
		}
		if len(options.relationSet) > 0 && !options.relationSet[edge.Kind] {
			continue
		}
		extraEdges = append(extraEdges, contextEdge(edge))
	}
	sort.Slice(extraEdges, func(i, j int) bool {
		if relationPriority(extraEdges[i].Kind) != relationPriority(extraEdges[j].Kind) {
			return relationPriority(extraEdges[i].Kind) < relationPriority(extraEdges[j].Kind)
		}
		return extraEdges[i].ID < extraEdges[j].ID
	})
	for position, edge := range extraEdges {
		cost := lineTokens(edgeLine(edge))
		if budgetUsed+cost+reserve > options.TokenBudget {
			result.Stats.Truncated = true
			result.Stats.TruncatedReason = appendReason(result.Stats.TruncatedReason, "token_budget")
			result.Stats.OmittedEdges += len(extraEdges) - position
			break
		}
		result.Edges = append(result.Edges, edge)
		includedEdges[edge.ID] = true
		budgetUsed += cost
	}
	result.Stats.EstimatedTokens = budgetUsed
	if result.Stats.Truncated {
		result.Stats.EstimatedTokens += lineTokens(truncationLine(result.Stats))
	}
	return result
}

func (idx *Index) edgesWithin(nodes map[string]bool, relationSet map[graph.EdgeKind]bool) []ContextEdge {
	var result []ContextEdge
	for _, edge := range idx.graph.Edges {
		if !nodes[edge.From] || !nodes[edge.To] {
			continue
		}
		if len(relationSet) > 0 && !relationSet[edge.Kind] {
			continue
		}
		result = append(result, contextEdge(edge))
	}
	sort.Slice(result, func(i, j int) bool {
		if relationPriority(result[i].Kind) != relationPriority(result[j].Kind) {
			return relationPriority(result[i].Kind) < relationPriority(result[j].Kind)
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func contextNode(node graph.Node, score, depth, degree int, seed bool) ContextNode {
	result := ContextNode{
		ID: node.ID, Kind: node.Kind, Name: safeText(node.Name), Path: safeText(node.Path),
		Package: safeText(node.Package), StartLine: node.StartLine, EndLine: node.EndLine,
		Score: score, Depth: depth, Degree: degree, Seed: seed, Confidence: safeText(node.Meta["confidence"]),
	}
	result.Resolved = parsedBool(node.Meta["resolved"])
	return result
}

func contextEdge(edge graph.Edge) ContextEdge {
	id := stableEdgeID(edge)
	result := ContextEdge{
		ID: id, Kind: edge.Kind, From: edge.From, To: edge.To,
		Confidence: safeText(edge.Meta["confidence"]), Evidence: safeText(edge.Meta["evidence"]),
		Rationale: safeText(edge.Meta["rationale"]), Path: safeText(edge.Meta["path"]),
	}
	if line, err := strconv.Atoi(edge.Meta["line"]); err == nil && line > 0 {
		result.Line = line
	}
	result.Resolved = parsedBool(edge.Meta["resolved"])
	return result
}

func stableEdgeID(edge graph.Edge) string {
	if edge.ID != "" {
		return edge.ID
	}
	return graph.EdgeID(edge.Kind, edge.From, edge.To)
}

func parsedBool(value string) *bool {
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return nil
	}
	return &parsed
}

// WriteRetrieval writes compact, model-safe text or the structured result.
func WriteRetrieval(w io.Writer, result Retrieval, jsonOut bool) error {
	if jsonOut {
		return writeJSON(w, result)
	}
	if len(result.Nodes) == 0 {
		if result.Stats.Truncated && len(result.Stats.SeedIDs) > 0 {
			if _, err := fmt.Fprintln(w, retrievalHeader(result.Query, result.Stats)); err != nil {
				return err
			}
			_, err := fmt.Fprintln(w, truncationLine(result.Stats))
			return err
		}
		_, err := fmt.Fprintln(w, "No matching nodes found.")
		return err
	}
	if _, err := fmt.Fprintln(w, retrievalHeader(result.Query, result.Stats)); err != nil {
		return err
	}
	edges := map[string]ContextEdge{}
	for _, edge := range result.Edges {
		edges[edge.ID] = edge
	}
	writtenEdges := map[string]bool{}
	seedIDs := map[string]bool{}
	for _, node := range result.Nodes {
		if node.Seed {
			seedIDs[node.ID] = true
		}
	}
	for _, node := range result.Nodes {
		if !node.Seed {
			continue
		}
		if _, err := fmt.Fprintln(w, nodeLine(node)); err != nil {
			return err
		}
	}
	for _, edge := range result.Edges {
		if !seedIDs[edge.From] || !seedIDs[edge.To] {
			continue
		}
		if _, err := fmt.Fprintln(w, edgeLine(edge)); err != nil {
			return err
		}
		writtenEdges[edge.ID] = true
	}
	for _, node := range result.Nodes {
		if node.Seed {
			continue
		}
		if _, err := fmt.Fprintln(w, nodeLine(node)); err != nil {
			return err
		}
		if edge, ok := edges[node.ViaEdgeID]; ok {
			if _, err := fmt.Fprintln(w, edgeLine(edge)); err != nil {
				return err
			}
			writtenEdges[edge.ID] = true
		}
	}
	for _, edge := range result.Edges {
		if writtenEdges[edge.ID] {
			continue
		}
		if _, err := fmt.Fprintln(w, edgeLine(edge)); err != nil {
			return err
		}
	}
	if result.Stats.Truncated {
		_, err := fmt.Fprintln(w, truncationLine(result.Stats))
		return err
	}
	return nil
}

// WriteAffected writes the same bounded retrieval envelope with an explicit
// reverse-impact label in compact text mode.
func WriteAffected(w io.Writer, result Retrieval, jsonOut bool) error {
	if jsonOut {
		return WriteRetrieval(w, result, true)
	}
	var output strings.Builder
	if err := WriteRetrieval(&output, result, false); err != nil {
		return err
	}
	_, err := io.WriteString(w, strings.Replace(output.String(), "RAVEL_CONTEXT", "RAVEL_AFFECTED", 1))
	return err
}

func retrievalHeader(question string, stats RetrievalStats) string {
	parts := []string{
		"RAVEL_CONTEXT",
		"traversal=" + strings.ToUpper(string(stats.Traversal)),
		"direction=" + string(stats.Direction),
		"depth=" + strconv.Itoa(stats.Depth),
		"budget=" + strconv.Itoa(stats.TokenBudget),
	}
	if stats.DirectionPreference != "" {
		parts = append(parts, "preference="+string(stats.DirectionPreference))
	}
	headerByteBudget := max(96, stats.TokenBudget) // one-third of the token budget at 3 bytes/token
	base := strings.Join(parts, "\t")
	queryLimit := min(96, max(0, headerByteBudget-len(base)-len("\tquery=\"\"")))
	queryPart := "query=" + quoteField(safeTextBytes(question, queryLimit))
	parts = append(parts[:1], append([]string{queryPart}, parts[1:]...)...)
	if len(stats.RelationFilters) > 0 {
		limit := min(8, len(stats.RelationFilters))
		filters := make([]string, limit)
		for i, relation := range stats.RelationFilters[:limit] {
			filters[i] = safeTextBytes(string(relation), 40)
		}
		if len(stats.RelationFilters) > limit {
			filters = append(filters, fmt.Sprintf("+%d", len(stats.RelationFilters)-limit))
		}
		optional := []string{"relations=" + strings.Join(filters, ","), "filter=" + stats.RelationFilterFrom}
		for _, field := range optional {
			candidate := append(append([]string(nil), parts...), field)
			if len(strings.Join(candidate, "\t")) > headerByteBudget {
				break
			}
			parts = candidate
		}
	}
	return strings.Join(parts, "\t")
}

func nodeLine(node ContextNode) string {
	prefix := "NODE"
	if node.Seed {
		prefix = "SEED"
	}
	parts := []string{prefix, safeText(string(node.Kind)), compactID(node.ID), quoteField(node.Name), "depth=" + strconv.Itoa(node.Depth), "degree=" + strconv.Itoa(node.Degree)}
	if node.Path != "" {
		location := node.Path
		if node.StartLine > 0 {
			location += ":" + strconv.Itoa(node.StartLine)
		}
		parts = append(parts, "src="+quoteField(location))
	}
	if node.Score > 0 {
		parts = append(parts, "score="+strconv.Itoa(node.Score))
	}
	if node.Confidence != "" {
		parts = append(parts, "confidence="+node.Confidence)
	}
	if node.Resolved != nil {
		parts = append(parts, "resolved="+strconv.FormatBool(*node.Resolved))
	}
	return strings.Join(parts, "\t")
}

func edgeLine(edge ContextEdge) string {
	parts := []string{"EDGE", compactID(edge.From), "--" + safeText(string(edge.Kind)) + "-->", compactID(edge.To)}
	if edge.Confidence != "" {
		parts = append(parts, "confidence="+edge.Confidence)
	}
	if edge.Resolved != nil {
		parts = append(parts, "resolved="+strconv.FormatBool(*edge.Resolved))
	}
	if edge.Evidence != "" {
		parts = append(parts, "evidence="+quoteField(edge.Evidence))
	} else if edge.Path != "" {
		location := edge.Path
		if edge.Line > 0 {
			location += ":" + strconv.Itoa(edge.Line)
		}
		parts = append(parts, "evidence="+quoteField(location))
	}
	if edge.Rationale != "" {
		parts = append(parts, "rationale="+quoteField(edge.Rationale))
	}
	return strings.Join(parts, "\t")
}

func truncationLine(stats RetrievalStats) string {
	return fmt.Sprintf("TRUNCATED\treason=%s\tomitted_nodes=%d\tomitted_edges=%d\tpruned_branches=%d\thint=%s",
		strings.Join(stats.TruncatedReason, ","), stats.OmittedNodes, stats.OmittedEdges, stats.BranchesPruned,
		quoteField(truncationHint(stats.TruncatedReason)))
}

func truncationHint(reasons []string) string {
	for _, reason := range reasons {
		if reason == "branch_limit" {
			return "branch_limit: raise --branch-fanout or narrow relations/depth"
		}
	}
	for _, reason := range reasons {
		if reason == "exploration_limit" {
			return "exploration_limit: narrow relations/depth"
		}
	}
	for _, reason := range reasons {
		if reason == "max_nodes" {
			return "max_nodes: raise --max-nodes or narrow relations/depth"
		}
	}
	return "token_budget: raise --token-budget or narrow output"
}

func quoteField(value string) string {
	return strconv.Quote(safeText(value))
}

func compactID(value string) string {
	if utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl) {
		return value
	}
	// Preserve the exact identifier while preventing control characters from
	// manufacturing extra compact-protocol records.
	return strconv.Quote(value)
}

func safeText(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || unicode.IsControl(r) || !utf8.ValidRune(r) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 400 {
		value = string(runes[:399]) + "…"
	}
	return value
}

func safeTextBytes(value string, limit int) string {
	value = safeText(value)
	if len(value) <= limit {
		return value
	}
	end := limit - len("…")
	if end < 0 {
		return ""
	}
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end] + "…"
}

func estimateTokens(value string) int {
	if value == "" {
		return 0
	}
	// Three UTF-8 bytes per token deliberately overestimates most source-code
	// text and matches the conservative public comparison target.
	return (len([]byte(value)) + 2) / 3
}

func lineTokens(value string) int {
	return estimateTokens(value + "\n")
}

func appendReason(reasons []string, reason string) []string {
	for _, existing := range reasons {
		if existing == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}
