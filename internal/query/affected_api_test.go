package query

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/graph"
)

func TestIndexAffectedTraversesIncomingDependentsOnly(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "caller", Kind: graph.NodeFunction, Name: "Caller"},
			{ID: "target", Kind: graph.NodeFunction, Name: "Target"},
			{ID: "callee", Kind: graph.NodeFunction, Name: "Callee"},
			{ID: "file", Kind: graph.NodeFile, Name: "target.go", Path: "target.go"},
		},
		Edges: []graph.Edge{
			{ID: "calls://caller-target", Kind: graph.EdgeCalls, From: "caller", To: "target"},
			{ID: "calls://target-callee", Kind: graph.EdgeCalls, From: "target", To: "callee"},
			{ID: "defines://file-target", Kind: graph.EdgeDefines, From: "file", To: "target"},
		},
	}
	result, err := NewIndex(g).Affected("Target", RetrieveOptions{
		Relations: []graph.EdgeKind{graph.EdgeCalls}, MaxDepth: 2, MaxNodes: 10,
		HubDegreeThreshold: -1, TokenBudget: 1_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Direction != DirectionIn || result.Stats.Traversal != TraversalBFS || !reflect.DeepEqual(result.Stats.SeedIDs, []string{"target"}) {
		t.Fatalf("affected stats = %#v", result.Stats)
	}
	ids := map[string]bool{}
	for _, node := range result.Nodes {
		ids[node.ID] = true
	}
	if !ids["target"] || !ids["caller"] || ids["callee"] {
		t.Fatalf("affected node IDs = %v", ids)
	}

	wrapper, err := Affected(g, "Target", RetrieveOptions{
		Relations: []graph.EdgeKind{graph.EdgeCalls}, MaxDepth: 2, MaxNodes: 10,
		HubDegreeThreshold: -1, TokenBudget: 1_000,
	})
	if err != nil || !reflect.DeepEqual(wrapper, result) {
		t.Fatalf("Affected wrapper = %#v, %v; want %#v", wrapper, err, result)
	}

	defaultResult, err := NewIndex(g).Affected("Target", RetrieveOptions{MaxDepth: 2, MaxNodes: 10, HubDegreeThreshold: -1, TokenBudget: 1_000})
	if err != nil {
		t.Fatal(err)
	}
	defaultIDs := map[string]bool{}
	for _, node := range defaultResult.Nodes {
		defaultIDs[node.ID] = true
	}
	if !defaultIDs["caller"] || defaultIDs["file"] || defaultIDs["callee"] {
		t.Fatalf("default affected node IDs = %v; want incoming call impact without containment noise", defaultIDs)
	}
	if defaultResult.Stats.RelationFilterFrom != "affected-default" || !reflect.DeepEqual(defaultResult.Stats.RelationFilters, []graph.EdgeKind{graph.EdgeCalls}) {
		t.Fatalf("default affected filters = %#v", defaultResult.Stats)
	}
}

func TestIndexAffectedGuaranteesResolvedTargetSeed(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "go://pkg.Target", Kind: graph.NodeFunction, Name: "Target"},
			{ID: "decoy", Kind: graph.NodeFunction, Name: "go pkg Target"},
			{ID: "caller", Kind: graph.NodeFunction, Name: "Caller"},
		},
		Edges: []graph.Edge{{ID: "calls://caller-target", Kind: graph.EdgeCalls, From: "caller", To: "go://pkg.Target"}},
	}
	result, err := NewIndex(g).Affected("go://pkg.Target", RetrieveOptions{MaxDepth: 1, MaxNodes: 10, HubDegreeThreshold: -1, TokenBudget: 1_000})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Stats.SeedIDs, []string{"go://pkg.Target"}) || len(result.Nodes) == 0 || result.Nodes[0].ID != "go://pkg.Target" {
		t.Fatalf("affected exact target was not guaranteed as seed: %#v", result)
	}
}

func TestIndexAffectedBootstrapsFileDefinitionsWithEvidence(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "file", Kind: graph.NodeFile, Name: "service.go", Path: "service.go"},
			{ID: "charge", Kind: graph.NodeFunction, Name: "Charge", Path: "service.go"},
			{ID: "refund", Kind: graph.NodeFunction, Name: "Refund", Path: "service.go"},
			{ID: "charge-caller", Kind: graph.NodeFunction, Name: "Checkout"},
			{ID: "refund-caller", Kind: graph.NodeFunction, Name: "Cancel"},
			{ID: "causal-upstream", Kind: graph.NodeStep, Name: "Upstream"},
			{ID: "causal-downstream", Kind: graph.NodeStep, Name: "Downstream"},
		},
		Edges: []graph.Edge{
			{ID: "defines://file-charge", Kind: graph.EdgeDefines, From: "file", To: "charge", Meta: map[string]string{"confidence": "extracted", "evidence": "service.go:10"}},
			{ID: "defines://file-refund", Kind: graph.EdgeDefines, From: "file", To: "refund", Meta: map[string]string{"confidence": "extracted", "evidence": "service.go:20"}},
			{ID: "calls://checkout-charge", Kind: graph.EdgeCalls, From: "charge-caller", To: "charge"},
			{ID: "calls://cancel-refund", Kind: graph.EdgeCalls, From: "refund-caller", To: "refund"},
			{ID: "affects://upstream-file", Kind: graph.EdgeAffects, From: "causal-upstream", To: "file"},
			{ID: "flows://file-downstream", Kind: graph.EdgeFlowsTo, From: "file", To: "causal-downstream"},
		},
	}
	result, err := NewIndex(g).Affected("service.go", RetrieveOptions{
		MaxDepth: 1, MaxNodes: 20, HubDegreeThreshold: -1, TokenBudget: 5_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Stats.SeedIDs, []string{"file", "charge", "refund"}) {
		t.Fatalf("file bootstrap seeds = %v", result.Stats.SeedIDs)
	}
	nodeIDs := map[string]bool{}
	for _, node := range result.Nodes {
		nodeIDs[node.ID] = true
	}
	for _, want := range []string{"file", "charge", "refund", "charge-caller", "refund-caller"} {
		if !nodeIDs[want] {
			t.Fatalf("affected file result missing %q: %v", want, nodeIDs)
		}
	}
	if nodeIDs["causal-upstream"] || nodeIDs["causal-downstream"] {
		t.Fatalf("default affected result followed causal edges with incompatible orientation: %v", nodeIDs)
	}
	edges := map[string]ContextEdge{}
	for _, edge := range result.Edges {
		edges[edge.ID] = edge
	}
	if edges["defines://file-charge"].Evidence != "service.go:10" || edges["defines://file-refund"].Evidence != "service.go:20" {
		t.Fatalf("definition evidence missing from bootstrap: %#v", edges)
	}
	if !reflect.DeepEqual(result.Stats.RelationFilters, []graph.EdgeKind{graph.EdgeCalls}) {
		t.Fatalf("affected defaults = %v; want reverse dependency kinds only", result.Stats.RelationFilters)
	}
}

func TestIndexAffectedBootstrapsPackageFilesAndDefinitions(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "go-package://internal/payments", Kind: graph.NodePackage, Name: "payments", Path: "internal/payments"},
			{ID: "file-a", Kind: graph.NodeFile, Name: "charge.go", Path: "internal/payments/charge.go"},
			{ID: "file-b", Kind: graph.NodeFile, Name: "refund.go", Path: "internal/payments/refund.go"},
			{ID: "charge", Kind: graph.NodeFunction, Name: "Charge"},
			{ID: "refund", Kind: graph.NodeFunction, Name: "Refund"},
			{ID: "caller-a", Kind: graph.NodeFunction, Name: "Checkout"},
			{ID: "caller-b", Kind: graph.NodeFunction, Name: "Cancel"},
		},
		Edges: []graph.Edge{
			{ID: "contains://package-a", Kind: graph.EdgeContains, From: "go-package://internal/payments", To: "file-a", Meta: map[string]string{"evidence": "internal/payments"}},
			{ID: "contains://package-b", Kind: graph.EdgeContains, From: "go-package://internal/payments", To: "file-b", Meta: map[string]string{"evidence": "internal/payments"}},
			{ID: "defines://a-charge", Kind: graph.EdgeDefines, From: "file-a", To: "charge", Meta: map[string]string{"evidence": "internal/payments/charge.go:4"}},
			{ID: "defines://b-refund", Kind: graph.EdgeDefines, From: "file-b", To: "refund", Meta: map[string]string{"evidence": "internal/payments/refund.go:7"}},
			{ID: "calls://a-charge", Kind: graph.EdgeCalls, From: "caller-a", To: "charge"},
			{ID: "calls://b-refund", Kind: graph.EdgeCalls, From: "caller-b", To: "refund"},
		},
	}
	result, err := NewIndex(g).Affected("go-package://internal/payments", RetrieveOptions{
		MaxDepth: 1, MaxNodes: 20, HubDegreeThreshold: -1, TokenBudget: 5_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantSeeds := []string{"go-package://internal/payments", "file-a", "charge", "file-b", "refund"}
	if !reflect.DeepEqual(result.Stats.SeedIDs, wantSeeds) {
		t.Fatalf("package bootstrap seeds = %v, want %v", result.Stats.SeedIDs, wantSeeds)
	}
	nodeIDs := map[string]bool{}
	for _, node := range result.Nodes {
		nodeIDs[node.ID] = true
	}
	if !nodeIDs["caller-a"] || !nodeIDs["caller-b"] {
		t.Fatalf("package affected result missed symbol callers: %v", nodeIDs)
	}
	edgeIDs := map[string]bool{}
	for _, edge := range result.Edges {
		edgeIDs[edge.ID] = true
	}
	for _, want := range []string{"contains://package-a", "contains://package-b", "defines://a-charge", "defines://b-refund"} {
		if !edgeIDs[want] {
			t.Fatalf("package affected result missing bootstrap evidence %q: %v", want, edgeIDs)
		}
	}
}

func TestIndexAffectedBoundsDefinitionBootstrapDeterministically(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{{ID: "file", Kind: graph.NodeFile, Name: "wide.go", Path: "wide.go"}}}
	for index := 29; index >= 0; index-- {
		id := fmt.Sprintf("symbol-%02d", index)
		g.Nodes = append(g.Nodes, graph.Node{ID: id, Kind: graph.NodeFunction, Name: id})
		g.Edges = append(g.Edges, graph.Edge{ID: "defines://" + id, Kind: graph.EdgeDefines, From: "file", To: id})
	}
	result, err := NewIndex(g).Affected("wide.go", RetrieveOptions{MaxDepth: 1, MaxNodes: 100, HubDegreeThreshold: -1, TokenBudget: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Stats.SeedIDs) != maximumAffectedBootstrapSeeds {
		t.Fatalf("bootstrap seed count = %d, want %d", len(result.Stats.SeedIDs), maximumAffectedBootstrapSeeds)
	}
	if result.Stats.SeedIDs[0] != "file" || result.Stats.SeedIDs[1] != "symbol-00" || result.Stats.SeedIDs[len(result.Stats.SeedIDs)-1] != "symbol-18" {
		t.Fatalf("bootstrap seeds are not stable and sorted: %v", result.Stats.SeedIDs)
	}
}

func TestIndexAffectedReservesBudgetAndPrioritizesDefinitionsWithDependents(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{
		{ID: "file", Kind: graph.NodeFile, Name: "wide.go", Path: "wide.go"},
		{ID: "caller", Kind: graph.NodeFunction, Name: "Caller"},
	}}
	for index := 0; index < 20; index++ {
		id := fmt.Sprintf("symbol-%02d", index)
		g.Nodes = append(g.Nodes, graph.Node{ID: id, Kind: graph.NodeFunction, Name: id})
		g.Edges = append(g.Edges, graph.Edge{ID: "defines://" + id, Kind: graph.EdgeDefines, From: "file", To: id})
	}
	g.Edges = append(g.Edges, graph.Edge{ID: "calls://caller-important", Kind: graph.EdgeCalls, From: "caller", To: "symbol-19"})
	result, err := NewIndex(g).Affected("wide.go", RetrieveOptions{
		Relations: []graph.EdgeKind{graph.EdgeCalls}, MaxDepth: 1, MaxNodes: 100,
		HubDegreeThreshold: -1, TokenBudget: 2_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Stats.SeedIDs) != 8 || result.Stats.SeedIDs[1] != "symbol-19" {
		t.Fatalf("budgeted impact seeds = %v, want 8 with depended-on symbol first", result.Stats.SeedIDs)
	}
	nodeIDs := map[string]bool{}
	for _, node := range result.Nodes {
		nodeIDs[node.ID] = true
	}
	if !nodeIDs["caller"] {
		t.Fatalf("budgeted file impact omitted dependent caller: nodes=%v", nodeIDs)
	}
}

func TestIndexAffectedTraversesExplicitCausalRelationsForward(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "target", Kind: graph.NodeStep, Name: "Target"},
			{ID: "upstream", Kind: graph.NodeStep, Name: "Upstream"},
			{ID: "downstream", Kind: graph.NodeStep, Name: "Downstream"},
		},
		Edges: []graph.Edge{
			{ID: "affects://upstream-target", Kind: graph.EdgeAffects, From: "upstream", To: "target"},
			{ID: "affects://target-downstream", Kind: graph.EdgeAffects, From: "target", To: "downstream"},
		},
	}
	result, err := NewIndex(g).Affected("Target", RetrieveOptions{
		Relations: []graph.EdgeKind{" affects "}, MaxDepth: 1, MaxNodes: 10,
		HubDegreeThreshold: -1, TokenBudget: 1_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, node := range result.Nodes {
		ids[node.ID] = true
	}
	if result.Stats.Direction != DirectionOut || !ids["downstream"] || ids["upstream"] {
		t.Fatalf("explicit causal affected traversal = %#v, nodes=%v", result.Stats, ids)
	}
}

func TestWriteAffectedLabelsCompactOutput(t *testing.T) {
	result := Retrieval{
		Query: "target",
		Nodes: []ContextNode{{ID: "target", Kind: graph.NodeFunction, Name: "Target", Seed: true}},
		Stats: RetrievalStats{Traversal: TraversalBFS, Direction: DirectionIn, Depth: 2, TokenBudget: 256},
	}
	var output bytes.Buffer
	if err := WriteAffected(&output, result, false); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "RAVEL_AFFECTED") {
		t.Fatalf("affected output = %q", output.String())
	}
}

func TestReusableIndexExplainAndShortestPathMatchGraphWrappers(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "one", Kind: graph.NodeFunction, Name: "One"},
			{ID: "two", Kind: graph.NodeFunction, Name: "Two"},
		},
		Edges: []graph.Edge{{ID: "calls://one-two", Kind: graph.EdgeCalls, From: "one", To: "two"}},
	}
	idx := NewIndex(g)
	indexedExplanation, indexedOK := idx.Explain("One")
	wrapperExplanation, wrapperOK := Explain(g, "One")
	if indexedOK != wrapperOK || !reflect.DeepEqual(indexedExplanation, wrapperExplanation) {
		t.Fatalf("indexed explanation = %#v, %v; wrapper = %#v, %v", indexedExplanation, indexedOK, wrapperExplanation, wrapperOK)
	}
	indexedPath, indexedOK := idx.ShortestPath("One", "Two")
	wrapperPath, wrapperOK := ShortestPath(g, "One", "Two")
	if indexedOK != wrapperOK || !reflect.DeepEqual(indexedPath, wrapperPath) {
		t.Fatalf("indexed path = %#v, %v; wrapper = %#v, %v", indexedPath, indexedOK, wrapperPath, wrapperOK)
	}
}
