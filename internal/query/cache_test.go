package query

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/graph"
)

func TestLoadOrBuildIndexPersistsAndReusesEquivalentIndex(t *testing.T) {
	value := cachedIndexTestGraph("Beta")
	data := marshalCachedIndexTestGraph(t, value)
	cacheDir := filepath.Join(t.TempDir(), "cache")

	first, hit, err := LoadOrBuildIndex(data, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Fatal("cold index load reported a cache hit")
	}
	hash := sha256.Sum256(data)
	cachePath := persistedIndexPath(cacheDir, hash)
	if info, err := os.Stat(cachePath); err != nil || info.Size() == 0 {
		t.Fatalf("persisted index stat = %#v, %v", info, err)
	}

	second, hit, err := LoadOrBuildIndex(data, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Fatal("warm index load rebuilt the index")
	}
	if got, want := second.Search("Beta", 5), first.Search("Beta", 5); !reflect.DeepEqual(got, want) {
		t.Fatalf("cached search = %#v, want %#v", got, want)
	}
	if got, want := mustCachedExplanation(t, second, "Alpha"), mustCachedExplanation(t, first, "Alpha"); !reflect.DeepEqual(got, want) {
		t.Fatalf("cached explanation = %#v, want %#v", got, want)
	}
	gotPath, gotOK, gotErr := second.ShortestPathResult("Alpha", "Beta")
	wantPath, wantOK, wantErr := first.ShortestPathResult("Alpha", "Beta")
	if gotErr != nil || wantErr != nil || gotOK != wantOK || !reflect.DeepEqual(gotPath, wantPath) {
		t.Fatalf("cached path = (%#v, %v, %v), want (%#v, %v, %v)", gotPath, gotOK, gotErr, wantPath, wantOK, wantErr)
	}
}

func TestLoadOrBuildIndexInvalidatesByGraphHashAndPrunesOldSnapshot(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	firstData := marshalCachedIndexTestGraph(t, cachedIndexTestGraph("Beta"))
	if _, hit, err := LoadOrBuildIndex(firstData, cacheDir); err != nil || hit {
		t.Fatalf("first load hit=%v err=%v", hit, err)
	}

	secondData := marshalCachedIndexTestGraph(t, cachedIndexTestGraph("Gamma"))
	second, hit, err := LoadOrBuildIndex(secondData, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Fatal("changed graph bytes reused a stale index")
	}
	if got := second.Search("Gamma", 1); len(got) != 1 || got[0].Node.Name != "Gamma" {
		t.Fatalf("rebuilt search = %#v", got)
	}

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	cacheFiles := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), indexCachePrefix) && strings.HasSuffix(entry.Name(), indexCacheSuffix) {
			cacheFiles++
		}
	}
	if cacheFiles != 1 {
		t.Fatalf("hash-keyed cache files = %d, want one current snapshot; entries=%#v", cacheFiles, entries)
	}
}

func TestLoadOrBuildIndexRepairsCorruptCache(t *testing.T) {
	data := marshalCachedIndexTestGraph(t, cachedIndexTestGraph("Beta"))
	cacheDir := filepath.Join(t.TempDir(), "cache")
	if _, _, err := LoadOrBuildIndex(data, cacheDir); err != nil {
		t.Fatal(err)
	}
	cachePath := persistedIndexPath(cacheDir, sha256.Sum256(data))
	if err := os.WriteFile(cachePath, []byte("not a persisted index"), 0o600); err != nil {
		t.Fatal(err)
	}

	repaired, hit, err := LoadOrBuildIndex(data, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Fatal("corrupt cache reported a hit")
	}
	if got := repaired.Search("Beta", 1); len(got) != 1 || got[0].Node.Name != "Beta" {
		t.Fatalf("repaired search = %#v", got)
	}
	if _, hit, err := LoadOrBuildIndex(data, cacheDir); err != nil || !hit {
		t.Fatalf("repaired cache was not reusable: hit=%v err=%v", hit, err)
	}
}

func TestLoadOrBuildIndexTreatsCacheWriteFailureAsBestEffort(t *testing.T) {
	data := marshalCachedIndexTestGraph(t, cachedIndexTestGraph("Beta"))
	cacheDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(cacheDir, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}

	index, hit, err := LoadOrBuildIndex(data, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Fatal("unwritable cold cache reported a hit")
	}
	if got := index.Search("Beta", 1); len(got) != 1 || got[0].Node.Name != "Beta" {
		t.Fatalf("best-effort cache search = %#v", got)
	}
}

func cachedIndexTestGraph(secondName string) graph.Graph {
	return graph.Graph{
		Version: "test",
		Nodes: []graph.Node{
			{ID: "function://alpha", Kind: graph.NodeFunction, Name: "Alpha", Path: "alpha.go", Meta: map[string]string{"community": "one"}},
			{ID: "function://second", Kind: graph.NodeFunction, Name: secondName, Path: "beta.go"},
		},
		Edges: []graph.Edge{{
			ID: "calls://alpha/second", Kind: graph.EdgeCalls, From: "function://alpha", To: "function://second",
			Meta: map[string]string{"confidence": "extracted"},
		}},
	}
}

func marshalCachedIndexTestGraph(t *testing.T, value graph.Graph) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustCachedExplanation(t *testing.T, index *Index, target string) Explanation {
	t.Helper()
	explanation, err := index.ExplainResolved(target)
	if err != nil {
		t.Fatal(err)
	}
	return explanation
}
