package query

import (
	"reflect"
	"testing"

	"github.com/12vault/ravel/internal/graph"
)

func TestIndexSearchRareTermOutranksCommonTerm(t *testing.T) {
	nodes := []graph.Node{{ID: "node://rare", Kind: graph.NodeConcept, Name: "Rare"}}
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		nodes = append(nodes, graph.Node{ID: "node://common-" + id, Kind: graph.NodeConcept, Name: "Common"})
	}

	results := NewIndex(graph.Graph{Nodes: nodes}).Search("common rare", 0)
	if len(results) != len(nodes) {
		t.Fatalf("Search() returned %d results, want %d: %#v", len(results), len(nodes), results)
	}
	if results[0].Node.ID != "node://rare" {
		t.Fatalf("Search() top result = %q, want rare term node; results = %#v", results[0].Node.ID, results)
	}
	if results[0].Score <= results[1].Score {
		t.Fatalf("rare score = %d, common score = %d; want rare term weighted more heavily", results[0].Score, results[1].Score)
	}
}

func TestIndexFindBestResolvesExactIDPathAndNameBeforeRanking(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{
		{ID: "go://auth.SessionManagerFactory", Kind: graph.NodeFunction, Name: "SessionManagerFactory", Path: "internal/auth/factory.go"},
		{ID: "go://auth.SessionManager", Kind: graph.NodeStruct, Name: "SessionManager", Path: "internal/auth/session_manager.go"},
	}}
	idx := NewIndex(g)

	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{name: "id", query: "go://auth.SessionManager", want: "go://auth.SessionManager"},
		{name: "path", query: "internal/auth/session_manager.go", want: "go://auth.SessionManager"},
		{name: "name", query: "SessionManager", want: "go://auth.SessionManager"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := idx.FindBest(tc.query)
			if !ok || got.ID != tc.want {
				t.Fatalf("FindBest(%q) = (%q, %v), want (%q, true)", tc.query, got.ID, ok, tc.want)
			}
		})
	}

	results := idx.Search("session manager", 1)
	if len(results) != 1 || results[0].Node.ID != "go://auth.SessionManager" {
		t.Fatalf("Search(session manager) = %#v, want exact normalized name", results)
	}
}

func TestIndexSearchNormalizesCamelCaseUnicodeAndCJK(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		nodes []graph.Node
		want  string
	}{
		{
			name:  "camel case acronym",
			query: "http server",
			nodes: []graph.Node{
				{ID: "go://net.HTTPClient", Kind: graph.NodeStruct, Name: "HTTPClient"},
				{ID: "go://net.HTTPServer", Kind: graph.NodeStruct, Name: "HTTPServer"},
			},
			want: "go://net.HTTPServer",
		},
		{
			name:  "unicode case folding",
			query: "ÜBER auth",
			nodes: []graph.Node{
				{ID: "go://auth.Other", Kind: graph.NodeFunction, Name: "OtherHandler"},
				{ID: "go://auth.Unicode", Kind: graph.NodeFunction, Name: "ÜberAuthHandler"},
			},
			want: "go://auth.Unicode",
		},
		{
			name:  "cjk bigram",
			query: "用户认证",
			nodes: []graph.Node{
				{ID: "concept://orders", Kind: graph.NodeConcept, Name: "订单处理"},
				{ID: "concept://auth", Kind: graph.NodeConcept, Name: "用户认证服务"},
			},
			want: "concept://auth",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results := NewIndex(graph.Graph{Nodes: tc.nodes}).Search(tc.query, 1)
			if len(results) != 1 || results[0].Node.ID != tc.want {
				t.Fatalf("Search(%q) = %#v, want %q first", tc.query, results, tc.want)
			}
		})
	}
}

func TestQueryTermsDropQuestionFillerAndKeepAllStopwordFallback(t *testing.T) {
	if got, want := queryTerms("how does the graph query cache work"), []string{"graph", "query", "cache"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("queryTerms() = %v, want informative terms %v", got, want)
	}

	index := NewIndex(graph.Graph{Nodes: []graph.Node{
		{ID: "node://work", Kind: graph.NodeConcept, Name: "Work"},
	}})
	results := index.Search("how does work", 1)
	if len(results) != 1 || results[0].Node.ID != "node://work" {
		t.Fatalf("all-stopword Search() = %#v, want exact filler-named node", results)
	}
}

func TestIndexRewardsUnorderedCompoundSymbolNameCoverage(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{
		{ID: "node://generic", Kind: graph.NodeFunction, Name: "Run"},
		{ID: "node://benchmark", Kind: graph.NodeFunction, Name: "runBenchmark"},
		{ID: "node://test", Kind: graph.NodeFunction, Name: "TestBenchmarkRetrievalConfiguration"},
	}}
	results := NewIndex(g).Search("how does benchmark run context retrieval", 3)
	if len(results) == 0 || results[0].Node.ID != "node://benchmark" {
		t.Fatalf("compound symbol ranking = %v, want runBenchmark first", searchResultIDs(results))
	}
}

func TestIndexPrefersSourceQualifiedExactSymbolOverVerboseCoverage(t *testing.T) {
	nodes := []graph.Node{
		{ID: "function://internal/cache.Cache", Kind: graph.NodeFunction, Name: "Cache", Path: "internal/cache/cache.go", Package: "internal/cache"},
		{ID: "function://internal/cache.LoadCache", Kind: graph.NodeFunction, Name: "LoadCache", Path: "internal/cache/load.go", Package: "internal/cache"},
		{ID: "function://internal/cache.TestCacheLoadsRareWidgetRules", Kind: graph.NodeFunction, Name: "TestCacheLoadsRareWidgetRules", Path: "internal/cache/cache_test.go", Package: "internal/cache"},
	}
	results := NewIndex(graph.Graph{Nodes: nodes}).Search("what does cache Cache call to load rare widget rules", 3)
	if len(results) == 0 || results[0].Node.ID != "function://internal/cache.Cache" {
		t.Fatalf("ranked results = %#v", results)
	}
}

func TestIndexUsesPackagePlusExactNameToDisambiguateWrapperFromMethod(t *testing.T) {
	nodes := []graph.Node{
		{ID: "function://internal/query.Search", Kind: graph.NodeFunction, Name: "Search", Path: "internal/query/query.go", Package: "internal/query"},
		{ID: "method://internal/query.(*Index).Search", Kind: graph.NodeMethod, Name: "(*Index).Search", Path: "internal/query/index.go", Package: "internal/query"},
	}
	results := NewIndex(graph.Graph{Nodes: nodes}).Search("what does query Search call to build the reusable Index", 2)
	if len(results) == 0 || results[0].Node.ID != "function://internal/query.Search" {
		t.Fatalf("ranked results = %#v", results)
	}
}

func TestIndexDoesNotTreatFileExtensionAsPackageQualification(t *testing.T) {
	nodes := []graph.Node{
		{ID: "interface://internal/lang.Analyzer", Kind: graph.NodeInterface, Name: "Analyzer", Path: "internal/lang/analyzer.go", Package: "internal/lang"},
		{ID: "method://internal/lang/goanalyzer.Analyzer.Analyze", Kind: graph.NodeMethod, Name: "Analyzer.Analyze", Path: "internal/lang/goanalyzer/analyzer.go", Package: "internal/lang/goanalyzer"},
	}
	results := NewIndex(graph.Graph{Nodes: nodes}).Search("what does go analyzer Analyze call for module import resolution", 2)
	if len(results) == 0 || results[0].Node.ID != "method://internal/lang/goanalyzer.Analyzer.Analyze" {
		t.Fatalf("ranked results = %#v", results)
	}
}

func TestIndexSearchIsDeterministicAcrossInputOrder(t *testing.T) {
	nodes := []graph.Node{
		{ID: "node://c", Kind: graph.NodeConcept, Name: "SharedName"},
		{ID: "node://a", Kind: graph.NodeConcept, Name: "SharedName"},
		{ID: "node://b", Kind: graph.NodeConcept, Name: "SharedName"},
	}
	reversed := []graph.Node{nodes[2], nodes[1], nodes[0]}

	want := []string{"node://a", "node://b", "node://c"}
	for run, candidateNodes := range [][]graph.Node{nodes, reversed, nodes, reversed} {
		got := searchResultIDs(NewIndex(graph.Graph{Nodes: candidateNodes}).Search("shared name", 0))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d Search() IDs = %v, want deterministic %v", run, got, want)
		}
	}
}

func TestIndexTrigramCandidatesUseNormalizedMetadata(t *testing.T) {
	nodes := []graph.Node{
		{ID: "node://meta", Kind: graph.NodeConcept, Name: "Target", Meta: map[string]string{"summary": "GraphNode"}},
		{ID: "node://banana", Kind: graph.NodeConcept, Name: "Banana"},
	}
	for i := 0; i < 18; i++ {
		nodes = append(nodes, graph.Node{ID: graph.ContentID("node", string(rune('a'+i))), Kind: graph.NodeConcept, Name: "Unrelated"})
	}
	results := NewIndex(graph.Graph{Nodes: nodes}).Search("graph banana", 0)
	if !containsSearchResult(results, "node://meta") {
		t.Fatalf("normalized metadata candidate missing: %#v", results)
	}
}

func TestIndexDoesNotShareMutableMetadataWithCallerOrResults(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{{ID: "node://safe", Kind: graph.NodeConcept, Name: "SafeNode", Meta: map[string]string{"summary": "original"}}}}
	index := NewIndex(g)
	g.Nodes[0].Meta["summary"] = "caller mutation"
	first := index.Search("safe node", 1)
	if first[0].Node.Meta["summary"] != "original" {
		t.Fatalf("index observed caller mutation: %#v", first[0].Node.Meta)
	}
	first[0].Node.Meta["summary"] = "result mutation"
	second := index.Search("safe node", 1)
	if second[0].Node.Meta["summary"] != "original" {
		t.Fatalf("index observed result mutation: %#v", second[0].Node.Meta)
	}
}

func searchResultIDs(results []SearchResult) []string {
	ids := make([]string, len(results))
	for i, result := range results {
		ids[i] = result.Node.ID
	}
	return ids
}

func containsSearchResult(results []SearchResult, id string) bool {
	for _, result := range results {
		if result.Node.ID == id {
			return true
		}
	}
	return false
}
