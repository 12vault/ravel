package evaluation

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/query"
)

func TestRunScoresRetrievalCases(t *testing.T) {
	g := graph.Graph{Nodes: []graph.Node{
		{ID: "domain://billing", Kind: graph.NodeDomain, Name: "Billing payments"},
		{ID: "domain://identity", Kind: graph.NodeDomain, Name: "User identity"},
	}}
	report, err := Run(g, []Case{{ID: "q1", Dataset: "LOCOMO", Question: "billing payments", ExpectedNodeIDs: []string{"domain://billing"}}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if report.Overall.MeanRecall != 1 || report.Overall.MeanReciprocalRank != 1 {
		t.Fatalf("unexpected metrics: %#v", report.Overall)
	}
	var rendered bytes.Buffer
	if err := query.WriteSearch(&rendered, query.Search(g, "billing payments", 1), false); err != nil {
		t.Fatal(err)
	}
	if got, want := report.Results[0].EstimatedTokens, estimatedTokens(rendered.Bytes()); got != want {
		t.Fatalf("flat estimated tokens = %d, want rendered compact output estimate %d", got, want)
	}
}

func TestRunRejectsInvalidTopK(t *testing.T) {
	if _, err := Run(graph.Graph{}, nil, 0); err == nil {
		t.Fatal("expected top-k error")
	}
}

func TestRunWithOptionsMeasuresContextAndEvidenceEfficiency(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "function://checkout", Kind: graph.NodeFunction, Name: "Checkout"},
			{ID: "function://charge", Kind: graph.NodeFunction, Name: "ChargeCard"},
		},
		Edges: []graph.Edge{{ID: "calls://checkout-charge", Kind: graph.EdgeCalls, From: "function://checkout", To: "function://charge"}},
	}
	report, err := RunWithOptions(g, []Case{{
		ID: "q1", Dataset: "repository-questions", Question: "checkout calls charge card",
		ExpectedNodeIDs:  []string{"function://checkout", "function://charge"},
		ExpectedEvidence: []string{"calls://checkout-charge"},
	}}, RunOptions{
		Retriever: "context", TopK: 10, RavelVersion: "test", DatasetRevision: "fixture",
		Retrieval: query.RetrieveOptions{Relations: []graph.EdgeKind{graph.EdgeCalls}, TokenBudget: 256},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Version != 3 || report.Retriever != "context" || report.IndexBuildMicros < 0 {
		t.Fatalf("report metadata = %#v", report)
	}
	if report.Overall.MeanRecall != 1 || report.Overall.MeanEvidenceRecall != 1 || report.Overall.MeanEstimatedTokens <= 0 || report.Overall.MeanRecallPer1KTokens <= 0 || report.Overall.MeanEvidencePer1KTokens <= 0 {
		t.Fatalf("metrics = %#v", report.Overall)
	}
	if len(report.Results[0].RetrievedEdgeIDs) != 1 {
		t.Fatalf("result = %#v", report.Results[0])
	}
}

func TestGraphHashIsStableAndChangesWithGraphContent(t *testing.T) {
	first := graph.Graph{Version: "0.1", Root: "/first/checkout", GeneratedAt: time.Unix(1, 0), Nodes: []graph.Node{{ID: "one", Name: "One"}}}
	hashA, err := GraphHash(first)
	if err != nil {
		t.Fatal(err)
	}
	relocated := first
	relocated.Root = "/another/checkout"
	relocated.GeneratedAt = time.Unix(2, 0)
	hashB, err := GraphHash(relocated)
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.Nodes = []graph.Node{{ID: "two", Name: "Two"}}
	hashC, err := GraphHash(second)
	if err != nil {
		t.Fatal(err)
	}
	if hashA == "" || hashA != hashB || hashA == hashC {
		t.Fatalf("graph hashes = %q, %q, %q", hashA, hashB, hashC)
	}
}

func TestEvidenceOnlyCaseUsesEvidenceForPrimaryEfficiencyMetrics(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "function://checkout", Kind: graph.NodeFunction, Name: "Checkout"},
			{ID: "function://charge", Kind: graph.NodeFunction, Name: "ChargeCard"},
		},
		Edges: []graph.Edge{{ID: "calls://checkout-charge", Kind: graph.EdgeCalls, From: "function://checkout", To: "function://charge"}},
	}
	report, err := RunWithOptions(g, []Case{{
		ID: "evidence-only", Dataset: "repository-questions", Question: "checkout charge",
		ExpectedEvidence: []string{"calls://checkout-charge"},
	}}, RunOptions{Retriever: "context", TopK: 10, Retrieval: query.RetrieveOptions{Relations: []graph.EdgeKind{graph.EdgeCalls}, TokenBudget: 256}})
	if err != nil {
		t.Fatal(err)
	}
	result := report.Results[0]
	if result.EvidenceRecall != 1 || result.Recall != 1 || result.ReciprocalRank <= 0 || result.RecallPer1KTokens <= 0 || result.EvidencePer1KTokens <= 0 {
		t.Fatalf("evidence-only metrics = %#v", result)
	}
}

func TestDatasetHashIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cases.jsonl")
	if err := os.WriteFile(path, []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := DatasetHash(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DatasetHash(path)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second {
		t.Fatalf("hashes = %q, %q", first, second)
	}
}

func TestLoadJSONLWithHashMatchesParsedBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cases.jsonl")
	data := []byte(`{"id":"one","dataset":"repo","question":"where","expectedNodeIds":["node://one"]}` + "\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cases, got, err := LoadJSONLWithHash(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := DatasetHash(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 || got == "" || got != want {
		t.Fatalf("cases=%#v hash=%q want=%q", cases, got, want)
	}
}

func TestLoadJSONLRejectsAmbiguousOrMisspelledCases(t *testing.T) {
	valid := `{"id":"one","dataset":"repo","question":"where","expectedNodeIds":["node://one"],"expectedKeyFacts":["One is the entry point."]}`
	for name, content := range map[string]string{
		"unknown-field":      `{"id":"one","dataset":"repo","question":"where","expectedNodeIds":["node://one"],"expectedKeyFact":["typo"]}`,
		"wrong-field-case":   `{"ID":"one","dataset":"repo","question":"where","expectedNodeIds":["node://one"]}`,
		"duplicate-id":       valid + "\n" + valid,
		"duplicate-node":     `{"id":"one","dataset":"repo","question":"where","expectedNodeIds":["node://one","node://one"]}`,
		"duplicate-evidence": `{"id":"one","dataset":"repo","question":"where","expectedEvidence":["edge://one","edge://one"]}`,
		"duplicate-fact":     `{"id":"one","dataset":"repo","question":"where","expectedNodeIds":["node://one"],"expectedKeyFacts":["fact","fact"]}`,
		"duplicate-field":    `{"id":"one","id":"two","dataset":"repo","question":"where","expectedNodeIds":["node://one"]}`,
		"empty-value":        `{"id":"one","dataset":"repo","question":"where","expectedNodeIds":[""]}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cases.jsonl")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadJSONL(path); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	path := filepath.Join(t.TempDir(), "cases.jsonl")
	if err := os.WriteFile(path, []byte(valid), 0o644); err != nil {
		t.Fatal(err)
	}
	cases, err := LoadJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 || len(cases[0].ExpectedKeyFacts) != 1 {
		t.Fatalf("cases = %#v", cases)
	}
}
