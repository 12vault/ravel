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
	defaultSeedLimit         = 3
	defaultMaxDepth          = 2
	defaultMaxNodes          = 100
	defaultTokenBudget       = 2_000
	minimumTokenBudget       = 128
	maximumTokenBudget       = 100_000
	maximumBranchFanout      = 10_000
	maximumTraceNodes        = 20
	maximumLexicalCandidates = 128
	maximumExplanationEdges  = 8
	candidateBudgetPercent   = 92
	shortlistLexicalPrefix   = 16
	shortlistGraphReserve    = 8
	shortlistSameFileRescues = 1
	shortlistSameFileSlot    = 1
	shortlistBudgetBase      = 700
	shortlistBudgetPerAnchor = 30
	shortlistBudgetMinimum   = 800
	shortlistBudgetMaximum   = 1_400
	shortlistBudgetUncertain = 1_200
	shortlistBudgetProse     = 2_000
	shortlistIdentityCopies  = 2
	sameFileMinimumAnchors   = 8
	shortlistAffinityRescues = 4
	sameFileCandidateWindow  = maximumLexicalCandidates
	sameFileAnchorLimit      = 24
	affinityRerankWindow     = 800
	affinityMaxDepth         = 3
	affinityNeighborLimit    = 128
	affinityFrontierLimit    = 2_048
	traceReachabilityLimit   = 20_000
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
	CommunityBoost           bool             `json:"communityBoost,omitempty"`
	CandidateShortlist       bool             `json:"candidateShortlist,omitempty"`
	TraceNodeIDs             []string         `json:"traceNodeIds,omitempty"`
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
	Traversal               Traversal        `json:"traversal"`
	Direction               Direction        `json:"direction"`
	DirectionPreference     Direction        `json:"directionPreference,omitempty"`
	Depth                   int              `json:"depth"`
	SeedIDs                 []string         `json:"seedIds"`
	RelationFilters         []graph.EdgeKind `json:"relationFilters,omitempty"`
	RelationFilterFrom      string           `json:"relationFilterFrom,omitempty"`
	TokenBudget             int              `json:"tokenBudget"`
	OutputTokenBudget       int              `json:"outputTokenBudget,omitempty"`
	EstimatedTokens         int              `json:"estimatedTokens"`
	HubThreshold            int              `json:"hubThreshold,omitempty"`
	BranchFanout            int              `json:"branchFanout,omitempty"`
	HubsSuppressed          int              `json:"hubsSuppressed,omitempty"`
	BranchesPruned          int              `json:"branchesPruned,omitempty"`
	ExploredNodes           int              `json:"exploredNodes"`
	LexicalCandidates       int              `json:"lexicalCandidates,omitempty"`
	DeduplicatedNodes       int              `json:"deduplicatedNodes,omitempty"`
	ExplanationEdgesOmitted int              `json:"explanationEdgesOmitted,omitempty"`
	UnselectedNodes         int              `json:"unselectedNodes,omitempty"`
	HeaderTokens            int              `json:"headerTokens,omitempty"`
	CandidateTokens         int              `json:"candidateTokens,omitempty"`
	ExplanationTokens       int              `json:"explanationTokens,omitempty"`
	OmittedNodes            int              `json:"omittedNodes,omitempty"`
	OmittedEdges            int              `json:"omittedEdges,omitempty"`
	Truncated               bool             `json:"truncated"`
	TruncatedReason         []string         `json:"truncatedReason,omitempty"`
	CommunityBoost          bool             `json:"communityBoost,omitempty"`
	SameFileRescues         []SameFileRescue `json:"sameFileRescues,omitempty"`
	AffinityRescues         []AffinityRescue `json:"affinityRescues,omitempty"`
	StructuredCandidates    int              `json:"structuredCandidates,omitempty"`
	StructuredQueryAnchors  int              `json:"structuredQueryAnchors,omitempty"`
	TraceNodes              []RetrievalTrace `json:"traceNodes,omitempty"`
}

// SameFileRescue describes one lexical candidate promoted because a stronger
// candidate or bounded traversal result points at the same source file.
type SameFileRescue struct {
	ID             string `json:"id"`
	AnchorPath     string `json:"anchorPath"`
	AnchorCount    int    `json:"anchorCount"`
	OriginalRank   int    `json:"originalRank"`
	StructuralSlot int    `json:"structuralSlot"`
}

// AffinityRescue describes one below-cutoff lexical candidate promoted by
// bounded graph affinity. Margin is relative to the strongest rejected rescue.
type AffinityRescue struct {
	ID             string  `json:"id"`
	OriginalRank   int     `json:"originalRank"`
	RerankedRank   int     `json:"rerankedRank"`
	Affinity       float64 `json:"affinity"`
	AffinityMargin float64 `json:"affinityMargin"`
}

// RetrievalTrace records how one requested node moved through retrieval. It is
// diagnostic only: traced nodes never affect ranking, traversal, or packing.
type RetrievalTrace struct {
	ID                  string  `json:"id"`
	Indexed             bool    `json:"indexed"`
	LexicalRank         int     `json:"lexicalRank,omitempty"`
	PromotionRank       int     `json:"promotionRank,omitempty"`
	PromotionExclusion  string  `json:"promotionExclusion,omitempty"`
	OriginalLexicalRank int     `json:"originalLexicalRank,omitempty"`
	SameFileRescued     bool    `json:"sameFileRescued,omitempty"`
	SameFileAnchorPath  string  `json:"sameFileAnchorPath,omitempty"`
	AffinityRescued     bool    `json:"affinityRescued,omitempty"`
	AffinityScore       float64 `json:"affinityScore,omitempty"`
	AffinityMargin      float64 `json:"affinityMargin,omitempty"`
	TraversalExclusion  string  `json:"traversalExclusion,omitempty"`
	Seeded              bool    `json:"seeded,omitempty"`
	Traversed           bool    `json:"traversed,omitempty"`
	Depth               int     `json:"depth,omitempty"`
	WalkRank            int     `json:"walkRank,omitempty"`
	CandidateRank       int     `json:"candidateRank,omitempty"`
	ReturnedRank        int     `json:"returnedRank,omitempty"`
	Deduplicated        bool    `json:"deduplicated,omitempty"`
	DroppedReason       string  `json:"droppedReason,omitempty"`
}

type normalizedRetrieveOptions struct {
	RetrieveOptions
	relationSet         map[graph.EdgeKind]bool
	filterFrom          string
	filterEdges         bool
	directionPreference Direction
}

type traversalResult struct {
	order                  []string
	distance               map[string]int
	via                    map[string]graph.Edge
	hubThreshold           int
	branchFanout           int
	hubsSuppressed         int
	branchesPruned         int
	exploredLimit          bool
	lexicalCandidates      int
	lexicalOnly            map[string]bool
	sameFileRescued        map[string]bool
	sameFileRescues        []SameFileRescue
	affinityRescues        []AffinityRescue
	structuredCandidates   int
	structuredQueryAnchors int
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
	ranked := idx.rankWithAnchors(strings.Join(terms, " "), question, !normalized.CandidateShortlist)
	if len(ranked) == 0 && len(forcedSeedIDs) == 0 {
		traces := idx.newRetrievalTraces(normalized.TraceNodeIDs, nil, nil, traversalResult{}, normalized.CandidateShortlist)
		finalizeRetrievalTraces(traces, 0)
		return Retrieval{
			Version: 1,
			Query:   safeTextBytes(question, 256),
			Stats: RetrievalStats{
				Traversal: normalized.Traversal, Direction: normalized.Direction,
				Depth: normalized.MaxDepth, TokenBudget: normalized.TokenBudget,
				RelationFilters: normalized.Relations, RelationFilterFrom: normalized.filterFrom,
				TraceNodes: traces,
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

	adjacency := idx.newQueryAdjacency(normalized, scores)
	walk := idx.traverse(seedIDs, seedSet, adjacency, normalized)
	if normalized.CandidateShortlist {
		walk.structuredCandidates = idx.countStructuredCandidates(ranked)
		walk.structuredQueryAnchors = structuredQueryAnchorCount(question, false)
		walk.sameFileRescues = idx.selectSameFileCandidates(
			ranked, seedIDs, walk.order, shortlistLexicalPrefix, shortlistSameFileRescues,
		)
		walk.sameFileRescued = make(map[string]bool, len(walk.sameFileRescues))
		for _, rescue := range walk.sameFileRescues {
			walk.sameFileRescued[rescue.ID] = true
		}
		ranked, walk.affinityRescues = idx.rerankAffinityCandidates(ranked, seedIDs, adjacency, shortlistLexicalPrefix, shortlistAffinityRescues)
	}
	traces := idx.newRetrievalTraces(normalized.TraceNodeIDs, ranked, seedSet, walk, normalized.CandidateShortlist)
	idx.diagnoseTraversalExclusions(traces, seedIDs, seedSet, scores, adjacency, normalized, walk)
	walk = idx.promoteLexicalCandidates(walk, ranked, normalized.CandidateShortlist)
	for seedID, edge := range seedEvidence {
		if seedSet[seedID] {
			walk.via[seedID] = cloneEdge(edge)
		}
	}
	return idx.fitRetrieval(question, normalized, seedIDs, seedSet, scores, adjacency.degree, walk, traces), nil
}

func (idx *Index) countStructuredCandidates(ranked []rankedNode) int {
	names := map[string]bool{}
	for _, candidate := range ranked {
		if !candidate.anchored {
			continue
		}
		node := idx.docs[candidate.index].node
		if !eligibleShortlistCandidate(node) {
			continue
		}
		name := idx.docs[candidate.index].nameText
		if name != "" {
			names[name] = true
		}
	}
	return len(names)
}

func adaptiveShortlistTokenBudget(hardLimit, structuredCandidates, structuredQueryAnchors int) int {
	if hardLimit <= shortlistBudgetMinimum {
		return hardLimit
	}
	target := shortlistBudgetUncertain
	if structuredQueryAnchors == 0 {
		target = shortlistBudgetProse
	} else if structuredCandidates > 1 {
		target = max(shortlistBudgetMinimum, min(
			shortlistBudgetMaximum,
			shortlistBudgetBase+structuredCandidates*shortlistBudgetPerAnchor,
		))
	}
	return min(hardLimit, target)
}

// selectSameFileCandidates spends a bounded part of the existing structural
// shortlist reserve on lexically relevant declarations from files already
// represented by strong lexical or traversal anchors. One rescue per file
// prevents large files from taking over the shortlist.
func (idx *Index) selectSameFileCandidates(ranked []rankedNode, seedIDs, walkOrder []string, prefix, rescueLimit int) []SameFileRescue {
	if rescueLimit <= 0 || len(ranked) <= prefix || prefix < 0 {
		return nil
	}
	type fileAnchor struct {
		bestRank int
		count    int
	}
	anchors := map[string]fileAnchor{}
	anchorIDs := map[string]bool{}
	walkedIDs := make(map[string]bool, len(walkOrder))
	for _, id := range walkOrder {
		walkedIDs[id] = true
	}
	addAnchor := func(id string, rank int) {
		if anchorIDs[id] {
			return
		}
		docIndex, ok := idx.byID[id]
		if !ok {
			return
		}
		path := idx.docs[docIndex].node.Path
		if path == "" {
			return
		}
		anchorIDs[id] = true
		anchor := anchors[path]
		if anchor.count == 0 || rank < anchor.bestRank {
			anchor.bestRank = rank
		}
		anchor.count++
		anchors[path] = anchor
	}
	for _, id := range seedIDs {
		addAnchor(id, 0)
	}
	for position, candidate := range ranked[:min(prefix, len(ranked))] {
		addAnchor(idx.docs[candidate.index].node.ID, position+1)
	}
	for position, id := range walkOrder[:min(len(walkOrder), sameFileAnchorLimit)] {
		addAnchor(id, prefix+position+1)
	}
	if len(anchors) == 0 {
		return nil
	}
	type rescueCandidate struct {
		position   int
		path       string
		anchorRank int
		anchorHits int
		score      float64
	}
	windowEnd := min(len(ranked), sameFileCandidateWindow)
	candidates := make([]rescueCandidate, 0, windowEnd-prefix)
	for position := prefix; position < windowEnd; position++ {
		candidate := ranked[position]
		node := idx.docs[candidate.index].node
		anchor, ok := anchors[node.Path]
		if !ok || anchor.count < sameFileMinimumAnchors || anchorIDs[node.ID] || walkedIDs[node.ID] || !graph.SymbolKind(node.Kind) || !eligibleShortlistCandidate(node) {
			continue
		}
		candidates = append(candidates, rescueCandidate{
			position: position, path: node.Path, anchorRank: anchor.bestRank,
			anchorHits: anchor.count, score: candidate.score,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].anchorHits != candidates[j].anchorHits {
			return candidates[i].anchorHits > candidates[j].anchorHits
		}
		if candidates[i].anchorRank != candidates[j].anchorRank {
			return candidates[i].anchorRank < candidates[j].anchorRank
		}
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].position < candidates[j].position
	})
	selectedPaths := map[string]bool{}
	selectedCandidates := make([]rescueCandidate, 0, rescueLimit)
	for _, candidate := range candidates {
		if len(selectedCandidates) >= rescueLimit {
			break
		}
		if selectedPaths[candidate.path] {
			continue
		}
		selectedPaths[candidate.path] = true
		selectedCandidates = append(selectedCandidates, candidate)
	}
	if len(selectedCandidates) == 0 {
		return nil
	}
	rescues := make([]SameFileRescue, 0, len(selectedCandidates))
	for _, candidate := range selectedCandidates {
		rescues = append(rescues, SameFileRescue{
			ID: idx.docs[ranked[candidate.position].index].node.ID, AnchorPath: candidate.path, AnchorCount: candidate.anchorHits,
			OriginalRank: candidate.position + 1, StructuralSlot: shortlistSameFileSlot,
		})
	}
	return rescues
}

// rerankAffinityCandidates gives a small number of below-cutoff candidates a
// shortlist position when bounded graph propagation connects them to lexical
// seeds. The leading lexical prefix remains stable.
func (idx *Index) rerankAffinityCandidates(ranked []rankedNode, seedIDs []string, adjacency *queryAdjacency, prefix, rescueLimit int) ([]rankedNode, []AffinityRescue) {
	if rescueLimit <= 0 || len(ranked) <= maximumLexicalCandidates || prefix >= maximumLexicalCandidates {
		return ranked, nil
	}
	rankByID := make(map[string]int, len(ranked))
	for position, candidate := range ranked {
		rankByID[idx.docs[candidate.index].node.ID] = position
	}
	frontier := map[string]float64{}
	affinity := map[string]float64{}
	for _, seedID := range seedIDs {
		weight := 1.0
		if position, ok := rankByID[seedID]; ok {
			weight = 1.0 / float64(position+1)
		}
		frontier[seedID] = max(frontier[seedID], weight)
		affinity[seedID] = max(affinity[seedID], weight)
	}
	for depth := 0; depth < affinityMaxDepth && len(frontier) > 0; depth++ {
		next := map[string]float64{}
		for nodeID, weight := range frontier {
			neighbors := adjacency.neighborsOf(nodeID)
			degreePenalty := math.Sqrt(float64(max(1, len(neighbors))))
			for _, edge := range neighbors[:min(len(neighbors), affinityNeighborLimit)] {
				nextID := adjacency.nodeID(edge)
				propagated := weight / degreePenalty
				if propagated > next[nextID] {
					next[nextID] = propagated
				}
				if propagated > affinity[nextID] {
					affinity[nextID] = propagated
				}
			}
		}
		frontier = strongestAffinity(next, affinityFrontierLimit)
	}
	type rescueCandidate struct {
		position int
		affinity float64
		score    float64
	}
	windowEnd := min(len(ranked), affinityRerankWindow)
	candidates := make([]rescueCandidate, 0, windowEnd-maximumLexicalCandidates)
	for position := maximumLexicalCandidates; position < windowEnd; position++ {
		node := idx.docs[ranked[position].index].node
		if !eligibleShortlistCandidate(node) || affinity[node.ID] == 0 {
			continue
		}
		candidates = append(candidates, rescueCandidate{
			position: position, affinity: affinity[node.ID], score: ranked[position].score,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].affinity != candidates[j].affinity {
			return candidates[i].affinity > candidates[j].affinity
		}
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].position < candidates[j].position
	})
	selected := map[int]bool{}
	selectedCandidates := make([]rescueCandidate, 0, rescueLimit)
	for _, candidate := range candidates {
		if len(selected) >= rescueLimit {
			break
		}
		selected[candidate.position] = true
		selectedCandidates = append(selectedCandidates, candidate)
	}
	if len(selected) == 0 {
		return ranked, nil
	}
	boundaryAffinity := 0.0
	if len(candidates) > len(selectedCandidates) {
		boundaryAffinity = candidates[len(selectedCandidates)].affinity
	}
	rescues := make([]AffinityRescue, 0, len(selectedCandidates))
	for rescueIndex, candidate := range selectedCandidates {
		margin := 1.0
		if candidate.affinity > 0 && boundaryAffinity > 0 {
			margin = max(0, (candidate.affinity-boundaryAffinity)/candidate.affinity)
		}
		rescues = append(rescues, AffinityRescue{
			ID:           idx.docs[ranked[candidate.position].index].node.ID,
			OriginalRank: candidate.position + 1, RerankedRank: prefix + rescueIndex + 1,
			Affinity: candidate.affinity, AffinityMargin: margin,
		})
	}
	result := make([]rankedNode, 0, len(ranked))
	result = append(result, ranked[:prefix]...)
	for _, candidate := range candidates {
		if selected[candidate.position] {
			result = append(result, ranked[candidate.position])
		}
	}
	for position, candidate := range ranked {
		if position < prefix || selected[position] {
			continue
		}
		result = append(result, candidate)
	}
	return result, rescues
}

func strongestAffinity(values map[string]float64, limit int) map[string]float64 {
	if len(values) <= limit {
		return values
	}
	type weightedNode struct {
		id     string
		weight float64
	}
	ordered := make([]weightedNode, 0, len(values))
	for id, weight := range values {
		ordered = append(ordered, weightedNode{id: id, weight: weight})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].weight != ordered[j].weight {
			return ordered[i].weight > ordered[j].weight
		}
		return ordered[i].id < ordered[j].id
	})
	result := make(map[string]float64, limit)
	for _, node := range ordered[:limit] {
		result[node.id] = node.weight
	}
	return result
}

func (idx *Index) newRetrievalTraces(ids []string, ranked []rankedNode, seedSet map[string]bool, walk traversalResult, shortlist bool) []RetrievalTrace {
	if len(ids) == 0 {
		return nil
	}
	traces := make([]RetrievalTrace, 0, len(ids))
	positions := make(map[string]int, len(ids))
	rescues := make(map[string]AffinityRescue, len(walk.affinityRescues))
	for _, rescue := range walk.affinityRescues {
		rescues[rescue.ID] = rescue
	}
	sameFileRescues := make(map[string]SameFileRescue, len(walk.sameFileRescues))
	for _, rescue := range walk.sameFileRescues {
		sameFileRescues[rescue.ID] = rescue
	}
	for _, id := range ids {
		if _, duplicate := positions[id]; duplicate {
			continue
		}
		positions[id] = len(traces)
		_, indexed := idx.byID[id]
		trace := RetrievalTrace{ID: id, Indexed: indexed, Seeded: seedSet[id]}
		if rescue, ok := sameFileRescues[id]; ok {
			trace.OriginalLexicalRank = rescue.OriginalRank
			trace.SameFileRescued = true
			trace.SameFileAnchorPath = rescue.AnchorPath
		}
		if rescue, ok := rescues[id]; ok {
			trace.OriginalLexicalRank = rescue.OriginalRank
			trace.AffinityRescued = true
			trace.AffinityScore = rescue.Affinity
			trace.AffinityMargin = rescue.AffinityMargin
		}
		if depth, traversed := walk.distance[id]; traversed {
			trace.Traversed = true
			trace.Depth = depth
		}
		traces = append(traces, trace)
	}
	promotionRank := 0
	for rank, candidate := range ranked {
		node := idx.docs[candidate.index].node
		id := node.ID
		position, traced := positions[id]
		eligible := candidate.anchored
		if shortlist {
			eligible = eligibleShortlistCandidate(node)
		}
		if !eligible {
			if traced {
				traces[position].LexicalRank = rank + 1
				traces[position].PromotionExclusion = "shortlist_ineligible"
				if !shortlist {
					traces[position].PromotionExclusion = "not_anchored"
				}
			}
			continue
		}
		promotionRank++
		if !traced {
			continue
		}
		traces[position].LexicalRank = rank + 1
		traces[position].PromotionRank = promotionRank
		if promotionRank > maximumLexicalCandidates {
			traces[position].PromotionExclusion = "lexical_cutoff"
		}
	}
	for position := range traces {
		if traces[position].Indexed && traces[position].LexicalRank == 0 {
			traces[position].PromotionExclusion = "not_ranked"
		}
	}
	return traces
}

// diagnoseTraversalExclusions explains why traced nodes were not reached by
// graph expansion. It runs only for explicit diagnostics and never changes the
// traversal result used for ranking or packing.
func (idx *Index) diagnoseTraversalExclusions(traces []RetrievalTrace, seedIDs []string, seedSet map[string]bool, scores map[string]int, adjacency *queryAdjacency, options normalizedRetrieveOptions, walk traversalResult) {
	if len(traces) == 0 {
		return
	}
	var unfiltered *queryAdjacency
	for position := range traces {
		trace := &traces[position]
		if !trace.Indexed || trace.Traversed {
			continue
		}
		reached, limited := traceReachable(seedIDs, seedSet, trace.ID, adjacency, options.MaxDepth, walk.hubThreshold, true)
		if reached {
			if walk.exploredLimit {
				trace.TraversalExclusion = "exploration_limit"
			} else {
				trace.TraversalExclusion = "branch_limit"
			}
			continue
		}
		if limited {
			trace.TraversalExclusion = "diagnostic_limit"
			continue
		}
		reached, limited = traceReachable(seedIDs, seedSet, trace.ID, adjacency, options.MaxDepth, -1, false)
		if reached {
			trace.TraversalExclusion = "hub_suppressed"
			continue
		}
		if limited {
			trace.TraversalExclusion = "diagnostic_limit"
			continue
		}
		reached, limited = traceReachable(seedIDs, seedSet, trace.ID, adjacency, 8, -1, false)
		if reached {
			trace.TraversalExclusion = "depth_limit"
			continue
		}
		if limited {
			trace.TraversalExclusion = "diagnostic_limit"
			continue
		}
		if options.filterEdges {
			if unfiltered == nil {
				allOptions := options
				allOptions.filterEdges = false
				allOptions.relationSet = nil
				unfiltered = idx.newQueryAdjacency(allOptions, scores)
			}
			reached, limited = traceReachable(seedIDs, seedSet, trace.ID, unfiltered, 8, -1, false)
			if reached {
				trace.TraversalExclusion = "relation_filter"
				continue
			}
			if limited {
				trace.TraversalExclusion = "diagnostic_limit"
				continue
			}
		}
		trace.TraversalExclusion = "disconnected"
	}
}

func traceReachable(seeds []string, seedSet map[string]bool, target string, adjacency *queryAdjacency, maxDepth, hubThreshold int, respectHubs bool) (bool, bool) {
	type item struct {
		id    string
		depth int
	}
	queue := make([]item, 0, len(seeds))
	seen := make(map[string]bool, min(traceReachabilityLimit, len(seeds)*16))
	for _, seed := range seeds {
		if seed == target {
			return true, false
		}
		if !seen[seed] {
			seen[seed] = true
			queue = append(queue, item{id: seed})
		}
	}
	for head := 0; head < len(queue); head++ {
		current := queue[head]
		if current.depth >= maxDepth {
			continue
		}
		if respectHubs && !seedSet[current.id] && hubThreshold >= 0 && adjacency.degreeOf(current.id) >= hubThreshold {
			continue
		}
		for _, edge := range adjacency.neighborsOf(current.id) {
			next := adjacency.nodeID(edge)
			if seen[next] {
				continue
			}
			if next == target {
				return true, false
			}
			if len(seen) >= traceReachabilityLimit {
				return false, true
			}
			seen[next] = true
			queue = append(queue, item{id: next, depth: current.depth + 1})
		}
	}
	return false, false
}

// promoteLexicalCandidates exposes ranked matches without turning every match
// into a traversal origin. Candidate-shortlist mode is explicitly ranking
// oriented, so it receives the leading lexical candidates; balanced context
// keeps its legacy behavior and only promotes exact structured anchors.
func (idx *Index) promoteLexicalCandidates(walk traversalResult, ranked []rankedNode, shortlist bool) traversalResult {
	seen := make(map[string]bool, len(walk.order))
	order := make([]string, 0, len(walk.order)+min(maximumLexicalCandidates, len(ranked)))
	for _, candidate := range ranked {
		if (!shortlist && !candidate.anchored) || len(order) >= maximumLexicalCandidates {
			continue
		}
		node := idx.docs[candidate.index].node
		if shortlist && !eligibleShortlistCandidate(node) {
			continue
		}
		id := node.ID
		if seen[id] {
			continue
		}
		seen[id] = true
		order = append(order, id)
		if _, traversed := walk.distance[id]; !traversed {
			walk.distance[id] = 0
			if shortlist {
				if walk.lexicalOnly == nil {
					walk.lexicalOnly = map[string]bool{}
				}
				walk.lexicalOnly[id] = true
			}
		}
		walk.lexicalCandidates++
	}
	for _, id := range walk.order {
		if seen[id] {
			continue
		}
		seen[id] = true
		order = append(order, id)
	}
	walk.order = order
	return walk
}

func eligibleShortlistCandidate(node graph.Node) bool {
	// Ranked candidate mode is for source-bearing files and declarations.
	// Directory nodes are useful traversal bridges, but returning several
	// matching directory prefixes crowds out the definitions they contain.
	if node.Kind == graph.NodeImport || node.Kind == graph.NodeDir {
		return false
	}
	return node.Meta == nil || node.Meta["resolved"] != "false"
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
	docIndex, ok := idx.byID[from]
	if !ok {
		return result
	}
	for _, adjacent := range idx.outgoing[docIndex] {
		if adjacent.edge.Kind == kind {
			result = append(result, cloneEdge(adjacent.edge))
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
	if len(options.TraceNodeIDs) > maximumTraceNodes {
		return normalizedRetrieveOptions{}, fmt.Errorf("trace node IDs must contain at most %d values", maximumTraceNodes)
	}
	seenTraceIDs := make(map[string]bool, len(options.TraceNodeIDs))
	traceNodeIDs := make([]string, 0, len(options.TraceNodeIDs))
	for _, id := range options.TraceNodeIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return normalizedRetrieveOptions{}, errors.New("trace node IDs must not contain empty values")
		}
		if !seenTraceIDs[id] {
			seenTraceIDs[id] = true
			traceNodeIDs = append(traceNodeIDs, id)
		}
	}
	options.TraceNodeIDs = traceNodeIDs

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
	anchored := make([]int, 0, min(limit, len(ranked)))
	for _, candidate := range ranked {
		if candidate.anchored && graph.SymbolKind(idx.docs[candidate.index].node.Kind) {
			anchored = append(anchored, candidate.index)
			if len(anchored) >= limit {
				break
			}
		}
	}
	if len(anchored) > 0 {
		return anchored
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

type queryAdjacency struct {
	idx       *Index
	options   normalizedRetrieveOptions
	scores    map[string]int
	degree    []int
	neighbors map[int][]adjacentEdge
}

func (idx *Index) newQueryAdjacency(options normalizedRetrieveOptions, scores map[string]int) *queryAdjacency {
	return &queryAdjacency{
		idx: idx, options: options, scores: scores,
		degree: idx.degreeForOptions(options), neighbors: map[int][]adjacentEdge{},
	}
}

func (idx *Index) degreeForOptions(options normalizedRetrieveOptions) []int {
	if !options.filterEdges {
		switch options.Direction {
		case DirectionOut:
			return idx.outDegree
		case DirectionIn:
			return idx.inDegree
		default:
			return idx.bothDegree
		}
	}
	degree := make([]int, len(idx.docs))
	for fromIndex, edges := range idx.outgoing {
		for _, adjacent := range edges {
			if !options.relationSet[adjacent.edge.Kind] {
				continue
			}
			switch options.Direction {
			case DirectionOut:
				degree[fromIndex]++
			case DirectionIn:
				degree[adjacent.nodeIndex]++
			case DirectionBoth:
				degree[fromIndex]++
				if fromIndex != adjacent.nodeIndex {
					degree[adjacent.nodeIndex]++
				}
			}
		}
	}
	return degree
}

func (adjacency *queryAdjacency) degreeOf(nodeID string) int {
	docIndex, ok := adjacency.idx.byID[nodeID]
	if !ok {
		return 0
	}
	return adjacency.degree[docIndex]
}

func (adjacency *queryAdjacency) nodeID(edge adjacentEdge) string {
	return adjacency.idx.docs[edge.nodeIndex].node.ID
}

func (adjacency *queryAdjacency) neighborsOf(nodeID string) []adjacentEdge {
	docIndex, ok := adjacency.idx.byID[nodeID]
	if !ok {
		return nil
	}
	if cached, ok := adjacency.neighbors[docIndex]; ok {
		return cached
	}
	neighbors := make([]adjacentEdge, 0, adjacency.degree[docIndex])
	add := func(edges []adjacentEdge, skipSelf bool) {
		for _, edge := range edges {
			if skipSelf && edge.nodeIndex == docIndex {
				continue
			}
			if adjacency.options.filterEdges && !adjacency.options.relationSet[edge.edge.Kind] {
				continue
			}
			neighbors = append(neighbors, edge)
		}
	}
	switch adjacency.options.Direction {
	case DirectionOut:
		add(adjacency.idx.outgoing[docIndex], false)
	case DirectionIn:
		add(adjacency.idx.incoming[docIndex], false)
	case DirectionBoth:
		add(adjacency.idx.outgoing[docIndex], false)
		add(adjacency.idx.incoming[docIndex], true)
	}
	if len(neighbors) > 1 {
		origin := adjacency.idx.nodeCommunity(nodeID)
		sort.SliceStable(neighbors, func(i, j int) bool {
			left := neighbors[i]
			right := neighbors[j]
			leftID := adjacency.nodeID(left)
			rightID := adjacency.nodeID(right)
			if adjacency.options.directionPreference != "" && left.outgoing != right.outgoing {
				if adjacency.options.directionPreference == DirectionOut {
					return left.outgoing
				}
				return !left.outgoing
			}
			if adjacency.scores[leftID] != adjacency.scores[rightID] {
				return adjacency.scores[leftID] > adjacency.scores[rightID]
			}
			if relationPriority(left.edge.Kind) != relationPriority(right.edge.Kind) {
				return relationPriority(left.edge.Kind) < relationPriority(right.edge.Kind)
			}
			if adjacency.degree[left.nodeIndex] != adjacency.degree[right.nodeIndex] {
				return adjacency.degree[left.nodeIndex] < adjacency.degree[right.nodeIndex]
			}
			if adjacency.options.CommunityBoost {
				leftSame := origin != "" && adjacency.idx.nodeCommunity(leftID) == origin
				rightSame := origin != "" && adjacency.idx.nodeCommunity(rightID) == origin
				if leftSame != rightSame {
					return leftSame
				}
			}
			if leftID != rightID {
				return leftID < rightID
			}
			return left.edge.ID < right.edge.ID
		})
	}
	adjacency.neighbors[docIndex] = neighbors
	return neighbors
}

func (idx *Index) nodeCommunity(id string) string {
	return idx.communityByID[id]
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

func (idx *Index) traverse(seedIDs []string, seedSet map[string]bool, adjacency *queryAdjacency, options normalizedRetrieveOptions) traversalResult {
	result := traversalResult{distance: map[string]int{}, via: map[string]graph.Edge{}}
	result.hubThreshold = hubThreshold(adjacency.degree, options.HubDegreeThreshold)
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
		idx.traverseDFS(&result, seedIDs, seedSet, adjacency, options.MaxDepth, exploreLimit, result.branchFanout)
	} else {
		idx.traverseBFS(&result, seedIDs, seedSet, adjacency, options.MaxDepth, exploreLimit, result.branchFanout)
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

func (idx *Index) traverseBFS(result *traversalResult, seeds []string, seedSet map[string]bool, adjacency *queryAdjacency, depth, exploreLimit, fanout int) {
	queue := append([]string(nil), seeds...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		currentDepth := result.distance[current]
		if currentDepth >= depth {
			continue
		}
		if !seedSet[current] && result.hubThreshold >= 0 && adjacency.degreeOf(current) >= result.hubThreshold {
			result.hubsSuppressed++
			continue
		}
		expanded := 0
		for _, adjacent := range adjacency.neighborsOf(current) {
			nextID := adjacency.nodeID(adjacent)
			if _, seen := result.distance[nextID]; seen {
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
			result.distance[nextID] = currentDepth + 1
			result.via[nextID] = adjacent.edge
			result.order = append(result.order, nextID)
			queue = append(queue, nextID)
			expanded++
		}
	}
}

func (idx *Index) traverseDFS(result *traversalResult, seeds []string, seedSet map[string]bool, adjacency *queryAdjacency, depth, exploreLimit, fanout int) {
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
			if !seedSet[current.id] && result.hubThreshold >= 0 && adjacency.degreeOf(current.id) >= result.hubThreshold {
				if !suppressed[current.id] {
					suppressed[current.id] = true
					result.hubsSuppressed++
				}
				continue
			}
			edges := adjacency.neighborsOf(current.id)
			chosen := make([]adjacentEdge, 0, min(fanout, len(edges)))
			for _, adjacent := range edges {
				nextID := adjacency.nodeID(adjacent)
				nextDepth := current.depth + 1
				previousDepth, seen := result.distance[nextID]
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
				nextID := adjacency.nodeID(adjacent)
				nextDepth := current.depth + 1
				if len(result.distance) >= exploreLimit {
					result.exploredLimit = true
					return
				}
				result.distance[nextID] = nextDepth
				result.via[nextID] = adjacent.edge
				stack = append(stack, item{id: nextID, depth: nextDepth})
			}
		}
	}
}

func hubThreshold(degree []int, configured int) int {
	if configured == -1 {
		return -1
	}
	if configured > 0 {
		return configured
	}
	if len(degree) == 0 {
		return 50
	}
	maxDegree := 0
	for _, value := range degree {
		maxDegree = max(maxDegree, value)
	}
	if maxDegree >= len(degree) {
		values := append([]int(nil), degree...)
		sort.Ints(values)
		index := int(math.Ceil(float64(len(values))*0.99)) - 1
		return max(50, values[max(0, min(index, len(values)-1))])
	}
	counts := make([]int, maxDegree+1)
	for _, value := range degree {
		counts[value]++
	}
	target := int(math.Ceil(float64(len(degree)) * 0.99))
	seen := 0
	for value, count := range counts {
		seen += count
		if seen >= target {
			return max(50, value)
		}
	}
	return 50
}

func (idx *Index) fitRetrieval(question string, options normalizedRetrieveOptions, seedIDs []string, seedSet map[string]bool, scores map[string]int, degree []int, walk traversalResult, traces []RetrievalTrace) Retrieval {
	stats := RetrievalStats{
		Traversal: options.Traversal, Direction: options.Direction, DirectionPreference: options.directionPreference, Depth: options.MaxDepth,
		SeedIDs: append([]string(nil), seedIDs...), RelationFilters: append([]graph.EdgeKind(nil), options.Relations...),
		RelationFilterFrom: options.filterFrom, TokenBudget: options.TokenBudget,
		CommunityBoost:  options.CommunityBoost,
		SameFileRescues: append([]SameFileRescue(nil), walk.sameFileRescues...),
		AffinityRescues: append([]AffinityRescue(nil), walk.affinityRescues...),
		HubThreshold:    walk.hubThreshold, BranchFanout: walk.branchFanout, HubsSuppressed: walk.hubsSuppressed,
		BranchesPruned: walk.branchesPruned, ExploredNodes: len(walk.order), LexicalCandidates: walk.lexicalCandidates,
		StructuredCandidates:   walk.structuredCandidates,
		StructuredQueryAnchors: walk.structuredQueryAnchors,
		TraceNodes:             traces,
	}
	outputTokenBudget := options.TokenBudget
	if options.CandidateShortlist {
		outputTokenBudget = adaptiveShortlistTokenBudget(
			options.TokenBudget, walk.structuredCandidates, walk.structuredQueryAnchors,
		)
		stats.OutputTokenBudget = outputTokenBudget
	}
	result := Retrieval{Version: 1, Query: safeTextBytes(question, 256), Stats: stats}
	if len(walk.order) == 0 {
		finalizeRetrievalTraces(result.Stats.TraceNodes, 0)
		return result
	}
	headerTokens := lineTokens(retrievalHeader(question, stats))
	budgetUsed := headerTokens
	result.Stats.HeaderTokens = headerTokens
	reserve := lineTokens(truncationLine(RetrievalStats{
		TruncatedReason: []string{"token_budget", "max_nodes", "exploration_limit", "branch_limit"},
		OmittedNodes:    50_000,
		OmittedEdges:    999_999_999,
	}))

	// Collapse repeated file/class/overload representations before applying the
	// token budget. Prefer the higher-scored representation, then concrete code
	// symbols over container nodes when two candidates describe the same name
	// and source path.
	unique := make([]ContextNode, 0, len(walk.order))
	uniqueOwners := make([]string, 0, len(walk.order))
	identityIndex := map[string]int{}
	traceIndex := make(map[string]int, len(result.Stats.TraceNodes))
	for position := range result.Stats.TraceNodes {
		traceIndex[result.Stats.TraceNodes[position].ID] = position
	}
	for walkPosition, nodeID := range walk.order {
		docIndex, ok := idx.byID[nodeID]
		if !ok {
			continue
		}
		node := contextNode(idx.docs[docIndex].node, scores[nodeID], walk.distance[nodeID], degree[docIndex], seedSet[nodeID])
		if position, traced := traceIndex[nodeID]; traced {
			result.Stats.TraceNodes[position].WalkRank = walkPosition + 1
		}
		if edge, hasVia := walk.via[nodeID]; hasVia {
			node.ViaEdgeID = stableEdgeID(edge)
		}
		identity := contextCandidateIdentity(node)
		if position, duplicate := identityIndex[identity]; duplicate {
			result.Stats.DeduplicatedNodes++
			if betterContextCandidate(node, unique[position]) {
				if traced, ok := traceIndex[uniqueOwners[position]]; ok {
					result.Stats.TraceNodes[traced].Deduplicated = true
				}
				unique[position] = node
				uniqueOwners[position] = nodeID
			} else if traced, ok := traceIndex[nodeID]; ok {
				result.Stats.TraceNodes[traced].Deduplicated = true
			}
			continue
		}
		identityIndex[identity] = len(unique)
		unique = append(unique, node)
		uniqueOwners = append(uniqueOwners, nodeID)
	}
	if options.CandidateShortlist {
		unique, uniqueOwners = diversifyShortlistCandidates(
			unique, uniqueOwners, shortlistIdentityCopies,
		)
		unique, uniqueOwners = prioritizeGraphCandidates(
			unique, uniqueOwners, walk.lexicalOnly, walk.sameFileRescued,
			shortlistLexicalPrefix, shortlistGraphReserve, shortlistSameFileSlot,
		)
	}

	maxCandidates := min(len(unique), options.MaxNodes)
	candidates := unique[:maxCandidates]
	for candidatePosition, nodeID := range uniqueOwners {
		if position, traced := traceIndex[nodeID]; traced {
			result.Stats.TraceNodes[position].CandidateRank = candidatePosition + 1
		}
	}
	if len(unique) > options.MaxNodes {
		result.Stats.Truncated = true
		result.Stats.TruncatedReason = appendReason(result.Stats.TruncatedReason, "max_nodes")
		result.Stats.OmittedNodes += len(unique) - options.MaxNodes
	}

	// Spend most of the available envelope on a ranked candidate shortlist and
	// reserve the remainder for explanations. Traversal nodes outside this
	// shortlist are internal discovery alternatives, not token-truncated output.
	// Count them explicitly so consumers can distinguish ranking selection from
	// an admitted candidate that could not fit the hard envelope.
	candidatePercent := 65
	if options.CandidateShortlist {
		candidatePercent = candidateBudgetPercent
	}
	primaryLimit := budgetUsed
	if available := outputTokenBudget - reserve - budgetUsed; available > 0 {
		primaryLimit += available * candidatePercent / 100
	}
	hardCandidateLimit := outputTokenBudget - reserve
	included := map[string]bool{}
	unselected := make([]ContextNode, 0)
	packCandidate := func(node ContextNode, limit int) bool {
		cost := lineTokens(nodeLine(node))
		if budgetUsed+cost > limit {
			return false
		}
		result.Nodes = append(result.Nodes, node)
		if position, traced := traceIndex[node.ID]; traced {
			result.Stats.TraceNodes[position].ReturnedRank = len(result.Nodes)
		}
		included[node.ID] = true
		budgetUsed += cost
		result.Stats.CandidateTokens += cost
		return true
	}
	for position, node := range candidates {
		limit := primaryLimit
		if options.CandidateShortlist && position == 0 {
			// Always give the top-ranked candidate the full hard envelope before
			// treating it as a genuine token-budget omission.
			limit = hardCandidateLimit
		}
		if !packCandidate(node, limit) {
			unselected = append(unselected, node)
		}
	}

	includedEdges := map[string]bool{}
	explanations := make([]ContextEdge, 0, maximumExplanationEdges)
	for _, node := range result.Nodes {
		if node.ViaEdgeID == "" {
			continue
		}
		edge, ok := walk.via[node.ID]
		if !ok || !included[edge.From] || !included[edge.To] {
			continue
		}
		candidate := contextEdge(edge)
		if !includedEdges[candidate.ID] {
			includedEdges[candidate.ID] = true
			explanations = append(explanations, candidate)
		}
	}
	for _, edge := range idx.edgesWithin(included, options.relationSet) {
		if !includedEdges[edge.ID] {
			includedEdges[edge.ID] = true
			explanations = append(explanations, edge)
		}
	}
	returnedRanks := make(map[string]int, len(result.Nodes))
	for position, node := range result.Nodes {
		returnedRanks[node.ID] = position + 1
	}
	prioritizeExplanationEdges(explanations, returnedRanks, options.directionPreference)
	includedEdges = map[string]bool{}
	omittedExplanationEdges := map[string]bool{}
	for _, edge := range explanations {
		if len(result.Edges) >= maximumExplanationEdges {
			if !omittedExplanationEdges[edge.ID] {
				omittedExplanationEdges[edge.ID] = true
				result.Stats.ExplanationEdgesOmitted++
			}
			continue
		}
		cost := lineTokens(edgeLine(edge))
		if budgetUsed+cost+reserve > outputTokenBudget {
			if !omittedExplanationEdges[edge.ID] {
				omittedExplanationEdges[edge.ID] = true
				result.Stats.ExplanationEdgesOmitted++
			}
			continue
		}
		result.Edges = append(result.Edges, edge)
		includedEdges[edge.ID] = true
		budgetUsed += cost
		result.Stats.ExplanationTokens += cost
	}
	if options.CandidateShortlist {
		// Explanations rarely consume their full reserve. Refill that leftover
		// space with the next ranked candidates so shortlist mode does not return
		// a needlessly underfilled payload. These candidates intentionally do not
		// displace the explanations that were already admitted.
		stillDeferred := unselected[:0]
		for _, node := range unselected {
			if !packCandidate(node, hardCandidateLimit) {
				stillDeferred = append(stillDeferred, node)
			}
		}
		result.Stats.UnselectedNodes = len(stillDeferred)
		if len(result.Nodes) == 0 && len(stillDeferred) > 0 {
			result.Stats.Truncated = true
			result.Stats.TruncatedReason = appendReason(result.Stats.TruncatedReason, "token_budget")
			result.Stats.OmittedNodes += len(stillDeferred)
			result.Stats.UnselectedNodes = 0
		}
	} else {
		stillDeferred := unselected[:0]
		for _, node := range unselected {
			if !packCandidate(node, hardCandidateLimit) {
				stillDeferred = append(stillDeferred, node)
			}
		}
		if len(stillDeferred) > 0 {
			result.Stats.Truncated = true
			result.Stats.TruncatedReason = appendReason(result.Stats.TruncatedReason, "token_budget")
			result.Stats.OmittedNodes += len(stillDeferred)
		}
	}
	for i := range result.Nodes {
		if result.Nodes[i].ViaEdgeID != "" && !includedEdges[result.Nodes[i].ViaEdgeID] {
			if !omittedExplanationEdges[result.Nodes[i].ViaEdgeID] {
				omittedExplanationEdges[result.Nodes[i].ViaEdgeID] = true
				result.Stats.ExplanationEdgesOmitted++
			}
			result.Nodes[i].ViaEdgeID = ""
		}
	}

	if walk.exploredLimit {
		result.Stats.Truncated = true
		result.Stats.TruncatedReason = appendReason(result.Stats.TruncatedReason, "exploration_limit")
	}
	if walk.branchesPruned > 0 {
		result.Stats.Truncated = true
		result.Stats.TruncatedReason = appendReason(result.Stats.TruncatedReason, "branch_limit")
	}
	result.Stats.EstimatedTokens = budgetUsed
	if result.Stats.Truncated {
		result.Stats.EstimatedTokens += lineTokens(truncationLine(result.Stats))
	}
	finalizeRetrievalTraces(result.Stats.TraceNodes, maxCandidates)
	return result
}

// diversifyShortlistCandidates keeps a bounded number of same-kind,
// same-name alternatives near the front, then defers further copies without
// discarding them. This preserves overloads and competing definitions while
// preventing repeated file or symbol labels from consuming the whole compact
// shortlist before another relevant candidate appears.
func diversifyShortlistCandidates(nodes []ContextNode, owners []string, copies int) ([]ContextNode, []string) {
	if copies <= 0 || len(nodes) < 2 || len(nodes) != len(owners) {
		return nodes, owners
	}
	counts := map[string]int{}
	selected := make([]bool, len(nodes))
	orderedNodes := make([]ContextNode, 0, len(nodes))
	orderedOwners := make([]string, 0, len(owners))
	for position, node := range nodes {
		key := string(node.Kind) + "\x00" + normalizeSearchText(strings.TrimSuffix(node.Name, "()"))
		limit := copies
		if node.Kind == graph.NodeFile {
			limit = 1
		}
		if counts[key] >= limit {
			continue
		}
		counts[key]++
		selected[position] = true
		orderedNodes = append(orderedNodes, node)
		orderedOwners = append(orderedOwners, owners[position])
	}
	for position, node := range nodes {
		if selected[position] {
			continue
		}
		orderedNodes = append(orderedNodes, node)
		orderedOwners = append(orderedOwners, owners[position])
	}
	return orderedNodes, orderedOwners
}

func prioritizeGraphCandidates(nodes []ContextNode, owners []string, lexicalOnly, sameFileRescued map[string]bool, prefix, reserve, sameFileSlot int) ([]ContextNode, []string) {
	if len(nodes) <= prefix || reserve <= 0 {
		return nodes, owners
	}
	selected := make([]bool, len(nodes))
	orderedNodes := make([]ContextNode, 0, len(nodes))
	orderedOwners := make([]string, 0, len(owners))
	add := func(position int) {
		selected[position] = true
		orderedNodes = append(orderedNodes, nodes[position])
		orderedOwners = append(orderedOwners, owners[position])
	}
	for position := 0; position < min(prefix, len(nodes)); position++ {
		add(position)
	}
	sameFileCount := 0
	for position := prefix; position < len(nodes); position++ {
		if sameFileRescued[owners[position]] {
			sameFileCount++
		}
	}
	graphReserve := max(0, reserve-min(reserve, sameFileCount))
	graphBeforeRescue := min(graphReserve, max(0, sameFileSlot-1))
	for position := prefix; position < len(nodes) && graphBeforeRescue > 0; position++ {
		if !lexicalOnly[owners[position]] && !sameFileRescued[owners[position]] {
			add(position)
			graphBeforeRescue--
			reserve--
		}
	}
	for position := prefix; position < len(nodes) && reserve > 0; position++ {
		if sameFileRescued[owners[position]] {
			add(position)
			reserve--
		}
	}
	for position := prefix; position < len(nodes) && reserve > 0; position++ {
		if !selected[position] && !lexicalOnly[owners[position]] {
			add(position)
			reserve--
		}
	}
	for position := range nodes {
		if !selected[position] {
			add(position)
		}
	}
	return orderedNodes, orderedOwners
}

func finalizeRetrievalTraces(traces []RetrievalTrace, maxCandidates int) {
	for position := range traces {
		trace := &traces[position]
		switch {
		case trace.ReturnedRank > 0:
		case !trace.Indexed:
			trace.DroppedReason = "not_indexed"
		case trace.WalkRank == 0:
			trace.DroppedReason = "not_reached"
		case trace.Deduplicated:
			trace.DroppedReason = "deduplicated"
		case trace.CandidateRank > maxCandidates:
			trace.DroppedReason = "max_nodes"
		case trace.CandidateRank > 0:
			trace.DroppedReason = "token_budget"
		default:
			trace.DroppedReason = "not_candidate"
		}
	}
}

func contextCandidateIdentity(node ContextNode) string {
	name := normalizeSearchText(strings.TrimSuffix(node.Name, "()"))
	if node.Kind == graph.NodeFile {
		for _, extension := range []string{" go", " java", " py", " js", " jsx", " ts", " tsx", " kt", " kts", " swift", " rs", " rb", " php", " cs", " cpp", " c", " h"} {
			name = strings.TrimSuffix(name, extension)
		}
	}
	path := strings.ToLower(strings.ReplaceAll(node.Path, "\\", "/"))
	if path == "" {
		return node.ID
	}
	return path + "\x00" + name
}

func betterContextCandidate(left, right ContextNode) bool {
	if left.Score != right.Score {
		return left.Score > right.Score
	}
	leftSymbol, rightSymbol := graph.SymbolKind(left.Kind), graph.SymbolKind(right.Kind)
	if leftSymbol != rightSymbol {
		return leftSymbol
	}
	if left.Seed != right.Seed {
		return left.Seed
	}
	return left.ID < right.ID
}

func (idx *Index) edgesWithin(nodes map[string]bool, relationSet map[graph.EdgeKind]bool) []ContextEdge {
	var result []ContextEdge
	for nodeID := range nodes {
		docIndex, ok := idx.byID[nodeID]
		if !ok {
			continue
		}
		for _, adjacent := range idx.outgoing[docIndex] {
			edge := adjacent.edge
			if !nodes[edge.To] {
				continue
			}
			if len(relationSet) > 0 && !relationSet[edge.Kind] {
				continue
			}
			result = append(result, contextEdge(edge))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if relationPriority(result[i].Kind) != relationPriority(result[j].Kind) {
			return relationPriority(result[i].Kind) < relationPriority(result[j].Kind)
		}
		return result[i].ID < result[j].ID
	})
	return result
}

// prioritizeExplanationEdges promotes only the evidence most likely to explain
// the top result: its direct relationship with the runner-up, plus two edges in
// an explicitly requested direction or one incoming and outgoing edge for a
// mixed question. The remainder retains discovery order so a high-ranked but
// incidental node cannot displace a useful call chain from a tight payload.
func prioritizeExplanationEdges(edges []ContextEdge, ranks map[string]int, directionPreference Direction) {
	if len(edges) < 2 || len(ranks) == 0 {
		return
	}

	var top, runnerUp string
	for id, rank := range ranks {
		switch rank {
		case 1:
			top = id
		case 2:
			runnerUp = id
		}
	}
	if top == "" {
		return
	}

	preferred := map[string]bool{}
	markFirst := func(limit int, matches func(ContextEdge) bool) {
		for _, edge := range edges {
			if len(preferred) >= limit {
				return
			}
			if matches(edge) {
				preferred[edge.ID] = true
			}
		}
	}
	switch directionPreference {
	case DirectionIn:
		markFirst(2, func(edge ContextEdge) bool { return edge.To == top })
	case DirectionOut:
		markFirst(2, func(edge ContextEdge) bool { return edge.From == top })
	default:
		markFirst(1, func(edge ContextEdge) bool { return edge.To == top })
		markFirst(2, func(edge ContextEdge) bool { return edge.From == top })
	}

	priority := func(edge ContextEdge) int {
		if runnerUp != "" && ((edge.From == top && edge.To == runnerUp) || (edge.From == runnerUp && edge.To == top)) {
			return 0
		}
		if preferred[edge.ID] {
			return 1
		}
		return 2
	}
	sort.SliceStable(edges, func(i, j int) bool {
		return priority(edges[i]) < priority(edges[j])
	})
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
	// Keep discovery and explanation as distinct phases: emit the complete
	// compact candidate list first, then the bounded evidence edges.
	for _, node := range result.Nodes {
		if _, err := fmt.Fprintln(w, nodeLine(node)); err != nil {
			return err
		}
	}
	for _, edge := range result.Edges {
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
	if stats.OutputTokenBudget > 0 && stats.OutputTokenBudget != stats.TokenBudget {
		parts = append(parts, "output_budget="+strconv.Itoa(stats.OutputTokenBudget))
	}
	if stats.DirectionPreference != "" {
		parts = append(parts, "preference="+string(stats.DirectionPreference))
	}
	if stats.CommunityBoost {
		parts = append(parts, "community_boost=true")
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
	parts := []string{prefix, safeText(string(node.Kind)), compactID(node.ID), quoteField(node.Name)}
	if node.Depth > 0 {
		parts = append(parts, "depth="+strconv.Itoa(node.Depth))
	}
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
