package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const latestProtocolVersion = "2025-11-25"

var (
	supportedProtocolVersions = map[string]bool{
		"2024-11-05": true,
		"2025-03-26": true,
		"2025-06-18": true,
		"2025-11-25": true,
	}
	integerID = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
)

// Options configures the dependency-free stdio MCP server.
type Options struct {
	OutDir          string
	Version         string
	MaxMessageBytes int
}

type server struct {
	graphs      *graphCache
	version     string
	initialized bool
	ready       bool
}

type request struct {
	id      json.RawMessage
	hasID   bool
	method  string
	params  json.RawMessage
	jsonrpc string
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Serve runs until stdin reaches EOF, the context is cancelled between
// messages, or a fatal framing/write error occurs. It never writes diagnostics
// or logs to the protocol output.
func Serve(ctx context.Context, in io.Reader, out io.Writer, options Options) error {
	if in == nil || out == nil {
		return errors.New("MCP stdin and stdout must not be nil")
	}
	outDir := strings.TrimSpace(options.OutDir)
	if outDir == "" {
		outDir = ".reporavel"
	}
	version := strings.TrimSpace(options.Version)
	if version == "" {
		version = "dev"
	}
	s := &server{graphs: newGraphCache(outDir), version: version}
	transport := newTransport(in, out, options.MaxMessageBytes)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		body, err := transport.read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			_ = writeResponse(transport, errorResponse(nil, -32700, "Parse error", map[string]any{"detail": oneLine(err.Error())}))
			return fmt.Errorf("read MCP frame: %w", err)
		}
		request, requestError := decodeRequest(body)
		if requestError != nil {
			if err := writeResponse(transport, requestError); err != nil {
				return err
			}
			continue
		}
		result := s.handle(request)
		if result == nil {
			continue
		}
		if err := writeResponse(transport, result); err != nil {
			return err
		}
	}
}

func writeResponse(transport *transport, value *response) error {
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode MCP response: %w", err)
	}
	if err := transport.write(body); err != nil {
		return fmt.Errorf("write MCP response: %w", err)
	}
	return nil
}

func decodeRequest(body []byte) (*request, *response) {
	if !json.Valid(body) {
		return nil, errorResponse(nil, -32700, "Parse error", nil)
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errorResponse(nil, -32600, "Invalid Request", nil)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil || fields == nil {
		return nil, errorResponse(nil, -32600, "Invalid Request", nil)
	}
	var protocol string
	if err := json.Unmarshal(fields["jsonrpc"], &protocol); err != nil || protocol != "2.0" {
		return nil, errorResponse(nil, -32600, "Invalid Request", map[string]any{"detail": "jsonrpc must equal 2.0"})
	}
	var method string
	if err := json.Unmarshal(fields["method"], &method); err != nil || strings.TrimSpace(method) == "" {
		return nil, errorResponse(nil, -32600, "Invalid Request", map[string]any{"detail": "method must be a non-empty string"})
	}
	id, hasID := fields["id"]
	if hasID && !validRequestID(id) {
		return nil, errorResponse(nil, -32600, "Invalid Request", map[string]any{"detail": "id must be a string or integer"})
	}
	return &request{
		id: id, hasID: hasID, method: method, params: fields["params"], jsonrpc: protocol,
	}, nil
}

func validRequestID(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	if trimmed[0] == '"' {
		var value string
		return json.Unmarshal(trimmed, &value) == nil
	}
	return integerID.Match(trimmed)
}

func (s *server) handle(request *request) *response {
	if !request.hasID {
		s.handleNotification(request)
		return nil
	}
	switch request.method {
	case "ping":
		return successResponse(request.id, map[string]any{})
	case "initialize":
		return s.initialize(request)
	}
	if !s.initialized {
		return errorResponse(request.id, -32002, "Server not initialized", nil)
	}
	switch request.method {
	case "tools/list":
		return s.listTools(request)
	case "tools/call":
		return s.invokeTool(request)
	default:
		return errorResponse(request.id, -32601, "Method not found", map[string]any{"method": request.method})
	}
}

func (s *server) handleNotification(request *request) {
	switch request.method {
	case "notifications/initialized":
		if s.initialized {
			s.ready = true
		}
	case "notifications/cancelled":
		// Calls execute synchronously and complete before the next frame is read.
	}
}

func (s *server) initialize(request *request) *response {
	if s.initialized {
		return errorResponse(request.id, -32600, "Server already initialized", nil)
	}
	var params struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ClientInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	}
	if len(request.params) == 0 || json.Unmarshal(request.params, &params) != nil || strings.TrimSpace(params.ProtocolVersion) == "" || params.Capabilities == nil || strings.TrimSpace(params.ClientInfo.Name) == "" || strings.TrimSpace(params.ClientInfo.Version) == "" {
		return errorResponse(request.id, -32602, "Invalid params", map[string]any{"detail": "initialize requires protocolVersion, capabilities, and clientInfo name/version"})
	}
	protocolVersion := latestProtocolVersion
	if supportedProtocolVersions[params.ProtocolVersion] {
		protocolVersion = params.ProtocolVersion
	}
	s.initialized = true
	return successResponse(request.id, map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{
			"name": "ravel", "title": "Ravel Code Knowledge Graph", "version": s.version,
		},
		"instructions": "Use context for connected bounded evidence, query for exact lookup, and affected for reverse dependency impact. Unresolved graph relationships remain explicit.",
	})
}

func (s *server) listTools(request *request) *response {
	if len(request.params) > 0 {
		params, err := decodeArguments(request.params, "cursor", "_meta")
		if err != nil {
			return errorResponse(request.id, -32602, "Invalid params", map[string]any{"detail": oneLine(err.Error())})
		}
		if raw, ok := params["cursor"]; ok {
			var cursor string
			if json.Unmarshal(raw, &cursor) != nil || cursor != "" {
				return errorResponse(request.id, -32602, "Invalid params", map[string]any{"detail": "cursor pagination is not supported"})
			}
		}
	}
	return successResponse(request.id, map[string]any{"tools": toolDefinitions()})
}

func (s *server) invokeTool(request *request) *response {
	params, err := decodeArguments(request.params, "name", "arguments", "_meta")
	if err != nil {
		return errorResponse(request.id, -32602, "Invalid params", map[string]any{"detail": oneLine(err.Error())})
	}
	name, err := requiredString(params, "name")
	if err != nil {
		return errorResponse(request.id, -32602, "Invalid params", map[string]any{"detail": oneLine(err.Error())})
	}
	if !knownTool(name) {
		return errorResponse(request.id, -32602, "Unknown tool", map[string]any{"tool": name, "available": toolNames()})
	}
	return successResponse(request.id, s.callTool(name, params["arguments"]))
}

func knownTool(name string) bool {
	for _, definition := range toolDefinitions() {
		if definition.Name == name {
			return true
		}
	}
	return false
}

func successResponse(id json.RawMessage, result any) *response {
	return &response{JSONRPC: "2.0", ID: responseID(id), Result: result}
}

func errorResponse(id json.RawMessage, code int, message string, data any) *response {
	return &response{
		JSONRPC: "2.0", ID: responseID(id),
		Error: &rpcError{Code: code, Message: message, Data: data},
	}
}

func responseID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return append(json.RawMessage(nil), id...)
}
