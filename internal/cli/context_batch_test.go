package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/query"
	"github.com/12vault/ravel/internal/store"
)

func TestContextBatchMatchesOneShotContext(t *testing.T) {
	outDir := writeContextBatchTestGraph(t)
	questions := []string{"checkout calls", "charge card"}
	requests := bytes.Buffer{}
	for index, question := range questions {
		if err := json.NewEncoder(&requests).Encode(contextBatchRequest{ID: string(rune('a' + index)), Question: question}); err != nil {
			t.Fatal(err)
		}
	}

	var stdout, stderr bytes.Buffer
	args := []string{"context-batch", "--out", outDir, "--relations", "calls", "--branch-fanout", "7", "--token-budget", "256", "--candidate-shortlist"}
	if err := ExecuteIO(context.Background(), args, &requests, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	decoder := json.NewDecoder(&stdout)
	var ready contextBatchReady
	if err := decoder.Decode(&ready); err != nil {
		t.Fatal(err)
	}
	if ready.Type != "ready" || ready.Version != 1 || ready.GraphNodes != 2 || ready.GraphEdges != 1 {
		t.Fatalf("ready = %#v", ready)
	}
	for index, question := range questions {
		var response contextBatchResponse
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response.Type != "result" || response.ID != string(rune('a'+index)) || response.Retrieval == nil {
			t.Fatalf("response = %#v", response)
		}
		var oneShot bytes.Buffer
		oneShotArgs := []string{"context", "--json", "--out", outDir, "--relations", "calls", "--branch-fanout", "7", "--token-budget", "256", "--candidate-shortlist", question}
		if err := Execute(context.Background(), oneShotArgs, &oneShot, io.Discard); err != nil {
			t.Fatal(err)
		}
		var expected query.Retrieval
		if err := json.Unmarshal(oneShot.Bytes(), &expected); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(*response.Retrieval, expected) {
			t.Fatalf("batch retrieval differs from one-shot\nbatch: %#v\none-shot: %#v", *response.Retrieval, expected)
		}
	}

	var warm bytes.Buffer
	if err := ExecuteIO(context.Background(), args, strings.NewReader(""), &warm, io.Discard); err != nil {
		t.Fatal(err)
	}
	var warmReady contextBatchReady
	if err := json.NewDecoder(&warm).Decode(&warmReady); err != nil {
		t.Fatal(err)
	}
	if !warmReady.IndexCacheHit || warmReady.GraphNodes != ready.GraphNodes || warmReady.GraphEdges != ready.GraphEdges {
		t.Fatalf("warm ready = %#v, want cached index with stable counts", warmReady)
	}
}

func TestContextBatchKeepsFixedSnapshotAndContinuesAfterBadRow(t *testing.T) {
	outDir := writeContextBatchTestGraph(t)
	input := strings.NewReader("not-json\n{\"id\":\"missing\",\"question\":\"\"}\n{\"id\":\"ok\",\"question\":\"checkout\"}\n")
	reader := &firstReadCallback{Reader: input, callback: func() {
		replacement := graph.Graph{Nodes: []graph.Node{{ID: "replacement", Kind: graph.NodeFunction, Name: "Other"}}}
		if err := store.WriteJSON(filepath.Join(outDir, "graph.json"), replacement); err != nil {
			t.Fatal(err)
		}
	}}

	var stdout bytes.Buffer
	if err := ExecuteIO(context.Background(), []string{"context-batch", "--out", outDir}, reader, &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&stdout)
	var ready contextBatchReady
	if err := decoder.Decode(&ready); err != nil {
		t.Fatal(err)
	}
	var malformed, missing, valid contextBatchResponse
	for _, response := range []*contextBatchResponse{&malformed, &missing, &valid} {
		if err := decoder.Decode(response); err != nil {
			t.Fatal(err)
		}
	}
	if malformed.Type != "error" || malformed.Line != 1 || missing.Type != "error" || missing.ID != "missing" {
		t.Fatalf("errors = %#v %#v", malformed, missing)
	}
	if valid.Type != "result" || valid.Retrieval == nil || len(valid.Retrieval.Nodes) == 0 || valid.Retrieval.Nodes[0].Name != "Checkout" {
		t.Fatalf("valid response did not use startup snapshot: %#v", valid)
	}
}

func TestContextBatchTracesRequestedNodes(t *testing.T) {
	outDir := writeContextBatchTestGraph(t)
	requests := bytes.Buffer{}
	if err := json.NewEncoder(&requests).Encode(contextBatchRequest{
		ID: "trace", Question: "checkout calls", TraceNodeIDs: []string{"function://charge", "missing"},
	}); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := ExecuteIO(context.Background(), []string{"context-batch", "--out", outDir, "--relations", "calls"}, &requests, &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&stdout)
	var ready contextBatchReady
	var response contextBatchResponse
	if err := decoder.Decode(&ready); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Retrieval == nil || len(response.Retrieval.Stats.TraceNodes) != 2 {
		t.Fatalf("response trace = %#v", response)
	}
	if got := response.Retrieval.Stats.TraceNodes[0]; !got.Indexed || got.WalkRank == 0 {
		t.Fatalf("charge trace = %#v", got)
	}
	if got := response.Retrieval.Stats.TraceNodes[1]; got.DroppedReason != "not_indexed" {
		t.Fatalf("missing trace = %#v", got)
	}
}

func TestContextBatchHelpAndArgumentValidation(t *testing.T) {
	var stdout bytes.Buffer
	if err := Execute(context.Background(), []string{"context-batch", "--help"}, &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Usage: ravel context-batch") || !strings.Contains(stdout.String(), "--candidate-shortlist") {
		t.Fatalf("help = %q", stdout.String())
	}
	if err := ExecuteIO(context.Background(), []string{"context-batch", "question"}, strings.NewReader(""), io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "does not accept positional") {
		t.Fatalf("positional argument error = %v", err)
	}
}

func writeContextBatchTestGraph(t *testing.T) string {
	t.Helper()
	outDir := t.TempDir()
	g := graph.Graph{
		Nodes: []graph.Node{
			{ID: "function://checkout", Kind: graph.NodeFunction, Name: "Checkout", Path: "checkout.go", StartLine: 10},
			{ID: "function://charge", Kind: graph.NodeFunction, Name: "ChargeCard", Path: "payments.go", StartLine: 20},
		},
		Edges: []graph.Edge{{ID: "calls://checkout-charge", Kind: graph.EdgeCalls, From: "function://checkout", To: "function://charge"}},
	}
	if err := store.WriteJSON(filepath.Join(outDir, "graph.json"), g); err != nil {
		t.Fatal(err)
	}
	return outDir
}

type firstReadCallback struct {
	io.Reader
	callback func()
	called   bool
}

func (reader *firstReadCallback) Read(buffer []byte) (int, error) {
	if !reader.called {
		reader.called = true
		reader.callback()
	}
	return reader.Reader.Read(buffer)
}
