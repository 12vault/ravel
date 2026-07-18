package query

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/graph"
)

func TestRetrieveUsesMultipleSeedsToCoverDistinctQueryTerms(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "node://auth", Kind: graph.NodeDomain, Name: "Authentication"},
			{ID: "node://invoice", Kind: graph.NodeDomain, Name: "Invoice"},
			{ID: "node://session", Kind: graph.NodeConcept, Name: "SessionStore"},
			{ID: "node://ledger", Kind: graph.NodeConcept, Name: "LedgerStore"},
		},
		Edges: []graph.Edge{
			testQueryEdge(graph.EdgeContains, "node://auth", "node://session"),
			testQueryEdge(graph.EdgeContains, "node://invoice", "node://ledger"),
		},
	}

	result := mustRetrieve(t, NewIndex(g), "authentication invoice", RetrieveOptions{
		Traversal: TraversalBFS, Direction: DirectionOut, DisableRelationInference: true,
		SeedLimit: 2, MaxDepth: 1, MaxNodes: 20, HubDegreeThreshold: -1, TokenBudget: 100_000,
	})
	if got, want := sortedStrings(result.Stats.SeedIDs), []string{"node://auth", "node://invoice"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("seed IDs = %v, want multi-term coverage %v", got, want)
	}
	for _, id := range []string{"node://auth", "node://invoice"} {
		node, ok := retrievalNode(result, id)
		if !ok || !node.Seed || node.Depth != 0 {
			t.Fatalf("seed %q = %#v, %v; want depth-zero seed", id, node, ok)
		}
	}
	for _, id := range []string{"node://session", "node://ledger"} {
		node, ok := retrievalNode(result, id)
		if !ok || node.Seed || node.Depth != 1 {
			t.Fatalf("expanded node %q = %#v, %v; want non-seed at depth one", id, node, ok)
		}
	}
}

func TestRetrieveCoversCacheQueryTermsBeforeGlobalScoreFill(t *testing.T) {
	nodes := []graph.Node{
		{ID: "node://graph", Kind: graph.NodeConcept, Name: "Graph"},
		{ID: "node://query", Kind: graph.NodeConcept, Name: "Query"},
		{ID: "node://cache", Kind: graph.NodeConcept, Name: "Cache"},
		{ID: "node://workflow", Kind: graph.NodeConcept, Name: "Workflow"},
	}
	reversed := append([]graph.Node(nil), nodes...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	options := RetrieveOptions{
		Traversal: TraversalBFS, Direction: DirectionBoth, DisableRelationInference: true,
		SeedLimit: 3, MaxDepth: 1, MaxNodes: 10, HubDegreeThreshold: -1, TokenBudget: 100_000,
	}
	var wantOrder []string
	for run, candidateNodes := range [][]graph.Node{nodes, reversed} {
		result := mustRetrieve(t, NewIndex(graph.Graph{Nodes: candidateNodes}), "how does the graph query cache work", options)
		if got, want := sortedStrings(result.Stats.SeedIDs), []string{"node://cache", "node://graph", "node://query"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d seed IDs = %v, want informative coverage %v", run, got, want)
		}
		seen := map[string]bool{}
		for _, id := range result.Stats.SeedIDs {
			if seen[id] {
				t.Fatalf("run %d duplicated seed %q: %v", run, id, result.Stats.SeedIDs)
			}
			seen[id] = true
		}
		if len(result.Stats.SeedIDs) > options.SeedLimit {
			t.Fatalf("run %d used %d seeds, limit %d", run, len(result.Stats.SeedIDs), options.SeedLimit)
		}
		if run == 0 {
			wantOrder = append([]string(nil), result.Stats.SeedIDs...)
		} else if !reflect.DeepEqual(result.Stats.SeedIDs, wantOrder) {
			t.Fatalf("run %d seed order = %v, want deterministic %v", run, result.Stats.SeedIDs, wantOrder)
		}
	}
}

func TestRetrieveDoesNotFillMultiTermSeedSlotsWithCoveredNoise(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{
		{ID: "node://benchmark", Kind: graph.NodeFunction, Name: "RunBenchmark"},
		{ID: "node://context", Kind: graph.NodeFunction, Name: "RunContext"},
		{ID: "node://noise", Kind: graph.NodeImport, Name: "Context"},
	}}
	result := mustRetrieve(t, NewIndex(g), "run benchmark context", RetrieveOptions{SeedLimit: 3, TokenBudget: 100_000})
	if containsString(result.Stats.SeedIDs, "node://noise") {
		t.Fatalf("covered-term noise consumed a seed slot: %v", result.Stats.SeedIDs)
	}
	if got, want := sortedStrings(result.Stats.SeedIDs), []string{"node://benchmark", "node://context"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("seed IDs = %v, want informative seeds %v", got, want)
	}
}

func TestRetrieveUsesInlineIdentifierAnchorAsOnlySeed(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "root", Kind: graph.NodeFunction, Name: "runBenchmark"},
			{ID: "noise", Kind: graph.NodeFunction, Name: "TestRunBenchmarkContextRetrievalScoring"},
			{ID: "target", Kind: graph.NodeFunction, Name: "Retrieve"},
		},
		Edges: []graph.Edge{testQueryEdge(graph.EdgeCalls, "root", "target")},
	}
	result := mustRetrieve(t, NewIndex(g), "what does runBenchmark call for context retrieval scoring", RetrieveOptions{
		Direction: DirectionOut, Relations: []graph.EdgeKind{graph.EdgeCalls},
		SeedLimit: 3, MaxDepth: 1, MaxNodes: 10, HubDegreeThreshold: -1, TokenBudget: 1_000,
	})
	if got, want := result.Stats.SeedIDs, []string{"root"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("seed IDs = %v, want exact inline identifier anchor %v", got, want)
	}
	if _, ok := retrievalNode(result, "target"); !ok {
		t.Fatalf("anchored traversal omitted target: %v", retrievalNodeIDs(result))
	}
}

func TestRankWithAnchorsCanExcludeInlineIdentifiersWithoutDroppingStructuredImports(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{
		{ID: "inline", Kind: graph.NodeFunction, Name: "runBenchmark"},
		{ID: "imported", Kind: graph.NodeFunction, Name: "Retrieve"},
	}}
	idx := NewIndex(g)
	question := "what does runBenchmark call\nimport example.Retrieve;"
	terms := strings.Join(retrievalTerms(question, nil), " ")

	withInline := idx.rankWithAnchors(terms, question, true)
	withoutInline := idx.rankWithAnchors(terms, question, false)
	anchored := func(ranked []rankedNode) map[string]bool {
		result := map[string]bool{}
		for _, candidate := range ranked {
			if candidate.anchored {
				result[idx.docs[candidate.index].node.ID] = true
			}
		}
		return result
	}
	if got := anchored(withInline); !reflect.DeepEqual(got, map[string]bool{"inline": true, "imported": true}) {
		t.Fatalf("anchors with inline identifiers = %v", got)
	}
	if got := anchored(withoutInline); !reflect.DeepEqual(got, map[string]bool{"imported": true}) {
		t.Fatalf("anchors without inline identifiers = %v, want structured import only", got)
	}
}

func TestRetrievePromotesStructuredImportsWithoutExpandingEveryCandidate(t *testing.T) {
	nodes := make([]graph.Node, 0, 25)
	var question strings.Builder
	question.WriteString("Which cross-file definition is needed?\nImports:\n")
	for i := 0; i < 25; i++ {
		name := fmt.Sprintf("ImportedType%02d", i)
		nodes = append(nodes, graph.Node{
			ID: fmt.Sprintf("type-%02d", i), Kind: graph.NodeClass, Name: name,
			Path: fmt.Sprintf("src/main/java/example/%s.java", name),
		})
		fmt.Fprintf(&question, "import example.%s;\n", name)
	}
	result := mustRetrieve(t, NewIndex(graph.Graph{Nodes: nodes}), question.String(), RetrieveOptions{
		SeedLimit: 1, MaxNodes: 100, TokenBudget: 100_000,
	})
	if len(result.Stats.SeedIDs) != 1 {
		t.Fatalf("seed IDs = %v, want configured traversal seed limit", result.Stats.SeedIDs)
	}
	if result.Stats.LexicalCandidates != 25 || len(result.Nodes) != 25 {
		t.Fatalf("lexical candidates = %d, nodes = %d; want all 25 imports", result.Stats.LexicalCandidates, len(result.Nodes))
	}
	if _, ok := retrievalNode(result, "type-24"); !ok {
		t.Fatalf("last non-seed import was not exposed: %v", retrievalNodeIDs(result))
	}
}

func TestRetrieveDeduplicatesCandidatesAndWritesAllCandidatesBeforeEdges(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "root", Kind: graph.NodeFunction, Name: "PackingRoot"},
			{ID: "file", Kind: graph.NodeFile, Name: "Worker.java", Path: "src/Worker.java"},
			{ID: "class", Kind: graph.NodeClass, Name: "Worker", Path: "src/Worker.java"},
			{ID: "class-c", Kind: graph.NodeClass, Name: "WorkerC", Path: "src/Worker.java"},
			{ID: "run-1", Kind: graph.NodeMethod, Name: "run", Path: "src/Worker.java"},
			{ID: "run-2", Kind: graph.NodeMethod, Name: "run", Path: "src/Worker.java"},
		},
		Edges: []graph.Edge{
			testQueryEdge(graph.EdgeCalls, "root", "file"),
			testQueryEdge(graph.EdgeCalls, "root", "class"),
			testQueryEdge(graph.EdgeCalls, "root", "class-c"),
			testQueryEdge(graph.EdgeCalls, "root", "run-1"),
			testQueryEdge(graph.EdgeCalls, "root", "run-2"),
		},
	}
	result := mustRetrieve(t, NewIndex(g), "PackingRoot", RetrieveOptions{
		Direction: DirectionOut, DisableRelationInference: true, SeedLimit: 1,
		MaxDepth: 1, MaxNodes: 20, HubDegreeThreshold: -1, TokenBudget: 100_000,
	})
	if got, want := retrievalNodeIDs(result), []string{"root", "class", "class-c", "run-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deduplicated nodes = %v, want %v", got, want)
	}
	if result.Stats.DeduplicatedNodes != 2 {
		t.Fatalf("deduplicated node count = %d, want 2", result.Stats.DeduplicatedNodes)
	}
	lines := strings.Split(writeRetrievalText(t, result), "\n")
	sawEdge := false
	for _, line := range lines {
		if strings.HasPrefix(line, "EDGE\t") {
			sawEdge = true
			continue
		}
		if sawEdge && (strings.HasPrefix(line, "NODE\t") || strings.HasPrefix(line, "SEED\t")) {
			t.Fatalf("candidate emitted after explanation edge:\n%s", strings.Join(lines, "\n"))
		}
	}
}

func TestRetrieveBFSAndDFSOrdering(t *testing.T) {
	g := traversalFixture()
	base := RetrieveOptions{
		Direction: DirectionOut, DisableRelationInference: true, SeedLimit: 1,
		MaxDepth: 2, MaxNodes: 20, HubDegreeThreshold: -1, TokenBudget: 100_000,
	}

	for _, tc := range []struct {
		traversal Traversal
		want      []string
	}{
		{traversal: TraversalBFS, want: []string{"root", "a", "b", "a1", "b1"}},
		{traversal: TraversalDFS, want: []string{"root", "a", "a1", "b", "b1"}},
	} {
		t.Run(string(tc.traversal), func(t *testing.T) {
			options := base
			options.Traversal = tc.traversal
			result := mustRetrieve(t, NewIndex(g), "TraversalRoot", options)
			if got := retrievalNodeIDs(result); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%s node order = %v, want %v", tc.traversal, got, tc.want)
			}
		})
	}
}

func TestRetrieveBoundsWideBranchesSoRelevantGrandchildrenFit(t *testing.T) {
	nodes := []graph.Node{
		{ID: "root", Kind: graph.NodeFunction, Name: "RootTargetCritical"},
		{ID: "critical", Kind: graph.NodeFunction, Name: "Critical"},
		{ID: "deep", Kind: graph.NodeFunction, Name: "DeepEvidence"},
	}
	edges := []graph.Edge{
		testQueryEdge(graph.EdgeCalls, "root", "critical"),
		testQueryEdge(graph.EdgeCalls, "critical", "deep"),
	}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("noise-%02d", i)
		nodes = append(nodes, graph.Node{ID: id, Kind: graph.NodeFunction, Name: id})
		edges = append(edges, testQueryEdge(graph.EdgeCalls, "root", id))
	}
	g := graph.Graph{Nodes: nodes, Edges: edges}
	result := mustRetrieve(t, NewIndex(g), "RootTargetCritical", RetrieveOptions{
		Traversal: TraversalBFS, Direction: DirectionOut, Relations: []graph.EdgeKind{graph.EdgeCalls},
		SeedLimit: 1, MaxDepth: 2, MaxNodes: 10, HubDegreeThreshold: -1, TokenBudget: 100_000,
	})
	if _, ok := retrievalNode(result, "deep"); !ok {
		t.Fatalf("wide first layer starved relevant grandchild: nodes = %v", retrievalNodeIDs(result))
	}
	if result.Stats.BranchesPruned == 0 || !containsString(result.Stats.TruncatedReason, "branch_limit") {
		t.Fatalf("branch pruning was not reported: %#v", result.Stats)
	}
	var output bytes.Buffer
	if err := WriteRetrieval(&output, result, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "branch_limit: raise --branch-fanout or narrow relations/depth") || strings.Contains(output.String(), "--token-budget") {
		t.Fatalf("branch-limit hint was not reason-specific: %s", output.String())
	}

	overridden := mustRetrieve(t, NewIndex(g), "RootTargetCritical", RetrieveOptions{
		Traversal: TraversalBFS, Direction: DirectionOut, Relations: []graph.EdgeKind{graph.EdgeCalls},
		SeedLimit: 1, MaxDepth: 2, MaxNodes: 30, BranchFanout: 32, HubDegreeThreshold: -1, TokenBudget: 100_000,
	})
	if overridden.Stats.BranchFanout != 32 || overridden.Stats.BranchesPruned != 0 || containsString(overridden.Stats.TruncatedReason, "branch_limit") {
		t.Fatalf("explicit branch fanout was not honored: %#v", overridden.Stats)
	}
	if _, ok := retrievalNode(overridden, "deep"); !ok {
		t.Fatalf("explicit fanout lost reachable evidence: %v", retrievalNodeIDs(overridden))
	}
}

func TestRetrieveHonorsIncomingOutgoingAndBothDirections(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "target", Kind: graph.NodeFunction, Name: "DirectionTarget"},
			{ID: "caller", Kind: graph.NodeFunction, Name: "UpstreamCaller"},
			{ID: "callee", Kind: graph.NodeFunction, Name: "DownstreamCallee"},
		},
		Edges: []graph.Edge{
			testQueryEdge(graph.EdgeCalls, "caller", "target"),
			testQueryEdge(graph.EdgeCalls, "target", "callee"),
		},
	}

	for _, tc := range []struct {
		direction Direction
		want      []string
	}{
		{direction: DirectionOut, want: []string{"callee", "target"}},
		{direction: DirectionIn, want: []string{"caller", "target"}},
		{direction: DirectionBoth, want: []string{"callee", "caller", "target"}},
	} {
		t.Run(string(tc.direction), func(t *testing.T) {
			result := mustRetrieve(t, NewIndex(g), "DirectionTarget", RetrieveOptions{
				Traversal: TraversalBFS, Direction: tc.direction, DisableRelationInference: true,
				SeedLimit: 1, MaxDepth: 1, MaxNodes: 10, HubDegreeThreshold: -1, TokenBudget: 100_000,
			})
			if got := sortedStrings(retrievalNodeIDs(result)); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("direction %s nodes = %v, want %v", tc.direction, got, tc.want)
			}
		})
	}
}

func TestRetrieveBothDirectionsPrioritizesExplicitQuestionIntent(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "target", Kind: graph.NodeFunction, Name: "Target"},
			{ID: "helper", Kind: graph.NodeFunction, Name: "Helper"},
			{ID: "caller", Kind: graph.NodeFunction, Name: "VerboseTargetCallerTest"},
		},
		Edges: []graph.Edge{
			testQueryEdge(graph.EdgeCalls, "target", "helper"),
			testQueryEdge(graph.EdgeCalls, "caller", "target"),
		},
	}
	idx := NewIndex(g)
	options := RetrieveOptions{Direction: DirectionBoth, Relations: []graph.EdgeKind{graph.EdgeCalls}, SeedLimit: 1, MaxDepth: 1, MaxNodes: 10, HubDegreeThreshold: -1, TokenBudget: 1_000}
	outgoing := mustRetrieve(t, idx, "what does Target call", options)
	if outgoing.Stats.DirectionPreference != DirectionOut || len(outgoing.Nodes) < 2 || outgoing.Nodes[1].ID != "helper" {
		t.Fatalf("outgoing-preferred retrieval = %#v", outgoing)
	}
	incoming := mustRetrieve(t, idx, "who calls Target", options)
	if incoming.Stats.DirectionPreference != DirectionIn || len(incoming.Nodes) < 2 || incoming.Nodes[1].ID != "caller" {
		t.Fatalf("incoming-preferred retrieval = %#v", incoming)
	}
	mixed := mustRetrieve(t, idx, "who calls Target and what does it call", options)
	if mixed.Stats.DirectionPreference != "" {
		t.Fatalf("mixed question preference = %q, want none", mixed.Stats.DirectionPreference)
	}
}

func TestRetrieveSupportsExplicitInferredAndCustomRelationFilters(t *testing.T) {
	custom := graph.EdgeKind("custom_link")
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "root", Kind: graph.NodeFunction, Name: "Dispatcher"},
			{ID: "called", Kind: graph.NodeFunction, Name: "Worker"},
			{ID: "imported", Kind: graph.NodePackage, Name: "Transport"},
			{ID: "custom", Kind: graph.NodeConcept, Name: "Policy"},
		},
		Edges: []graph.Edge{
			testQueryEdge(graph.EdgeCalls, "root", "called"),
			testQueryEdge(graph.EdgeImports, "root", "imported"),
			testQueryEdge(custom, "root", "custom"),
		},
	}
	idx := NewIndex(g)
	base := RetrieveOptions{
		Traversal: TraversalBFS, Direction: DirectionOut, SeedLimit: 1,
		MaxDepth: 1, MaxNodes: 10, HubDegreeThreshold: -1, TokenBudget: 100_000,
	}

	t.Run("explicit", func(t *testing.T) {
		options := base
		options.Relations = []graph.EdgeKind{"", graph.EdgeCalls, graph.EdgeCalls}
		result := mustRetrieve(t, idx, "Dispatcher", options)
		if got, want := retrievalNodeIDs(result), []string{"root", "called"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("explicit calls nodes = %v, want %v", got, want)
		}
		if got, want := result.Stats.RelationFilters, []graph.EdgeKind{graph.EdgeCalls}; !reflect.DeepEqual(got, want) || result.Stats.RelationFilterFrom != "explicit" {
			t.Fatalf("explicit filter stats = (%v, %q), want (%v, explicit)", got, result.Stats.RelationFilterFrom, want)
		}
	})

	t.Run("inferred", func(t *testing.T) {
		result := mustRetrieve(t, idx, "Dispatcher callers", base)
		if got, want := retrievalNodeIDs(result), []string{"root", "called"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("inferred calls nodes = %v, want %v", got, want)
		}
		if got, want := result.Stats.RelationFilters, []graph.EdgeKind{graph.EdgeCalls}; !reflect.DeepEqual(got, want) || result.Stats.RelationFilterFrom != "inferred" {
			t.Fatalf("inferred filter stats = (%v, %q), want (%v, inferred)", got, result.Stats.RelationFilterFrom, want)
		}
	})

	t.Run("inference disabled", func(t *testing.T) {
		options := base
		options.DisableRelationInference = true
		result := mustRetrieve(t, idx, "Dispatcher callers", options)
		if got, want := sortedStrings(retrievalNodeIDs(result)), []string{"called", "custom", "imported", "root"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("unfiltered nodes = %v, want %v", got, want)
		}
		if len(result.Stats.RelationFilters) != 0 || result.Stats.RelationFilterFrom != "" {
			t.Fatalf("disabled inference stats = (%v, %q), want no filter", result.Stats.RelationFilters, result.Stats.RelationFilterFrom)
		}
	})

	t.Run("custom", func(t *testing.T) {
		options := base
		options.Relations = []graph.EdgeKind{custom}
		result := mustRetrieve(t, idx, "Dispatcher", options)
		if got, want := retrievalNodeIDs(result), []string{"root", "custom"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("custom relation nodes = %v, want %v", got, want)
		}
		if got, want := result.Stats.RelationFilters, []graph.EdgeKind{custom}; !reflect.DeepEqual(got, want) || result.Stats.RelationFilterFrom != "explicit" {
			t.Fatalf("custom filter stats = (%v, %q), want (%v, explicit)", got, result.Stats.RelationFilterFrom, want)
		}
	})
}

func TestRetrieveSuppressesHubExpansionUnlessDisabled(t *testing.T) {
	nodes := []graph.Node{
		{ID: "root", Kind: graph.NodeFunction, Name: "HubRoot"},
		{ID: "hub", Kind: graph.NodePackage, Name: "Connector"},
	}
	edges := []graph.Edge{testQueryEdge(graph.EdgeCalls, "root", "hub")}
	for _, id := range []string{"leaf-a", "leaf-b", "leaf-c"} {
		nodes = append(nodes, graph.Node{ID: id, Kind: graph.NodeFunction, Name: id})
		edges = append(edges, testQueryEdge(graph.EdgeCalls, "hub", id))
	}
	idx := NewIndex(graph.Graph{Nodes: nodes, Edges: edges})
	base := RetrieveOptions{
		Traversal: TraversalBFS, Direction: DirectionOut, DisableRelationInference: true,
		SeedLimit: 1, MaxDepth: 2, MaxNodes: 20, TokenBudget: 100_000,
	}

	suppressedOptions := base
	suppressedOptions.HubDegreeThreshold = 3
	suppressed := mustRetrieve(t, idx, "HubRoot", suppressedOptions)
	if got, want := retrievalNodeIDs(suppressed), []string{"root", "hub"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("suppressed nodes = %v, want %v", got, want)
	}
	if suppressed.Stats.HubsSuppressed != 1 || suppressed.Stats.HubThreshold != 3 {
		t.Fatalf("suppression stats = %#v, want one hub at threshold three", suppressed.Stats)
	}

	disabledOptions := base
	disabledOptions.HubDegreeThreshold = -1
	disabled := mustRetrieve(t, idx, "HubRoot", disabledOptions)
	if got, want := sortedStrings(retrievalNodeIDs(disabled)), []string{"hub", "leaf-a", "leaf-b", "leaf-c", "root"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("disabled suppression nodes = %v, want %v", got, want)
	}
	if disabled.Stats.HubsSuppressed != 0 || disabled.Stats.HubThreshold != -1 {
		t.Fatalf("disabled suppression stats = %#v", disabled.Stats)
	}
}

func TestRetrieveIgnoresEdgesWithMissingEndpoints(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "root", Kind: graph.NodeFunction, Name: "EndpointRoot"},
			{ID: "valid", Kind: graph.NodeFunction, Name: "ValidTarget"},
		},
		Edges: []graph.Edge{
			testQueryEdge(graph.EdgeCalls, "root", "valid"),
			testQueryEdge(graph.EdgeCalls, "root", "missing"),
			testQueryEdge(graph.EdgeCalls, "missing", "valid"),
		},
	}

	result := mustRetrieve(t, NewIndex(g), "EndpointRoot", RetrieveOptions{
		Traversal: TraversalBFS, Direction: DirectionBoth, Relations: []graph.EdgeKind{graph.EdgeCalls},
		SeedLimit: 1, MaxDepth: 2, MaxNodes: 20, HubDegreeThreshold: -1, TokenBudget: 100_000,
	})
	if got, want := retrievalNodeIDs(result), []string{"root", "valid"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("nodes = %v, want dangling endpoints omitted: %v", got, want)
	}
	if len(result.Edges) != 1 || result.Edges[0].From != "root" || result.Edges[0].To != "valid" {
		t.Fatalf("edges = %#v, want only valid endpoint edge", result.Edges)
	}
	if result.Nodes[0].Degree != 1 {
		t.Fatalf("root degree = %d, want dangling edges excluded", result.Nodes[0].Degree)
	}
}

func TestRetrievePreservesUnresolvedNodeAndEdgeMetadata(t *testing.T) {
	unresolvedID := graph.UnresolvedCallID("missingDependency")
	edge := testQueryEdge(graph.EdgeCalls, "root", unresolvedID)
	edge.Meta = map[string]string{
		"confidence": "low",
		"resolved":   "false",
		"evidence":   "static call candidate",
		"rationale":  "symbol could not be resolved",
		"path":       "internal/root.go",
		"line":       "42",
	}
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "root", Kind: graph.NodeFunction, Name: "MetadataRoot"},
			{ID: unresolvedID, Kind: graph.NodeFunction, Name: "missingDependency", Meta: map[string]string{"confidence": "low", "resolved": "false"}},
		},
		Edges: []graph.Edge{edge},
	}

	result := mustRetrieve(t, NewIndex(g), "MetadataRoot", RetrieveOptions{
		Traversal: TraversalBFS, Direction: DirectionOut, DisableRelationInference: true,
		SeedLimit: 1, MaxDepth: 1, MaxNodes: 10, HubDegreeThreshold: -1, TokenBudget: 100_000,
	})
	node, ok := retrievalNode(result, unresolvedID)
	if !ok || node.Resolved == nil || *node.Resolved || node.Confidence != "low" {
		t.Fatalf("unresolved node = %#v, %v; want resolved=false and confidence=low", node, ok)
	}
	if len(result.Edges) != 1 {
		t.Fatalf("edges = %#v, want one unresolved edge", result.Edges)
	}
	gotEdge := result.Edges[0]
	if gotEdge.Resolved == nil || *gotEdge.Resolved || gotEdge.Confidence != "low" ||
		gotEdge.Evidence != "static call candidate" || gotEdge.Rationale != "symbol could not be resolved" ||
		gotEdge.Path != "internal/root.go" || gotEdge.Line != 42 {
		t.Fatalf("unresolved edge metadata = %#v", gotEdge)
	}

	var output bytes.Buffer
	if err := WriteRetrieval(&output, result, false); err != nil {
		t.Fatalf("WriteRetrieval() error = %v", err)
	}
	for _, fragment := range []string{"resolved=false", "confidence=low", "static call candidate", "symbol could not be resolved"} {
		if !strings.Contains(output.String(), fragment) {
			t.Fatalf("output missing %q:\n%s", fragment, output.String())
		}
	}
}

func TestRetrieveOutputIsDeterministicAcrossRunsAndGraphOrder(t *testing.T) {
	base := traversalFixture()
	base.Edges = append(base.Edges, testQueryEdge(graph.EdgeReferences, "a", "b"))
	reversed := graph.Graph{
		Nodes: append([]graph.Node(nil), base.Nodes...),
		Edges: append([]graph.Edge(nil), base.Edges...),
	}
	for left, right := 0, len(reversed.Nodes)-1; left < right; left, right = left+1, right-1 {
		reversed.Nodes[left], reversed.Nodes[right] = reversed.Nodes[right], reversed.Nodes[left]
	}
	for left, right := 0, len(reversed.Edges)-1; left < right; left, right = left+1, right-1 {
		reversed.Edges[left], reversed.Edges[right] = reversed.Edges[right], reversed.Edges[left]
	}
	options := RetrieveOptions{
		Traversal: TraversalBFS, Direction: DirectionOut, DisableRelationInference: true,
		SeedLimit: 1, MaxDepth: 2, MaxNodes: 20, HubDegreeThreshold: -1, TokenBudget: 100_000,
	}

	want := mustRetrieve(t, NewIndex(base), "TraversalRoot", options)
	wantText := writeRetrievalText(t, want)
	for run := 0; run < 12; run++ {
		candidate := base
		if run%2 == 1 {
			candidate = reversed
		}
		got := mustRetrieve(t, NewIndex(candidate), "TraversalRoot", options)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d retrieval differs:\ngot  %#v\nwant %#v", run, got, want)
		}
		if gotText := writeRetrievalText(t, got); gotText != wantText {
			t.Fatalf("run %d text differs:\ngot:\n%s\nwant:\n%s", run, gotText, wantText)
		}
	}
}

func TestRetrieveCommunityBoostPrioritizesSameCommunityNeighbors(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "seed", Kind: graph.NodeFunction, Name: "Target", Meta: map[string]string{"community": "c-a"}},
			{ID: "z-inside", Kind: graph.NodeFunction, Name: "Worker", Meta: map[string]string{"community": "c-a"}},
			{ID: "a-outside", Kind: graph.NodeFunction, Name: "Worker", Meta: map[string]string{"community": "c-b"}},
		},
		Edges: []graph.Edge{{ID: "e1", Kind: graph.EdgeCalls, From: "seed", To: "z-inside"}, {ID: "e2", Kind: graph.EdgeCalls, From: "seed", To: "a-outside"}},
	}
	options := RetrieveOptions{Direction: DirectionOut, DisableRelationInference: true, SeedLimit: 1, MaxDepth: 1, MaxNodes: 3, TokenBudget: 2_000, BranchFanout: 2}
	plain, err := Retrieve(g, "Target", options)
	if err != nil {
		t.Fatal(err)
	}
	options.CommunityBoost = true
	boosted, err := Retrieve(g, "Target", options)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.Nodes) < 3 || len(boosted.Nodes) < 3 {
		t.Fatalf("unexpected retrievals: plain=%#v boosted=%#v", plain.Nodes, boosted.Nodes)
	}
	if plain.Nodes[1].ID != "a-outside" {
		t.Fatalf("plain second node = %q, want a-outside", plain.Nodes[1].ID)
	}
	if boosted.Nodes[1].ID != "z-inside" || !boosted.Stats.CommunityBoost {
		t.Fatalf("boosted result = %#v, stats=%#v", boosted.Nodes, boosted.Stats)
	}
}

func TestRetrieveBudgetPrioritizesDiscoveredCandidateOverLargeExplanationEdge(t *testing.T) {
	edge := testQueryEdge(graph.EdgeCalls, "root", "child")
	edge.Meta = map[string]string{"evidence": strings.Repeat("large edge evidence ", 80)}
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "root", Kind: graph.NodeFunction, Name: "BudgetRoot"},
			{ID: "child", Kind: graph.NodeFunction, Name: "Child"},
		},
		Edges: []graph.Edge{edge},
	}

	result := mustRetrieve(t, NewIndex(g), "BudgetRoot", RetrieveOptions{
		Traversal: TraversalBFS, Direction: DirectionOut, DisableRelationInference: true,
		SeedLimit: 1, MaxDepth: 1, MaxNodes: 10, HubDegreeThreshold: -1, TokenBudget: 128,
	})
	if got, want := retrievalNodeIDs(result), []string{"root", "child"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("budgeted nodes = %v, want candidate retained ahead of evidence: %v", got, want)
	}
	if len(result.Edges) != 0 {
		t.Fatalf("budgeted edges = %#v, want oversized explanation omitted", result.Edges)
	}
	if result.Stats.Truncated || result.Stats.OmittedNodes != 0 || result.Stats.ExplanationEdgesOmitted != 1 {
		t.Fatalf("budget stats = %#v, want only one intentionally omitted explanation", result.Stats)
	}
	for _, node := range result.Nodes {
		if node.ViaEdgeID != "" {
			t.Fatalf("candidate %q retained a dangling discovery edge: %#v", node.ID, result)
		}
	}
}

func TestRetrievePrioritizesEvidenceConnectingTopCandidates(t *testing.T) {
	nodes := []graph.Node{
		{ID: "caller", Kind: graph.NodeFunction, Name: "DirectCaller"},
		{ID: "callee", Kind: graph.NodeFunction, Name: "DirectCallee"},
	}
	edges := []graph.Edge{testQueryEdge(graph.EdgeCalls, "caller", "callee")}
	for index := 0; index < maximumExplanationEdges; index++ {
		id := fmt.Sprintf("noise-%02d", index)
		nodes = append(nodes, graph.Node{ID: id, Kind: graph.NodeFunction, Name: fmt.Sprintf("NoiseCandidate%02d", index)})
		edges = append(edges, testQueryEdge(graph.EdgeCalls, "caller", id))
	}

	result := mustRetrieve(t, NewIndex(graph.Graph{Nodes: nodes, Edges: edges}), "DirectCaller DirectCallee", RetrieveOptions{
		Direction: DirectionOut, DisableRelationInference: true, SeedLimit: 2,
		MaxDepth: 1, MaxNodes: 20, BranchFanout: 20, HubDegreeThreshold: -1, TokenBudget: 100_000,
	})
	direct := graph.EdgeID(graph.EdgeCalls, "caller", "callee")
	if !retrievalHasEdge(result, direct) {
		t.Fatalf("top candidates lost their direct evidence edge: nodes=%v edges=%#v", retrievalNodeIDs(result), result.Edges)
	}
	if len(result.Edges) != maximumExplanationEdges || result.Stats.ExplanationEdgesOmitted != 1 {
		t.Fatalf("explanation cap stats = %#v, edges = %#v", result.Stats, result.Edges)
	}
}

func TestRetrieveDistinguishesRankedShortlistSelectionFromTokenTruncation(t *testing.T) {
	nodes := []graph.Node{{ID: "root", Kind: graph.NodeFunction, Name: "ShortlistRoot"}}
	edges := make([]graph.Edge, 0, 20)
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("child-%02d", i)
		nodes = append(nodes, graph.Node{
			ID: id, Kind: graph.NodeFunction, Name: fmt.Sprintf("Candidate%02d", i),
			Path: fmt.Sprintf("internal/shortlist/candidate_%02d_with_a_descriptive_path.go", i),
		})
		edges = append(edges, testQueryEdge(graph.EdgeCalls, "root", id))
	}
	g := graph.Graph{Nodes: nodes, Edges: edges}
	options := RetrieveOptions{
		Direction: DirectionOut, DisableRelationInference: true, SeedLimit: 1,
		MaxDepth: 1, MaxNodes: 100, BranchFanout: 100, HubDegreeThreshold: -1, TokenBudget: 256,
		CandidateShortlist: true,
	}
	result := mustRetrieve(t, NewIndex(g), "ShortlistRoot", options)
	if result.Stats.UnselectedNodes == 0 {
		t.Fatalf("shortlist selected every traversal alternative: %#v", result.Stats)
	}
	if result.Stats.Truncated || result.Stats.OmittedNodes != 0 || containsString(result.Stats.TruncatedReason, "token_budget") {
		t.Fatalf("ranked selection was misreported as hard truncation: %#v", result.Stats)
	}
	accounted := result.Stats.HeaderTokens + result.Stats.CandidateTokens + result.Stats.ExplanationTokens
	if accounted != result.Stats.EstimatedTokens {
		t.Fatalf("token accounting = %d, estimated = %d: %#v", accounted, result.Stats.EstimatedTokens, result.Stats)
	}
	if got := estimateTokens(writeRetrievalText(t, result)); got > result.Stats.TokenBudget {
		t.Fatalf("rendered output estimate = %d, budget = %d", got, result.Stats.TokenBudget)
	}

	options.CandidateShortlist = false
	balanced := mustRetrieve(t, NewIndex(g), "ShortlistRoot", options)
	if balanced.Stats.UnselectedNodes != 0 || !balanced.Stats.Truncated || !containsString(balanced.Stats.TruncatedReason, "token_budget") {
		t.Fatalf("default balanced packing no longer uses legacy truncation accounting: %#v", balanced.Stats)
	}
}

func TestRetrieveCandidateShortlistRefillsUnusedExplanationBudget(t *testing.T) {
	nodes := make([]graph.Node, 40)
	walk := traversalResult{
		order:       make([]string, len(nodes)),
		distance:    map[string]int{},
		via:         map[string]graph.Edge{},
		lexicalOnly: map[string]bool{},
	}
	scores := map[string]int{}
	seedSet := map[string]bool{}
	for position := range nodes {
		id := fmt.Sprintf("node-%02d", position)
		nodes[position] = graph.Node{ID: id, Kind: graph.NodeFunction, Name: fmt.Sprintf("Candidate%02d", position)}
		walk.order[position] = id
		walk.distance[id] = 0
		walk.lexicalOnly[id] = true
		scores[id] = len(nodes) - position
	}
	idx := NewIndex(graph.Graph{Nodes: nodes})
	options := normalizedRetrieveOptions{RetrieveOptions: RetrieveOptions{
		Traversal: TraversalBFS, Direction: DirectionBoth, MaxNodes: 100,
		TokenBudget: 512, CandidateShortlist: true,
	}}
	result := idx.fitRetrieval("candidate refill", options, nil, seedSet, scores, idx.bothDegree, walk, nil)
	reserve := lineTokens(truncationLine(RetrievalStats{
		TruncatedReason: []string{"token_budget", "max_nodes", "exploration_limit", "branch_limit"},
		OmittedNodes:    50_000, OmittedEdges: 999_999_999,
	}))
	primaryLimit := result.Stats.HeaderTokens +
		(options.TokenBudget-reserve-result.Stats.HeaderTokens)*candidateBudgetPercent/100
	if result.Stats.EstimatedTokens <= primaryLimit {
		t.Fatalf("shortlist used %d tokens, want refill beyond primary limit %d", result.Stats.EstimatedTokens, primaryLimit)
	}
	if result.Stats.EstimatedTokens > options.TokenBudget-reserve {
		t.Fatalf("shortlist used %d tokens beyond hard candidate limit %d", result.Stats.EstimatedTokens, options.TokenBudget-reserve)
	}
	if result.Stats.UnselectedNodes == 0 {
		t.Fatalf("fixture did not leave deferred candidates: %#v", result.Stats)
	}
}

func TestRetrieveCandidateShortlistKeepsTopLexicalMatchThatIsNotASeed(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{
		{ID: "seed", Kind: graph.NodeFunction, Name: "AssembleProviderInstanceRegistryLayerFixedSettingsWatcherControl"},
		{ID: "gold", Kind: graph.NodeFunction, Name: "ProviderInstanceRegistryLayer"},
		{ID: "other", Kind: graph.NodeFunction, Name: "SettingsWatcherLive"},
	}}
	idx := NewIndex(g)
	question := "assemble a ProviderInstanceRegistry layer with fixed settings watcher control"
	ranked := idx.rank(question)
	if len(ranked) < 2 || ranked[1].index != idx.byID["gold"] {
		t.Fatalf("fixture lexical order = %#v, want gold second", ranked)
	}
	result := mustRetrieve(t, idx, question, RetrieveOptions{
		SeedLimit: 1, MaxNodes: 10, TokenBudget: 1_000, CandidateShortlist: true,
	})
	if !containsString(retrievalNodeIDs(result), "gold") || result.Nodes[1].ID != "gold" {
		t.Fatalf("shortlist nodes = %#v, want non-seed lexical runner-up retained", result.Nodes)
	}
}

func TestEligibleShortlistCandidateExcludesImportAndUnresolvedNoise(t *testing.T) {
	for _, tc := range []struct {
		name string
		node graph.Node
		want bool
	}{
		{name: "declaration", node: graph.Node{Kind: graph.NodeFunction}, want: true},
		{name: "import", node: graph.Node{Kind: graph.NodeImport}, want: false},
		{name: "unresolved call", node: graph.Node{Kind: graph.NodeFunction, Meta: map[string]string{"resolved": "false"}}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := eligibleShortlistCandidate(tc.node); got != tc.want {
				t.Fatalf("eligibleShortlistCandidate() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRetrieveReportsTokenTruncationWhenTopCandidateCannotFit(t *testing.T) {
	long := strings.Repeat("very-long-path-segment/", 40)
	g := graph.Graph{Nodes: []graph.Node{{
		ID: strings.Repeat("candidate-id-", 40), Kind: graph.NodeFunction,
		Name: strings.Repeat("OversizedCandidate", 30), Path: long + "candidate.go",
	}}}
	result := mustRetrieve(t, NewIndex(g), "OversizedCandidate", RetrieveOptions{TokenBudget: 128, CandidateShortlist: true})
	if !result.Stats.Truncated || !containsString(result.Stats.TruncatedReason, "token_budget") || result.Stats.OmittedNodes != 1 {
		t.Fatalf("oversized admitted candidate was not reported as token truncation: %#v", result.Stats)
	}
	if result.Stats.UnselectedNodes != 0 || len(result.Nodes) != 0 {
		t.Fatalf("oversized candidate accounting = %#v, nodes = %#v", result.Stats, result.Nodes)
	}
}

func TestRetrieveNeverEmitsEdgesWhoseSeedsWereOmitted(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "first", Kind: graph.NodeConcept, Name: "FirstTopic"},
			{ID: "second", Kind: graph.NodeConcept, Name: "SecondTopic"},
		},
		Edges: []graph.Edge{testQueryEdge(graph.EdgeReferences, "first", "second")},
	}
	result := mustRetrieve(t, NewIndex(g), "first topic second topic", RetrieveOptions{
		Direction: DirectionBoth, DisableRelationInference: true, SeedLimit: 2,
		MaxDepth: 1, MaxNodes: 1, HubDegreeThreshold: -1, TokenBudget: 100_000,
	})
	if len(result.Nodes) != 1 || len(result.Edges) != 0 {
		t.Fatalf("retrieval has dangling seed edge: %#v", result)
	}
}

func TestRetrieveLongQuestionStaysWithinMinimumBudgetAndReportsTruncation(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{{ID: "root", Kind: graph.NodeFunction, Name: "LongQueryRoot"}}}
	question := strings.Repeat("verylongsegment", 100) + " LongQueryRoot"
	result := mustRetrieve(t, NewIndex(g), question, RetrieveOptions{TokenBudget: 128})
	if result.Stats.EstimatedTokens > result.Stats.TokenBudget {
		t.Fatalf("estimated tokens = %d, budget = %d", result.Stats.EstimatedTokens, result.Stats.TokenBudget)
	}
	text := writeRetrievalText(t, result)
	if estimateTokens(text) > result.Stats.TokenBudget {
		t.Fatalf("rendered output estimate = %d, budget = %d\n%s", estimateTokens(text), result.Stats.TokenBudget, text)
	}
	if len(result.Nodes) == 0 && !strings.Contains(text, "TRUNCATED") {
		t.Fatalf("matched-but-omitted result hid truncation: %s", text)
	}
}

func TestWriteRetrievalEscapesControlCharactersInIdentifiers(t *testing.T) {
	maliciousID := "node\nEDGE\tforged"
	g := graph.Graph{Nodes: []graph.Node{{ID: maliciousID, Kind: graph.NodeFunction, Name: "SafeTarget"}}}
	result := mustRetrieve(t, NewIndex(g), "SafeTarget", RetrieveOptions{TokenBudget: 1_000})
	text := writeRetrievalText(t, result)
	if strings.Contains(text, maliciousID) || strings.Contains(text, "\nEDGE\tforged\t") {
		t.Fatalf("raw control-bearing identifier escaped the compact record: %q", text)
	}
	if !strings.Contains(text, `node\nEDGE\tforged`) {
		t.Fatalf("escaped identifier missing from compact output: %q", text)
	}
}

func TestRetrieveInfersCommonRelationInflections(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{{ID: "root", Kind: graph.NodeFunction, Name: "InflectionRoot"}, {ID: "caller", Kind: graph.NodeFunction, Name: "Caller"}, {ID: "import", Kind: graph.NodeImport, Name: "Import"}},
		Edges: []graph.Edge{testQueryEdge(graph.EdgeCalls, "caller", "root"), testQueryEdge(graph.EdgeImports, "root", "import")},
	}
	called := mustRetrieve(t, NewIndex(g), "what is called by InflectionRoot", RetrieveOptions{Direction: DirectionBoth, SeedLimit: 1, MaxDepth: 1, TokenBudget: 100_000})
	if got, want := called.Stats.RelationFilters, []graph.EdgeKind{graph.EdgeCalls}; !reflect.DeepEqual(got, want) {
		t.Fatalf("called inference = %v, want %v", got, want)
	}
	imported := mustRetrieve(t, NewIndex(g), "what is imported by InflectionRoot", RetrieveOptions{Direction: DirectionBoth, SeedLimit: 1, MaxDepth: 1, TokenBudget: 100_000})
	if got, want := imported.Stats.RelationFilters, []graph.EdgeKind{graph.EdgeImports}; !reflect.DeepEqual(got, want) {
		t.Fatalf("imported inference = %v, want %v", got, want)
	}
}

func TestRetrieveRejectsInvalidOptions(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "root", Kind: graph.NodeFunction, Name: "OptionRoot"},
			{ID: "child", Kind: graph.NodeFunction, Name: "Child"},
		},
		Edges: []graph.Edge{testQueryEdge(graph.EdgeCalls, "root", "child")},
	}
	idx := NewIndex(g)

	if _, err := idx.Retrieve(" \n\t ", RetrieveOptions{}); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("empty query error = %v, want non-empty validation", err)
	}

	for _, tc := range []struct {
		name    string
		options RetrieveOptions
		message string
	}{
		{name: "traversal", options: RetrieveOptions{Traversal: "walk"}, message: "unsupported traversal"},
		{name: "direction", options: RetrieveOptions{Direction: "sideways"}, message: "unsupported direction"},
		{name: "seed too small", options: RetrieveOptions{SeedLimit: -1}, message: "seed limit"},
		{name: "seed too large", options: RetrieveOptions{SeedLimit: 21}, message: "seed limit"},
		{name: "depth too small", options: RetrieveOptions{MaxDepth: -1}, message: "max depth"},
		{name: "depth too large", options: RetrieveOptions{MaxDepth: 9}, message: "max depth"},
		{name: "nodes too small", options: RetrieveOptions{MaxNodes: -1}, message: "max nodes"},
		{name: "nodes too large", options: RetrieveOptions{MaxNodes: 10_001}, message: "max nodes"},
		{name: "branch fanout too small", options: RetrieveOptions{BranchFanout: -1}, message: "branch fanout"},
		{name: "branch fanout too large", options: RetrieveOptions{BranchFanout: 10_001}, message: "branch fanout"},
		{name: "hub threshold", options: RetrieveOptions{HubDegreeThreshold: -2}, message: "hub degree threshold"},
		{name: "budget too small", options: RetrieveOptions{TokenBudget: 127}, message: "token budget"},
		{name: "budget too large", options: RetrieveOptions{TokenBudget: 100_001}, message: "token budget"},
		{name: "unknown relation", options: RetrieveOptions{Relations: []graph.EdgeKind{"not_a_relation"}}, message: "not present in this graph"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := idx.Retrieve("OptionRoot", tc.options)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("Retrieve() error = %v, want substring %q", err, tc.message)
			}
		})
	}
}

func TestRetrieveAppliesDocumentedDefaults(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{{ID: "root", Kind: graph.NodeFunction, Name: "DefaultRoot"}}}
	result := mustRetrieve(t, NewIndex(g), "DefaultRoot", RetrieveOptions{})
	if result.Stats.Traversal != TraversalBFS || result.Stats.Direction != DirectionBoth ||
		result.Stats.Depth != defaultMaxDepth || result.Stats.TokenBudget != defaultTokenBudget ||
		result.Stats.BranchFanout != traversalFanout(defaultMaxNodes, 1, defaultMaxDepth, 0) {
		t.Fatalf("default stats = %#v", result.Stats)
	}
	if got, want := result.Stats.SeedIDs, []string{"root"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("default seeds = %v, want %v", got, want)
	}
}

func TestRetrieveGraphWrapperMatchesReusableIndex(t *testing.T) {
	g := traversalFixture()
	options := RetrieveOptions{
		Traversal: TraversalBFS, Direction: DirectionOut, DisableRelationInference: true,
		SeedLimit: 1, MaxDepth: 2, MaxNodes: 20, HubDegreeThreshold: -1, TokenBudget: 100_000,
	}
	want := mustRetrieve(t, NewIndex(g), "TraversalRoot", options)
	got, err := Retrieve(g, "TraversalRoot", options)
	if err != nil {
		t.Fatalf("Retrieve() wrapper error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Retrieve() wrapper = %#v, want Index.Retrieve() %#v", got, want)
	}
}

func TestReusableIndexKeepsCompoundNameTieOrderDeterministic(t *testing.T) {
	path := "src/provider/Registry.ts"
	g := graph.Graph{Nodes: []graph.Node{
		{ID: "call://registry#120", Kind: graph.NodeFunction, Name: "Snapshot Instance Routing Persistence Key", Path: path, StartLine: 120},
		{ID: "call://registry#125", Kind: graph.NodeFunction, Name: "Snapshot Instance Routing Persistence Key", Path: path, StartLine: 125},
		{ID: "call://registry#297", Kind: graph.NodeFunction, Name: "Snapshot Instance Routing Persistence Key", Path: path, StartLine: 297},
	}}
	idx := NewIndex(g)
	options := RetrieveOptions{
		Traversal: TraversalBFS, Direction: DirectionBoth, DisableRelationInference: true,
		SeedLimit: 20, MaxDepth: 3, MaxNodes: 10_000, BranchFanout: 10_000,
		TokenBudget: 2_000, CandidateShortlist: true,
	}
	question := "find the snapshot instance routing persistence key"
	want := mustRetrieve(t, idx, question, options)
	if len(want.Nodes) != 1 || want.Nodes[0].ID != "call://registry#120" {
		t.Fatalf("first retrieval nodes = %#v, want lexicographically first duplicate", want.Nodes)
	}
	for run := 0; run < 100; run++ {
		got := mustRetrieve(t, idx, question, options)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d changed reusable retrieval\ngot:  %#v\nwant: %#v", run, got, want)
		}
	}
}

func TestRetrieveTraceReportsNodeFunnelWithoutChangingResults(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "root", Kind: graph.NodeFunction, Name: "CheckoutRoot"},
			{ID: "target", Kind: graph.NodeFunction, Name: "ChargeCard", Path: "payments.go", StartLine: 20},
		},
		Edges: []graph.Edge{testQueryEdge(graph.EdgeCalls, "root", "target")},
	}
	options := RetrieveOptions{
		Direction: DirectionOut, Relations: []graph.EdgeKind{graph.EdgeCalls},
		SeedLimit: 1, MaxDepth: 1, MaxNodes: 10, HubDegreeThreshold: -1, TokenBudget: 1_000,
	}
	idx := NewIndex(g)
	plain := mustRetrieve(t, idx, "CheckoutRoot", options)
	options.TraceNodeIDs = []string{"target", "missing", "target"}
	traced := mustRetrieve(t, idx, "CheckoutRoot", options)
	if !reflect.DeepEqual(plain.Nodes, traced.Nodes) || !reflect.DeepEqual(plain.Edges, traced.Edges) {
		t.Fatalf("trace changed retrieval\nplain:  %#v\ntraced: %#v", plain, traced)
	}
	if len(traced.Stats.TraceNodes) != 2 {
		t.Fatalf("trace nodes = %#v, want deduplicated target and missing", traced.Stats.TraceNodes)
	}
	target := traced.Stats.TraceNodes[0]
	if !target.Indexed || !target.Traversed || target.WalkRank == 0 || target.CandidateRank == 0 || target.ReturnedRank == 0 || target.DroppedReason != "" {
		t.Fatalf("target trace = %#v", target)
	}
	missing := traced.Stats.TraceNodes[1]
	if missing.Indexed || missing.DroppedReason != "not_indexed" {
		t.Fatalf("missing trace = %#v", missing)
	}
}

func TestRetrieveTraceDiagnosesTraversalExclusions(t *testing.T) {
	tests := []struct {
		name      string
		graph     graph.Graph
		options   RetrieveOptions
		target    string
		exclusion string
	}{
		{
			name: "relation filter",
			graph: graph.Graph{
				Nodes: []graph.Node{
					{ID: "root", Kind: graph.NodeFunction, Name: "TraceRoot"},
					{ID: "other", Kind: graph.NodeFunction, Name: "Other"},
					{ID: "target", Kind: graph.NodeFunction, Name: "FilteredTarget"},
				},
				Edges: []graph.Edge{
					testQueryEdge(graph.EdgeCalls, "root", "other"),
					testQueryEdge(graph.EdgeImports, "root", "target"),
				},
			},
			options: RetrieveOptions{Relations: []graph.EdgeKind{graph.EdgeCalls}, MaxDepth: 1, HubDegreeThreshold: -1},
			target:  "target", exclusion: "relation_filter",
		},
		{
			name: "depth limit",
			graph: graph.Graph{
				Nodes: []graph.Node{
					{ID: "root", Kind: graph.NodeFunction, Name: "TraceRoot"},
					{ID: "middle", Kind: graph.NodeFunction, Name: "Middle"},
					{ID: "target", Kind: graph.NodeFunction, Name: "DeepTarget"},
				},
				Edges: []graph.Edge{
					testQueryEdge(graph.EdgeCalls, "root", "middle"),
					testQueryEdge(graph.EdgeCalls, "middle", "target"),
				},
			},
			options: RetrieveOptions{Relations: []graph.EdgeKind{graph.EdgeCalls}, MaxDepth: 1, HubDegreeThreshold: -1},
			target:  "target", exclusion: "depth_limit",
		},
		{
			name: "hub suppression",
			graph: graph.Graph{
				Nodes: []graph.Node{
					{ID: "root", Kind: graph.NodeFunction, Name: "TraceRoot"},
					{ID: "middle", Kind: graph.NodeFunction, Name: "Middle"},
					{ID: "target", Kind: graph.NodeFunction, Name: "HubTarget"},
				},
				Edges: []graph.Edge{
					testQueryEdge(graph.EdgeCalls, "root", "middle"),
					testQueryEdge(graph.EdgeCalls, "middle", "target"),
				},
			},
			options: RetrieveOptions{Relations: []graph.EdgeKind{graph.EdgeCalls}, MaxDepth: 2, HubDegreeThreshold: 1},
			target:  "target", exclusion: "hub_suppressed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.options.Direction = DirectionOut
			test.options.SeedLimit = 1
			test.options.MaxNodes = 10
			test.options.TokenBudget = 1_000
			test.options.TraceNodeIDs = []string{test.target}
			got := mustRetrieve(t, NewIndex(test.graph), "TraceRoot", test.options)
			if len(got.Stats.TraceNodes) != 1 || got.Stats.TraceNodes[0].TraversalExclusion != test.exclusion {
				t.Fatalf("trace = %#v, want traversal exclusion %q", got.Stats.TraceNodes, test.exclusion)
			}
		})
	}
}

func TestRetrievalTraceDiagnosesLexicalPromotionCutoff(t *testing.T) {
	nodes := make([]graph.Node, maximumLexicalCandidates+1)
	ranked := make([]rankedNode, len(nodes))
	for position := range nodes {
		nodes[position] = graph.Node{ID: fmt.Sprintf("node-%03d", position), Kind: graph.NodeFunction, Name: fmt.Sprintf("Node%d", position)}
		ranked[position] = rankedNode{index: position, anchored: true}
	}
	idx := NewIndex(graph.Graph{Nodes: nodes})
	target := nodes[len(nodes)-1].ID
	traces := idx.newRetrievalTraces([]string{target}, ranked, nil, traversalResult{}, true)
	if len(traces) != 1 || traces[0].PromotionRank != maximumLexicalCandidates+1 || traces[0].PromotionExclusion != "lexical_cutoff" {
		t.Fatalf("trace = %#v", traces)
	}
}

func TestSelectSameFileCandidatesPromotesOneBoundedSiblingPerFile(t *testing.T) {
	nodes := make([]graph.Node, maximumLexicalCandidates+4)
	ranked := make([]rankedNode, len(nodes))
	for position := range nodes {
		nodes[position] = graph.Node{
			ID: fmt.Sprintf("node-%03d", position), Kind: graph.NodeFunction,
			Name: fmt.Sprintf("Node%d", position), Path: fmt.Sprintf("file-%03d.go", position),
		}
		ranked[position] = rankedNode{index: position, score: float64(len(nodes) - position)}
	}
	targetPosition := maximumLexicalCandidates - 1
	for position := 0; position < sameFileMinimumAnchors; position++ {
		nodes[position].Path = "shared.go"
	}
	nodes[targetPosition].Path = "shared.go"
	nodes[targetPosition+1].Path = "shared.go"
	idx := NewIndex(graph.Graph{Nodes: nodes})

	rescues := idx.selectSameFileCandidates(ranked, []string{nodes[0].ID}, []string{nodes[0].ID}, sameFileMinimumAnchors, 2)
	if len(rescues) != 1 {
		t.Fatalf("rescues = %#v, want one rescue for shared.go", rescues)
	}
	want := SameFileRescue{
		ID: nodes[targetPosition].ID, AnchorPath: "shared.go", AnchorCount: sameFileMinimumAnchors,
		OriginalRank: targetPosition + 1, StructuralSlot: shortlistSameFileSlot,
	}
	if rescues[0] != want {
		t.Fatalf("rescue = %#v, want %#v", rescues[0], want)
	}

	walk := traversalResult{
		order: []string{nodes[0].ID}, distance: map[string]int{nodes[0].ID: 0},
		lexicalOnly: map[string]bool{}, sameFileRescued: map[string]bool{want.ID: true},
		sameFileRescues: rescues,
	}
	walk = idx.promoteLexicalCandidates(walk, ranked, true)
	if !walk.lexicalOnly[want.ID] {
		t.Fatalf("same-file rescue %q lost its lexical origin", want.ID)
	}
	traces := idx.newRetrievalTraces([]string{want.ID}, ranked, nil, walk, true)
	if len(traces) != 1 || !traces[0].SameFileRescued || traces[0].OriginalLexicalRank != want.OriginalRank || traces[0].SameFileAnchorPath != want.AnchorPath {
		t.Fatalf("trace = %#v, want same-file rescue telemetry", traces)
	}
}

func TestSelectSameFileCandidatesRejectsCandidatesOutsideWindow(t *testing.T) {
	nodes := make([]graph.Node, sameFileCandidateWindow+1)
	ranked := make([]rankedNode, len(nodes))
	for position := range nodes {
		nodes[position] = graph.Node{
			ID: fmt.Sprintf("node-%04d", position), Kind: graph.NodeFunction,
			Name: fmt.Sprintf("Node%d", position), Path: fmt.Sprintf("file-%04d.go", position),
		}
		ranked[position] = rankedNode{index: position, score: float64(len(nodes) - position)}
	}
	for position := 0; position < sameFileMinimumAnchors; position++ {
		nodes[position].Path = "shared.go"
	}
	nodes[sameFileCandidateWindow].Path = "shared.go"
	idx := NewIndex(graph.Graph{Nodes: nodes})
	rescues := idx.selectSameFileCandidates(ranked, []string{nodes[0].ID}, nil, sameFileMinimumAnchors, 2)
	if len(rescues) != 0 {
		t.Fatalf("outside-window candidate was rescued: rescues=%#v", rescues)
	}
}

func TestSelectSameFileCandidatesRequiresMinimumAnchors(t *testing.T) {
	nodes := make([]graph.Node, sameFileMinimumAnchors+1)
	ranked := make([]rankedNode, len(nodes))
	for position := range nodes {
		nodes[position] = graph.Node{
			ID: fmt.Sprintf("node-%02d", position), Kind: graph.NodeFunction,
			Name: fmt.Sprintf("Node%d", position), Path: "shared.go",
		}
		ranked[position] = rankedNode{index: position, score: float64(len(nodes) - position)}
	}
	idx := NewIndex(graph.Graph{Nodes: nodes})
	rescues := idx.selectSameFileCandidates(ranked, []string{nodes[0].ID}, nil, sameFileMinimumAnchors-1, 1)
	if len(rescues) != 0 {
		t.Fatalf("candidate with only %d anchors was rescued: %#v", sameFileMinimumAnchors-1, rescues)
	}
}

func TestRerankAffinityCandidatesRescuesConnectedBelowCutoffCandidate(t *testing.T) {
	nodes := make([]graph.Node, maximumLexicalCandidates+4)
	ranked := make([]rankedNode, len(nodes))
	for position := range nodes {
		nodes[position] = graph.Node{
			ID: fmt.Sprintf("node-%03d", position), Kind: graph.NodeFunction,
			Name: fmt.Sprintf("Node%d", position),
		}
		ranked[position] = rankedNode{index: position, score: float64(len(nodes) - position)}
	}
	g := graph.Graph{Nodes: nodes, Edges: []graph.Edge{
		testQueryEdge(graph.EdgeCalls, nodes[0].ID, nodes[maximumLexicalCandidates].ID),
	}}
	idx := NewIndex(g)
	options := normalizedRetrieveOptions{RetrieveOptions: RetrieveOptions{Direction: DirectionBoth}}
	got, rescues := idx.rerankAffinityCandidates(ranked, []string{nodes[0].ID}, idx.newQueryAdjacency(options, nil), 2, 2)
	if got[0].index != 0 || got[1].index != 1 || got[2].index != maximumLexicalCandidates {
		t.Fatalf("leading reranked indexes = [%d %d %d], want [0 1 %d]", got[0].index, got[1].index, got[2].index, maximumLexicalCandidates)
	}
	if len(got) != len(ranked) {
		t.Fatalf("reranked length = %d, want %d", len(got), len(ranked))
	}
	if len(rescues) != 1 || rescues[0].ID != nodes[maximumLexicalCandidates].ID || rescues[0].OriginalRank != maximumLexicalCandidates+1 || rescues[0].RerankedRank != 3 {
		t.Fatalf("rescues = %#v", rescues)
	}
}

func TestRerankAffinityCandidatesRejectsCandidatesOutsideConfidenceWindow(t *testing.T) {
	nodes := make([]graph.Node, affinityRerankWindow+1)
	ranked := make([]rankedNode, len(nodes))
	for position := range nodes {
		nodes[position] = graph.Node{ID: fmt.Sprintf("node-%03d", position), Kind: graph.NodeFunction, Name: fmt.Sprintf("Node%d", position)}
		ranked[position] = rankedNode{index: position, score: float64(len(nodes) - position)}
	}
	g := graph.Graph{Nodes: nodes, Edges: []graph.Edge{
		testQueryEdge(graph.EdgeCalls, nodes[0].ID, nodes[affinityRerankWindow].ID),
	}}
	idx := NewIndex(g)
	options := normalizedRetrieveOptions{RetrieveOptions: RetrieveOptions{Direction: DirectionBoth}}
	got, rescues := idx.rerankAffinityCandidates(ranked, []string{nodes[0].ID}, idx.newQueryAdjacency(options, nil), 2, 2)
	if len(rescues) != 0 || got[affinityRerankWindow].index != affinityRerankWindow {
		t.Fatalf("outside-window candidate was rescued: rescues=%#v rank=%d", rescues, got[affinityRerankWindow].index)
	}
}

func TestPrioritizeGraphCandidatesReservesStructuralSlots(t *testing.T) {
	nodes := make([]ContextNode, 7)
	owners := make([]string, 7)
	lexicalOnly := map[string]bool{}
	for index := range nodes {
		owners[index] = fmt.Sprintf("node-%d", index)
		nodes[index] = ContextNode{ID: owners[index]}
		lexicalOnly[owners[index]] = true
	}
	delete(lexicalOnly, "node-4")
	delete(lexicalOnly, "node-6")
	gotNodes, gotOwners := prioritizeGraphCandidates(nodes, owners, lexicalOnly, nil, 2, 2, shortlistSameFileSlot)
	wantOwners := []string{"node-0", "node-1", "node-4", "node-6", "node-2", "node-3", "node-5"}
	if !reflect.DeepEqual(gotOwners, wantOwners) {
		t.Fatalf("owners = %v, want %v", gotOwners, wantOwners)
	}
	for position, node := range gotNodes {
		if node.ID != gotOwners[position] {
			t.Fatalf("node/owner mismatch at %d: %#v != %q", position, node, gotOwners[position])
		}
	}
}

func TestPrioritizeGraphCandidatesPlacesSameFileRescueAtConfiguredSlot(t *testing.T) {
	nodes := make([]ContextNode, 7)
	owners := make([]string, 7)
	lexicalOnly := map[string]bool{}
	for index := range nodes {
		owners[index] = fmt.Sprintf("node-%d", index)
		nodes[index] = ContextNode{ID: owners[index]}
		lexicalOnly[owners[index]] = true
	}
	delete(lexicalOnly, "node-4")
	delete(lexicalOnly, "node-6")
	sameFileRescued := map[string]bool{"node-2": true}
	_, gotOwners := prioritizeGraphCandidates(nodes, owners, lexicalOnly, sameFileRescued, 2, 3, 3)
	wantOwners := []string{"node-0", "node-1", "node-4", "node-6", "node-2", "node-3", "node-5"}
	if !reflect.DeepEqual(gotOwners, wantOwners) {
		t.Fatalf("owners = %v, want same-file rescue after structural slots %v", gotOwners, wantOwners)
	}
}

func traversalFixture() graph.Graph {
	return graph.Graph{
		Nodes: []graph.Node{
			{ID: "b1", Kind: graph.NodeFunction, Name: "BranchBLeaf"},
			{ID: "a", Kind: graph.NodeFunction, Name: "BranchA"},
			{ID: "root", Kind: graph.NodeFunction, Name: "TraversalRoot"},
			{ID: "b", Kind: graph.NodeFunction, Name: "BranchB"},
			{ID: "a1", Kind: graph.NodeFunction, Name: "BranchALeaf"},
		},
		Edges: []graph.Edge{
			testQueryEdge(graph.EdgeCalls, "b", "b1"),
			testQueryEdge(graph.EdgeCalls, "root", "b"),
			testQueryEdge(graph.EdgeCalls, "a", "a1"),
			testQueryEdge(graph.EdgeCalls, "root", "a"),
		},
	}
}

func testQueryEdge(kind graph.EdgeKind, from, to string) graph.Edge {
	return graph.Edge{ID: graph.EdgeID(kind, from, to), Kind: kind, From: from, To: to}
}

func mustRetrieve(t *testing.T, idx *Index, text string, options RetrieveOptions) Retrieval {
	t.Helper()
	result, err := idx.Retrieve(text, options)
	if err != nil {
		t.Fatalf("Retrieve(%q) error = %v", text, err)
	}
	return result
}

func retrievalNodeIDs(result Retrieval) []string {
	ids := make([]string, len(result.Nodes))
	for i, node := range result.Nodes {
		ids[i] = node.ID
	}
	return ids
}

func retrievalNode(result Retrieval, id string) (ContextNode, bool) {
	for _, node := range result.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return ContextNode{}, false
}

func retrievalHasEdge(result Retrieval, id string) bool {
	for _, edge := range result.Edges {
		if edge.ID == id {
			return true
		}
	}
	return false
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writeRetrievalText(t *testing.T, result Retrieval) string {
	t.Helper()
	var output bytes.Buffer
	if err := WriteRetrieval(&output, result, false); err != nil {
		t.Fatalf("WriteRetrieval() error = %v", err)
	}
	return output.String()
}
