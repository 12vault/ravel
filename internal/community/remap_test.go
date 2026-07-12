package community

import (
	"strconv"
	"testing"

	"github.com/12vault/ravel/internal/graph"
)

func TestRemapLabelsKeepsExactLabelAndDescription(t *testing.T) {
	previous := remapFixture(map[string][]string{"c-old": {"a", "b"}}, map[string]string{"c-old": "Authentication"})
	for i := range previous.Nodes {
		previous.Nodes[i].Meta[MetaDescriptionKey] = "Handles login."
	}
	current := remapFixture(map[string][]string{"c-old": {"a", "b"}}, map[string]string{"c-old": "internal/auth"})
	got := RemapLabels(current, previous)
	assertCommunityMeta(t, got, "c-old", MetaLabelKey, "Authentication")
	assertCommunityMeta(t, got, "c-old", MetaLabelStatusKey, "stable")
	assertCommunityMeta(t, got, "c-old", MetaDescriptionKey, "Handles login.")
}

func BenchmarkRemapLabelsTenThousandCommunities(b *testing.B) {
	previous, current := graph.Graph{}, graph.Graph{}
	for i := 0; i < 10_000; i++ {
		id, oldCommunity, newCommunity := "n"+strconv.Itoa(i), "old-"+strconv.Itoa(i), "new-"+strconv.Itoa(i)
		previous.Nodes = append(previous.Nodes, graph.Node{ID: id, Meta: map[string]string{MetaKey: oldCommunity, MetaNameKey: oldCommunity, MetaLabelKey: oldCommunity}})
		current.Nodes = append(current.Nodes, graph.Node{ID: id, Meta: map[string]string{MetaKey: newCommunity, MetaNameKey: newCommunity, MetaLabelKey: newCommunity}})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = RemapLabels(current, previous)
	}
}

func TestRemapLabelsTransfersConfidentLabelButNotDescription(t *testing.T) {
	previous := remapFixture(map[string][]string{"c-old": {"a", "b", "c", "d"}}, map[string]string{"c-old": "Authentication"})
	for i := range previous.Nodes {
		previous.Nodes[i].Meta[MetaDescriptionKey] = "Handles sessions."
	}
	current := remapFixture(map[string][]string{"c-new": {"a", "b", "c", "d", "e"}}, map[string]string{"c-new": "internal/oauth"})
	got := RemapLabels(current, previous)
	assertCommunityMeta(t, got, "c-new", MetaLabelKey, "Authentication")
	assertCommunityMeta(t, got, "c-new", MetaLabelStatusKey, "remapped")
	assertCommunityMeta(t, got, "c-new", MetaLabelOverlapKey, "0.800")
	assertCommunityMeta(t, got, "c-new", MetaDescriptionKey, "")
}

func TestRemapLabelsMarksMediumOverlapProvisional(t *testing.T) {
	previous := remapFixture(map[string][]string{"c-old": {"a", "b", "c"}}, map[string]string{"c-old": "Authentication"})
	current := remapFixture(map[string][]string{"c-new": {"a", "b", "x"}}, map[string]string{"c-new": "internal/auth"})
	got := RemapLabels(current, previous)
	assertCommunityMeta(t, got, "c-new", MetaLabelKey, "Authentication")
	assertCommunityMeta(t, got, "c-new", MetaLabelStatusKey, "provisional")
	assertCommunityMeta(t, got, "c-new", MetaLabelOverlapKey, "0.500")
}

func TestRemapLabelsRejectsWeakOverlap(t *testing.T) {
	previous := remapFixture(map[string][]string{"c-old": {"a", "b", "c"}}, map[string]string{"c-old": "Authentication"})
	current := remapFixture(map[string][]string{"c-new": {"a", "x", "y"}}, map[string]string{"c-new": "internal/new"})
	got := RemapLabels(current, previous)
	assertCommunityMeta(t, got, "c-new", MetaLabelKey, "internal/new")
	assertCommunityMeta(t, got, "c-new", MetaLabelStatusKey, "deterministic")
}

func TestRemapLabelsGivesSplitLabelOnlyToStrongestMatch(t *testing.T) {
	previous := remapFixture(map[string][]string{"c-old": {"a", "b", "c", "d"}}, map[string]string{"c-old": "Authentication"})
	current := remapFixture(map[string][]string{"c-new-a": {"a", "b"}, "c-new-b": {"c", "d"}}, map[string]string{"c-new-a": "auth/a", "c-new-b": "auth/b"})
	got := RemapLabels(current, previous)
	assertCommunityMeta(t, got, "c-new-a", MetaLabelKey, "Authentication")
	assertCommunityMeta(t, got, "c-new-b", MetaLabelKey, "auth/b")
}

func TestRemapLabelsDoesNotInheritAcrossMerge(t *testing.T) {
	previous := remapFixture(map[string][]string{"c-old-a": {"a", "b"}, "c-old-b": {"c", "d"}}, map[string]string{"c-old-a": "Authentication", "c-old-b": "Sessions"})
	current := remapFixture(map[string][]string{"c-new": {"a", "b", "c", "d"}}, map[string]string{"c-new": "internal/auth"})
	got := RemapLabels(current, previous)
	assertCommunityMeta(t, got, "c-new", MetaLabelKey, "internal/auth")
	assertCommunityMeta(t, got, "c-new", MetaLabelStatusKey, "deterministic")
}

func remapFixture(communities map[string][]string, labels map[string]string) graph.Graph {
	var g graph.Graph
	for communityID, members := range communities {
		for _, id := range members {
			g.Nodes = append(g.Nodes, graph.Node{ID: id, Name: id, Meta: map[string]string{
				MetaKey: communityID, MetaNameKey: labels[communityID], MetaLabelKey: labels[communityID], MetaLabelStatusKey: "deterministic",
			}})
		}
	}
	return g
}

func assertCommunityMeta(t *testing.T, g graph.Graph, communityID, key, want string) {
	t.Helper()
	for _, node := range g.Nodes {
		if node.Meta[MetaKey] != communityID {
			continue
		}
		if got := node.Meta[key]; got != want {
			t.Fatalf("community %s %s = %q, want %q", communityID, key, got, want)
		}
		return
	}
	t.Fatalf("community %s not found", communityID)
}
