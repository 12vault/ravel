package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/evaluation"
	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/store"
)

func TestExecuteBenchmarkAttachesExternalAnswerLedger(t *testing.T) {
	root := t.TempDir()
	graphDir := filepath.Join(root, "graph")
	if err := os.MkdirAll(graphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	g := graph.Graph{Nodes: []graph.Node{{ID: "function://checkout", Kind: graph.NodeFunction, Name: "Checkout"}}}
	if err := store.WriteJSON(filepath.Join(graphDir, "graph.json"), g); err != nil {
		t.Fatal(err)
	}
	dataset := filepath.Join(root, "cases.jsonl")
	datasetData := `{"id":"q1","dataset":"repository-questions","question":"checkout","expectedNodeIds":["function://checkout"],"expectedKeyFacts":["Checkout is the entry point."]}` + "\n"
	if err := os.WriteFile(dataset, []byte(datasetData), 0o644); err != nil {
		t.Fatal(err)
	}
	answers := filepath.Join(root, "answers.jsonl")
	answerData := `{"id":"q1","correct":true,"keyFactsFound":["Checkout is the entry point."],"inputTokens":10,"outputTokens":3,"toolTokens":7,"costUsd":0.002,"model":"fixture","judge":"human","runId":"run-1"}` + "\n"
	if err := os.WriteFile(answers, []byte(answerData), 0o644); err != nil {
		t.Fatal(err)
	}
	wantHash, err := evaluation.DatasetHash(answers)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err = Execute(context.Background(), []string{"benchmark", "--graph", graphDir, "--dataset", dataset, "--answers", answers}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	var report evaluation.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("benchmark JSON: %v\n%s", err, stdout.String())
	}
	if report.Version != 3 || report.AnswerSHA256 != wantHash || report.AnswerQuality == nil || report.AnswerQuality.Accuracy != 1 || report.AnswerQuality.MeanKeyFactCoverage != 1 || report.AnswerQuality.TotalAgentTokens != 20 || report.AnswerQuality.TotalCostUSD != 0.002 {
		t.Fatalf("answer report = %#v", report)
	}
	if len(report.Results) != 1 || report.Results[0].Answer == nil || report.Results[0].Answer.RunID != "run-1" {
		t.Fatalf("case answer = %#v", report.Results)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestExecuteBenchmarkQualityGateWritesReportAndFailsThresholds(t *testing.T) {
	root := t.TempDir()
	graphDir := filepath.Join(root, "graph")
	if err := os.MkdirAll(graphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	g := graph.Graph{Nodes: []graph.Node{{ID: "function://checkout", Kind: graph.NodeFunction, Name: "Checkout"}}}
	if err := store.WriteJSON(filepath.Join(graphDir, "graph.json"), g); err != nil {
		t.Fatal(err)
	}
	dataset := filepath.Join(root, "cases.jsonl")
	if err := os.WriteFile(dataset, []byte(`{"id":"q1","dataset":"repository-questions","question":"checkout","expectedNodeIds":["function://checkout"]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(root, "gate.json")
	if err := os.WriteFile(gate, []byte(`{"version":1,"minimumCases":2,"minimumMeanRecall":1,"requireFreshExpectations":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "result.json")

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{"benchmark", "--graph", graphDir, "--dataset", dataset, "--gate", gate, "--out", out}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "retrieval quality gate failed") {
		t.Fatalf("error = %v", err)
	}
	data, readErr := os.ReadFile(out)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var report evaluation.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.QualityGate == nil || report.QualityGate.Passed || len(report.QualityGate.Failures) != 1 {
		t.Fatalf("quality gate = %#v", report.QualityGate)
	}
}

func TestExecuteBenchmarkQualityGateRejectsStaleExpectations(t *testing.T) {
	root := t.TempDir()
	graphDir := filepath.Join(root, "graph")
	if err := os.MkdirAll(graphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteJSON(filepath.Join(graphDir, "graph.json"), graph.Graph{}); err != nil {
		t.Fatal(err)
	}
	dataset := filepath.Join(root, "cases.jsonl")
	if err := os.WriteFile(dataset, []byte(`{"id":"q1","dataset":"repository-questions","question":"checkout","expectedNodeIds":["function://missing"]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(root, "gate.json")
	if err := os.WriteFile(gate, []byte(`{"version":1,"minimumCases":1,"requireFreshExpectations":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{"benchmark", "--graph", graphDir, "--dataset", dataset, "--gate", gate}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "stale benchmark expectations") {
		t.Fatalf("error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
