package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/12ya/reporavel/internal/graph"
)

func TestServeContentLengthLifecycleAndAllTools(t *testing.T) {
	outDir := t.TempDir()
	g := graph.Graph{
		Version: "test",
		Nodes: []graph.Node{
			{ID: "function://checkout", Kind: graph.NodeFunction, Name: "Checkout", Path: "checkout.go", StartLine: 10, Meta: map[string]string{"confidence": "extracted"}},
			{ID: "function://charge", Kind: graph.NodeFunction, Name: "ChargeCard", Path: "payments.go", StartLine: 20, Meta: map[string]string{"confidence": "extracted"}},
		},
		Edges: []graph.Edge{{ID: "calls://checkout-charge", Kind: graph.EdgeCalls, From: "function://checkout", To: "function://charge", Meta: map[string]string{"confidence": "inferred", "evidence": "checkout.go:12", "resolved": "true"}}},
	}
	writeTestGraph(t, filepath.Join(outDir, "graph.json"), g)

	requests := [][]byte{
		marshalRequest(t, json.RawMessage("900719925474099312345"), "initialize", map[string]any{
			"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
			"clientInfo": map[string]any{"name": "test-client", "version": "1.0"},
		}),
		marshalNotification(t, "notifications/initialized", nil),
		marshalRequest(t, 2, "ping", nil),
		marshalRequest(t, 3, "tools/list", map[string]any{}),
		marshalToolCall(t, 4, "query", map[string]any{"query": "Checkout", "limit": 5}),
		marshalToolCall(t, 5, "context", map[string]any{"question": "checkout calls", "relations": []string{"calls"}, "token_budget": 256}),
		marshalToolCall(t, 6, "explain", map[string]any{"target": "Checkout", "token_budget": 256}),
		marshalToolCall(t, 7, "path", map[string]any{"from": "Checkout", "to": "ChargeCard", "token_budget": 256}),
		marshalToolCall(t, 8, "affected", map[string]any{"target": "ChargeCard", "relations": []string{"calls"}, "token_budget": 256}),
	}
	var input bytes.Buffer
	for _, request := range requests {
		input.Write(contentLengthFrame(request))
	}
	var output bytes.Buffer
	if err := Serve(context.Background(), &input, &output, Options{OutDir: outDir, Version: "v-test"}); err != nil {
		t.Fatal(err)
	}

	responses := decodeFramedResponses(t, output.Bytes())
	if len(responses) != 8 {
		t.Fatalf("responses = %d, want 8 (notification must be silent)\n%s", len(responses), output.String())
	}
	if got := string(responses[0].ID); got != "900719925474099312345" {
		t.Fatalf("large request id changed: %s", got)
	}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools struct {
				ListChanged bool `json:"listChanged"`
			} `json:"tools"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	decodeResult(t, responses[0], &initialized)
	if initialized.ProtocolVersion != "2025-06-18" || initialized.ServerInfo.Name != "ravel" || initialized.ServerInfo.Version != "v-test" || initialized.Capabilities.Tools.ListChanged {
		t.Fatalf("initialize result = %#v", initialized)
	}
	if string(responses[1].Result) != "{}" {
		t.Fatalf("ping result = %s", responses[1].Result)
	}

	var listed struct {
		Tools []toolDefinition `json:"tools"`
	}
	decodeResult(t, responses[2], &listed)
	var names []string
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		if tool.InputSchema["additionalProperties"] != false {
			t.Fatalf("tool %s schema allows unknown arguments: %#v", tool.Name, tool.InputSchema)
		}
	}
	if want := []string{"query", "context", "explain", "path", "affected"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("tool names = %v, want %v", names, want)
	}

	texts := make([]string, 0, 5)
	for _, response := range responses[3:] {
		var result callToolResult
		decodeResult(t, response, &result)
		if result.IsError || len(result.Content) != 1 || result.Content[0].Type != "text" {
			t.Fatalf("tool result = %#v", result)
		}
		texts = append(texts, result.Content[0].Text)
	}
	checks := []struct {
		index int
		want  []string
	}{
		{0, []string{"Checkout"}},
		{1, []string{"RAVEL_CONTEXT", "Checkout", "ChargeCard"}},
		{2, []string{"Checkout", "Outgoing relationships", "function://charge", "confidence=inferred", `evidence="checkout.go:12"`, "resolved=true"}},
		{3, []string{"Checkout", "ChargeCard", "->"}},
		{4, []string{"RAVEL_AFFECTED", "Checkout", "ChargeCard"}},
	}
	for _, check := range checks {
		for _, fragment := range check.want {
			if !strings.Contains(texts[check.index], fragment) {
				t.Fatalf("tool text %d missing %q:\n%s", check.index, fragment, texts[check.index])
			}
		}
	}
	if strings.Contains(output.String(), "RepoRavel") {
		t.Fatalf("stdout contains non-protocol logging: %q", output.String())
	}
}

func TestServeNewlineProtocolErrorsAndStructuredToolErrors(t *testing.T) {
	requests := [][]byte{
		marshalRequest(t, 1, "ping", nil),
		marshalRequest(t, 2, "tools/list", nil),
		[]byte(`{bad}`),
		marshalRequest(t, 3, "initialize", map[string]any{
			"protocolVersion": "unsupported", "capabilities": map[string]any{},
			"clientInfo": map[string]any{"name": "test-client", "version": "1.0"},
		}),
		marshalRequest(t, 4, "unknown/method", nil),
		marshalToolCall(t, 5, "not-a-tool", map[string]any{}),
		marshalToolCall(t, 6, "query", map[string]any{"query": "x", "limit": 0}),
		marshalToolCall(t, 7, "query", map[string]any{"query": "x"}),
		marshalNotification(t, "unknown/notification", nil),
	}
	var input bytes.Buffer
	input.WriteString("\n")
	for _, request := range requests {
		input.Write(request)
		input.WriteByte('\n')
	}
	var output bytes.Buffer
	if err := Serve(context.Background(), &input, &output, Options{OutDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "Content-Length:") {
		t.Fatalf("newline session changed framing: %q", output.String())
	}
	responses := decodeNewlineResponses(t, output.Bytes())
	if len(responses) != 8 {
		t.Fatalf("responses = %d, want 8", len(responses))
	}
	if responses[0].Error != nil || string(responses[0].Result) != "{}" {
		t.Fatalf("pre-initialize ping = %#v", responses[0])
	}
	wantCodes := map[int]int{1: -32002, 2: -32700, 4: -32601, 5: -32602}
	for index, code := range wantCodes {
		if responses[index].Error == nil || responses[index].Error.Code != code {
			t.Fatalf("response %d error = %#v, want code %d", index, responses[index].Error, code)
		}
	}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	decodeResult(t, responses[3], &initialized)
	if initialized.ProtocolVersion != latestProtocolVersion {
		t.Fatalf("negotiated version = %q", initialized.ProtocolVersion)
	}
	for _, index := range []int{6, 7} {
		var result callToolResult
		decodeResult(t, responses[index], &result)
		if !result.IsError || result.StructuredContent == nil || len(result.Content) != 1 || !strings.HasPrefix(result.Content[0].Text, "ERROR\t") {
			t.Fatalf("structured tool error %d = %#v", index, result)
		}
	}
}

func TestServeGracefulEOF(t *testing.T) {
	var output bytes.Buffer
	if err := Serve(context.Background(), bytes.NewReader(nil), &output, Options{}); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("EOF output = %q", output.String())
	}
}

type wireResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

func marshalRequest(t *testing.T, id any, method string, params any) []byte {
	t.Helper()
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		request["params"] = params
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func marshalNotification(t *testing.T, method string, params any) []byte {
	t.Helper()
	request := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		request["params"] = params
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func marshalToolCall(t *testing.T, id any, name string, arguments any) []byte {
	t.Helper()
	return marshalRequest(t, id, "tools/call", map[string]any{"name": name, "arguments": arguments})
}

func decodeFramedResponses(t *testing.T, data []byte) []wireResponse {
	t.Helper()
	transport := newTransport(bytes.NewReader(data), io.Discard, defaultMaxMessageBytes)
	var responses []wireResponse
	for {
		body, err := transport.read()
		if err == io.EOF {
			return responses
		}
		if err != nil {
			t.Fatalf("read response frame: %v\n%s", err, data)
		}
		responses = append(responses, decodeWireResponse(t, body))
	}
}

func decodeNewlineResponses(t *testing.T, data []byte) []wireResponse {
	t.Helper()
	var responses []wireResponse
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		responses = append(responses, decodeWireResponse(t, line))
	}
	return responses
}

func decodeWireResponse(t *testing.T, data []byte) wireResponse {
	t.Helper()
	var response wireResponse
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("decode response: %v\n%s", err, data)
	}
	if response.JSONRPC != "2.0" {
		t.Fatalf("jsonrpc = %q", response.JSONRPC)
	}
	return response
}

func decodeResult(t *testing.T, response wireResponse, target any) {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("response error = %#v", response.Error)
	}
	if err := json.Unmarshal(response.Result, target); err != nil {
		t.Fatalf("decode result: %v\n%s", err, response.Result)
	}
}
