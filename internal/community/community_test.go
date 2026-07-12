package community

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/12vault/ravel/internal/graph"
)

func TestAssignFindsSeparateCommunities(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "x"}, {ID: "y"}, {ID: "z"}},
		Edges: []graph.Edge{
			{From: "a", To: "b", Kind: graph.EdgeCalls}, {From: "b", To: "c", Kind: graph.EdgeCalls}, {From: "c", To: "a", Kind: graph.EdgeCalls},
			{From: "x", To: "y", Kind: graph.EdgeCalls}, {From: "y", To: "z", Kind: graph.EdgeCalls}, {From: "z", To: "x", Kind: graph.EdgeCalls},
		},
	}
	got := Assign(g)
	communities := nodeCommunities(got)
	if communities["a"] != communities["b"] || communities["b"] != communities["c"] {
		t.Fatalf("first component was split: %#v", communities)
	}
	if communities["x"] != communities["y"] || communities["y"] != communities["z"] {
		t.Fatalf("second component was split: %#v", communities)
	}
	if communities["a"] == communities["x"] {
		t.Fatalf("disconnected components share a community: %#v", communities)
	}
}

func TestAssignNamesSummarizesAndRemovesCommunities(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{{ID: "a", Kind: graph.NodeFile, Name: "a.go", Path: "internal/query/a.go"}, {ID: "b", Kind: graph.NodeFunction, Name: "Run", Path: "internal/query/a.go"}},
		Edges: []graph.Edge{{From: "a", To: "b", Kind: graph.EdgeDefines}},
	}
	assigned := Assign(g)
	if got := assigned.Nodes[0].Meta[MetaNameKey]; got != "internal/query" {
		t.Fatalf("community name = %q", got)
	}
	if got := assigned.Nodes[0].Meta[MetaSizeKey]; got != "2" {
		t.Fatalf("community size = %q", got)
	}
	summaries := Summaries(assigned)
	if len(summaries) != 1 || summaries[0].Name != "internal/query" || summaries[0].Size != 2 {
		t.Fatalf("summaries = %#v", summaries)
	}
	removed := Remove(assigned)
	if _, ok := removed.Nodes[0].Meta[MetaKey]; ok {
		t.Fatal("Remove retained community metadata")
	}
}

func TestAssignUsesHubDownWeightingAndGranularityMetadata(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{{ID: "a"}, {ID: "b"}}, Edges: []graph.Edge{{From: "a", To: "b", Kind: graph.EdgeCalls}}}
	got := AssignWithOptions(g, Options{Granularity: PresetFine, HubDegreeThreshold: 7})
	if got.Nodes[0].Meta[MetaGranularityKey] != "fine" || got.Nodes[0].Meta[MetaHubThresholdKey] != "7" {
		t.Fatalf("community settings metadata = %#v", got.Nodes[0].Meta)
	}
	if value := downWeightHub(3*1024, 100, 50); value != 1536 {
		t.Fatalf("down-weighted hub edge = %d", value)
	}
}

func TestHubDownWeightingPreservesDenseGroups(t *testing.T) {
	g := graph.Graph{}
	for _, id := range []string{"a1", "a2", "a3", "a4", "b1", "b2", "b3", "b4", "hub"} {
		g.Nodes = append(g.Nodes, graph.Node{ID: id})
	}
	for _, group := range [][]string{{"a1", "a2", "a3", "a4"}, {"b1", "b2", "b3", "b4"}} {
		for i := range group {
			for j := i + 1; j < len(group); j++ {
				g.Edges = append(g.Edges, graph.Edge{From: group[i], To: group[j], Kind: graph.EdgeCalls})
			}
		}
		for _, id := range group {
			g.Edges = append(g.Edges, graph.Edge{From: "hub", To: id, Kind: graph.EdgeReferences})
		}
	}
	assigned := AssignWithOptions(g, Options{Granularity: PresetBalanced, HubDegreeThreshold: 2})
	communities := nodeCommunities(assigned)
	if communities["a1"] != communities["a4"] || communities["b1"] != communities["b4"] {
		t.Fatalf("dense groups were split: %#v", communities)
	}
	if communities["a1"] == communities["b1"] {
		t.Fatalf("hub collapsed distinct dense groups: %#v", communities)
	}
}

func TestGranularityPresetsChangeResolution(t *testing.T) {
	g := graph.Graph{}
	for i := 0; i < 18; i++ {
		g.Nodes = append(g.Nodes, graph.Node{ID: "n" + strconv.Itoa(i)})
	}
	for i := 0; i < 17; i++ {
		g.Edges = append(g.Edges, graph.Edge{From: "n" + strconv.Itoa(i), To: "n" + strconv.Itoa(i+1), Kind: graph.EdgeCalls})
	}
	coarse := len(Summaries(AssignWithOptions(g, Options{Granularity: PresetCoarse, HubDegreeThreshold: -1})))
	fine := len(Summaries(AssignWithOptions(g, Options{Granularity: PresetFine, HubDegreeThreshold: -1})))
	if fine <= coarse {
		t.Fatalf("fine communities = %d, coarse = %d; want finer partition", fine, coarse)
	}
}

func TestDeterministicNameIncludesTwoStrongPathGroups(t *testing.T) {
	nodes := []graph.Node{
		{ID: "a", Kind: graph.NodeFile, Path: "internal/query/a.go"},
		{ID: "b", Kind: graph.NodeFile, Path: "internal/query/b.go"},
		{ID: "c", Kind: graph.NodeFile, Path: "internal/mcp/c.go"},
		{ID: "d", Kind: graph.NodeFile, Path: "internal/mcp/d.go"},
	}
	if got := name(nodes); got != "internal/mcp + internal/query" {
		t.Fatalf("name = %q", got)
	}
}

func TestDescriptionsNeverChangeCommunityIdentity(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}, Edges: []graph.Edge{{From: "a", To: "b", Kind: graph.EdgeCalls}}}
	assigned := Assign(g)
	id, deterministicName := assigned.Nodes[0].Meta[MetaKey], assigned.Nodes[0].Meta[MetaNameKey]
	file := DescriptionFile{Version: 1, Source: "test-ai", Descriptions: []Description{{Community: id, Text: "Coordinates request handling.", Rationale: "Members share request-flow responsibilities."}}}
	described, err := ApplyDescriptions(assigned, file)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range described.Nodes {
		if node.Meta[MetaKey] != id || node.Meta[MetaNameKey] != deterministicName {
			t.Fatal("AI description changed deterministic identity")
		}
		if node.Meta[MetaDescriptionConfidenceKey] != "inferred" || node.Meta[MetaDescriptionSourceKey] != "test-ai" {
			t.Fatalf("description provenance = %#v", node.Meta)
		}
	}
	changed := described
	changed.Nodes = append(changed.Nodes, graph.Node{ID: "c", Name: "C"})
	changed.Edges = append(changed.Edges, graph.Edge{From: "b", To: "c", Kind: graph.EdgeCalls})
	reassigned := Assign(changed)
	for _, node := range reassigned.Nodes {
		if node.Meta[MetaDescriptionKey] != "" {
			t.Fatal("stale description survived changed community identity")
		}
	}
}

func TestDescriptionImportRejectsAIIdentityFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "descriptions.json")
	data := `{"version":1,"source":"test-ai","descriptions":[{"community":"c-1","description":"Text","rationale":"Facts","name":"AI name","members":["x"]}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDescriptions(path); err == nil {
		t.Fatal("identity fields were accepted")
	}
}

func BenchmarkAssignLargeGraph(b *testing.B) {
	const nodes = 10_000
	g := graph.Graph{Nodes: make([]graph.Node, nodes), Edges: make([]graph.Edge, 0, nodes*3)}
	for i := 0; i < nodes; i++ {
		id := "n" + strconv.Itoa(i)
		g.Nodes[i] = graph.Node{ID: id, Kind: graph.NodeFunction, Name: id, Path: "internal/p" + strconv.Itoa(i/100) + "/file.go"}
		if i > 0 {
			g.Edges = append(g.Edges, graph.Edge{From: "n" + strconv.Itoa(i-1), To: id, Kind: graph.EdgeCalls})
		}
		if i >= 100 {
			g.Edges = append(g.Edges, graph.Edge{From: "n" + strconv.Itoa(i-100), To: id, Kind: graph.EdgeReferences})
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Assign(g)
	}
}

func TestAssignIsStableAcrossInputOrder(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{{ID: "c"}, {ID: "a"}, {ID: "b"}, {ID: "isolated", Meta: map[string]string{"keep": "yes"}}},
		Edges: []graph.Edge{{From: "b", To: "c", Kind: graph.EdgeReferences}, {From: "a", To: "b", Kind: graph.EdgeCalls}},
	}
	reversed := g
	reversed.Nodes = append([]graph.Node(nil), g.Nodes...)
	reversed.Edges = append([]graph.Edge(nil), g.Edges...)
	sort.Slice(reversed.Nodes, func(i, j int) bool { return reversed.Nodes[i].ID < reversed.Nodes[j].ID })
	sort.Slice(reversed.Edges, func(i, j int) bool { return reversed.Edges[i].From > reversed.Edges[j].From })

	first, second := nodeCommunities(Assign(g)), nodeCommunities(Assign(reversed))
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("community IDs changed with input order:\nfirst: %#v\nsecond: %#v", first, second)
	}
	assigned := Assign(g)
	for _, node := range assigned.Nodes {
		if node.ID == "isolated" && node.Meta["keep"] != "yes" {
			t.Fatal("existing metadata was not preserved")
		}
	}
	if _, exists := g.Nodes[3].Meta[MetaKey]; exists {
		t.Fatal("Assign mutated its input graph")
	}
}

func nodeCommunities(g graph.Graph) map[string]string {
	out := map[string]string{}
	for _, node := range g.Nodes {
		out[node.ID] = node.Meta[MetaKey]
	}
	return out
}
