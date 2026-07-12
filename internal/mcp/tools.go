package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/12ya/reporavel/internal/graph"
	"github.com/12ya/reporavel/internal/query"
)

const (
	defaultToolTokenBudget = 2_000
	minimumTokenBudget     = 128
	maximumTokenBudget     = 100_000
	maximumToolTextBytes   = 4_096
)

type toolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callToolResult struct {
	Content           []textContent `json:"content"`
	StructuredContent any           `json:"structuredContent,omitempty"`
	IsError           bool          `json:"isError"`
}

type toolExecutionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func toolDefinitions() []toolDefinition {
	readOnly := map[string]any{
		"readOnlyHint":    true,
		"destructiveHint": false,
		"idempotentHint":  true,
		"openWorldHint":   false,
	}
	return []toolDefinition{
		{
			Name:        "query",
			Description: "Find a compact ranked list of graph nodes matching text.",
			InputSchema: objectSchema(map[string]any{
				"query":        stringSchema("Search text", 1, maximumToolTextBytes),
				"limit":        integerSchema("Maximum results", 1, 100, 25),
				"token_budget": integerSchema("Approximate output-token budget", minimumTokenBudget, maximumTokenBudget, defaultToolTokenBudget),
			}, "query"),
			Annotations: readOnly,
		},
		{
			Name:        "context",
			Description: "Retrieve a connected, confidence-preserving, token-bounded graph context.",
			InputSchema: objectSchema(map[string]any{
				"question":             stringSchema("Natural-language relationship question", 1, maximumToolTextBytes),
				"traversal":            enumSchema("Traversal strategy", "bfs", "dfs"),
				"direction":            enumSchema("Relationship direction", "both", "out", "in"),
				"relations":            arrayStringSchema("Exact edge-kind filters", 32, 80),
				"infer_relations":      booleanSchema("Infer edge-kind filters from the question", true),
				"seed_limit":           integerSchema("Maximum lexical seeds", 1, 20, 3),
				"max_depth":            integerSchema("Traversal depth", 1, 8, 2),
				"max_nodes":            integerSchema("Hard node limit", 1, 10_000, 100),
				"branch_fanout":        integerSchema("0 for automatic; positive values override neighbors expanded per node", 0, 10_000, 0),
				"hub_degree_threshold": integerMinimumSchema("Hub suppression threshold; -1 disables", -1, 0),
				"token_budget":         integerSchema("Approximate output-token budget", minimumTokenBudget, maximumTokenBudget, defaultToolTokenBudget),
			}, "question"),
			Annotations: readOnly,
		},
		{
			Name:        "explain",
			Description: "Explain one file, symbol, or node and its immediate relationships.",
			InputSchema: objectSchema(map[string]any{
				"target":        stringSchema("File path, symbol name, or exact node ID", 1, maximumToolTextBytes),
				"max_relations": integerSchema("Maximum incoming and outgoing relationships", 1, 1_000, 100),
				"token_budget":  integerSchema("Approximate output-token budget", minimumTokenBudget, maximumTokenBudget, defaultToolTokenBudget),
			}, "target"),
			Annotations: readOnly,
		},
		{
			Name:        "path",
			Description: "Find the shortest directed graph path; if only undirected connectivity exists, label every fallback hop and original edge orientation explicitly.",
			InputSchema: objectSchema(map[string]any{
				"from":         stringSchema("Starting file, symbol, or node ID", 1, maximumToolTextBytes),
				"to":           stringSchema("Destination file, symbol, or node ID", 1, maximumToolTextBytes),
				"token_budget": integerSchema("Approximate output-token budget", minimumTokenBudget, maximumTokenBudget, defaultToolTokenBudget),
			}, "from", "to"),
			Annotations: readOnly,
		},
		{
			Name:        "affected",
			Description: "Find graph dependents likely affected by changing a target.",
			InputSchema: objectSchema(map[string]any{
				"target":               stringSchema("Changed file, symbol, or node ID", 1, maximumToolTextBytes),
				"relations":            arrayStringSchema("Optional exact edge-kind filters", 32, 80),
				"max_depth":            integerSchema("Impact traversal depth", 1, 8, 2),
				"max_nodes":            integerSchema("Hard node limit", 1, 10_000, 100),
				"branch_fanout":        integerSchema("0 for automatic; positive values override neighbors expanded per node", 0, 10_000, 0),
				"hub_degree_threshold": integerMinimumSchema("Hub suppression threshold; -1 disables", -1, 0),
				"token_budget":         integerSchema("Approximate output-token budget", minimumTokenBudget, maximumTokenBudget, defaultToolTokenBudget),
			}, "target"),
			Annotations: readOnly,
		},
	}
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringSchema(description string, minimum, maximum int) map[string]any {
	return map[string]any{"type": "string", "description": description, "minLength": minimum, "maxLength": maximum}
}

func integerSchema(description string, minimum, maximum, defaultValue int) map[string]any {
	return map[string]any{"type": "integer", "description": description, "minimum": minimum, "maximum": maximum, "default": defaultValue}
}

func integerMinimumSchema(description string, minimum, defaultValue int) map[string]any {
	return map[string]any{"type": "integer", "description": description, "minimum": minimum, "default": defaultValue}
}

func booleanSchema(description string, defaultValue bool) map[string]any {
	return map[string]any{"type": "boolean", "description": description, "default": defaultValue}
}

func enumSchema(description string, values ...string) map[string]any {
	items := make([]any, len(values))
	for i, value := range values {
		items[i] = value
	}
	return map[string]any{"type": "string", "description": description, "enum": items, "default": values[0]}
}

func arrayStringSchema(description string, maxItems, maxLength int) map[string]any {
	return map[string]any{
		"type": "array", "description": description, "maxItems": maxItems, "uniqueItems": true,
		"items": map[string]any{"type": "string", "minLength": 1, "maxLength": maxLength},
	}
}

func (s *server) callTool(name string, raw json.RawMessage) callToolResult {
	var (
		text string
		err  *toolExecutionError
	)
	switch name {
	case "query":
		text, err = s.queryTool(raw)
	case "context":
		text, err = s.contextTool(raw)
	case "explain":
		text, err = s.explainTool(raw)
	case "path":
		text, err = s.pathTool(raw)
	case "affected":
		text, err = s.affectedTool(raw)
	default:
		return toolFailure("unknown_tool", fmt.Sprintf("unknown tool %q", name))
	}
	if err != nil {
		return toolFailure(err.Code, err.Message)
	}
	return callToolResult{Content: []textContent{{Type: "text", Text: text}}, IsError: false}
}

func (s *server) queryTool(raw json.RawMessage) (string, *toolExecutionError) {
	args, err := decodeArguments(raw, "query", "limit", "token_budget")
	if err != nil {
		return "", invalidArguments(err)
	}
	text, err := requiredString(args, "query")
	if err != nil {
		return "", invalidArguments(err)
	}
	limit, err := optionalInt(args, "limit", 25, 1, 100)
	if err != nil {
		return "", invalidArguments(err)
	}
	budget, err := toolTokenBudget(args)
	if err != nil {
		return "", invalidArguments(err)
	}
	snapshot, loadErr := s.graphs.snapshot()
	if loadErr != nil {
		return "", graphUnavailable(loadErr)
	}
	var output bytes.Buffer
	if err := query.WriteSearch(&output, snapshot.index.Search(text, limit), false); err != nil {
		return "", internalToolError(err)
	}
	return boundText(output.String(), budget), nil
}

func (s *server) contextTool(raw json.RawMessage) (string, *toolExecutionError) {
	allowed := []string{"question", "traversal", "direction", "relations", "infer_relations", "seed_limit", "max_depth", "max_nodes", "branch_fanout", "hub_degree_threshold", "token_budget"}
	args, err := decodeArguments(raw, allowed...)
	if err != nil {
		return "", invalidArguments(err)
	}
	question, err := requiredString(args, "question")
	if err != nil {
		return "", invalidArguments(err)
	}
	options, budget, err := retrievalOptions(args, query.DirectionBoth, false)
	if err != nil {
		return "", invalidArguments(err)
	}
	snapshot, loadErr := s.graphs.snapshot()
	if loadErr != nil {
		return "", graphUnavailable(loadErr)
	}
	result, retrieveErr := snapshot.index.Retrieve(question, options)
	if retrieveErr != nil {
		return "", invalidArguments(retrieveErr)
	}
	var output bytes.Buffer
	if err := query.WriteRetrieval(&output, result, false); err != nil {
		return "", internalToolError(err)
	}
	return boundText(output.String(), budget), nil
}

func (s *server) explainTool(raw json.RawMessage) (string, *toolExecutionError) {
	args, err := decodeArguments(raw, "target", "max_relations", "token_budget")
	if err != nil {
		return "", invalidArguments(err)
	}
	target, err := requiredString(args, "target")
	if err != nil {
		return "", invalidArguments(err)
	}
	maxRelations, err := optionalInt(args, "max_relations", 100, 1, 1_000)
	if err != nil {
		return "", invalidArguments(err)
	}
	budget, err := toolTokenBudget(args)
	if err != nil {
		return "", invalidArguments(err)
	}
	snapshot, loadErr := s.graphs.snapshot()
	if loadErr != nil {
		return "", graphUnavailable(loadErr)
	}
	explanation, resolveErr := snapshot.index.ExplainResolved(target)
	if resolveErr != nil {
		return "", targetToolError(resolveErr)
	}
	limited, omitted := limitExplanation(explanation, maxRelations)
	var output bytes.Buffer
	if err := query.WriteExplanation(&output, limited, false); err != nil {
		return "", internalToolError(err)
	}
	if omitted > 0 {
		fmt.Fprintf(&output, "\nTRUNCATED\treason=max_relations\tomitted=%d\n", omitted)
	}
	return boundText(output.String(), budget), nil
}

func (s *server) pathTool(raw json.RawMessage) (string, *toolExecutionError) {
	args, err := decodeArguments(raw, "from", "to", "token_budget")
	if err != nil {
		return "", invalidArguments(err)
	}
	from, err := requiredString(args, "from")
	if err != nil {
		return "", invalidArguments(err)
	}
	to, err := requiredString(args, "to")
	if err != nil {
		return "", invalidArguments(err)
	}
	budget, err := toolTokenBudget(args)
	if err != nil {
		return "", invalidArguments(err)
	}
	snapshot, loadErr := s.graphs.snapshot()
	if loadErr != nil {
		return "", graphUnavailable(loadErr)
	}
	result, ok, pathErr := snapshot.index.ShortestPathResult(from, to)
	if pathErr != nil {
		return "", targetToolError(pathErr)
	}
	var output bytes.Buffer
	if !ok {
		output.WriteString("No path found.\n")
	} else if err := query.WritePathResult(&output, result, false); err != nil {
		return "", internalToolError(err)
	}
	return boundText(output.String(), budget), nil
}

func (s *server) affectedTool(raw json.RawMessage) (string, *toolExecutionError) {
	allowed := []string{"target", "relations", "max_depth", "max_nodes", "branch_fanout", "hub_degree_threshold", "token_budget"}
	args, err := decodeArguments(raw, allowed...)
	if err != nil {
		return "", invalidArguments(err)
	}
	target, err := requiredString(args, "target")
	if err != nil {
		return "", invalidArguments(err)
	}
	options, budget, err := retrievalOptions(args, query.DirectionIn, true)
	if err != nil {
		return "", invalidArguments(err)
	}
	snapshot, loadErr := s.graphs.snapshot()
	if loadErr != nil {
		return "", graphUnavailable(loadErr)
	}
	result, retrieveErr := snapshot.index.Affected(target, options)
	if retrieveErr != nil {
		if query.IsTargetError(retrieveErr, query.TargetAmbiguous) || query.IsTargetError(retrieveErr, query.TargetNotFound) {
			return "", targetToolError(retrieveErr)
		}
		return "", invalidArguments(retrieveErr)
	}
	var output bytes.Buffer
	if err := query.WriteAffected(&output, result, false); err != nil {
		return "", internalToolError(err)
	}
	return boundText(output.String(), budget), nil
}

func targetToolError(err error) *toolExecutionError {
	code := "target_not_found"
	if query.IsTargetError(err, query.TargetAmbiguous) {
		code = "target_ambiguous"
	}
	return &toolExecutionError{Code: code, Message: oneLine(err.Error())}
}

func retrievalOptions(args map[string]json.RawMessage, defaultDirection query.Direction, affected bool) (query.RetrieveOptions, int, error) {
	traversal, err := optionalEnum(args, "traversal", string(query.TraversalBFS), string(query.TraversalBFS), string(query.TraversalDFS))
	if err != nil && !affected {
		return query.RetrieveOptions{}, 0, err
	}
	direction, err := optionalEnum(args, "direction", string(defaultDirection), string(query.DirectionOut), string(query.DirectionIn), string(query.DirectionBoth))
	if err != nil && !affected {
		return query.RetrieveOptions{}, 0, err
	}
	seedLimit, err := optionalInt(args, "seed_limit", 3, 1, 20)
	if err != nil && !affected {
		return query.RetrieveOptions{}, 0, err
	}
	maxDepth, err := optionalInt(args, "max_depth", 2, 1, 8)
	if err != nil {
		return query.RetrieveOptions{}, 0, err
	}
	maxNodes, err := optionalInt(args, "max_nodes", 100, 1, 10_000)
	if err != nil {
		return query.RetrieveOptions{}, 0, err
	}
	branchFanout, err := optionalInt(args, "branch_fanout", 0, 0, 10_000)
	if err != nil {
		return query.RetrieveOptions{}, 0, err
	}
	hubThreshold, err := optionalIntMinimum(args, "hub_degree_threshold", 0, -1)
	if err != nil {
		return query.RetrieveOptions{}, 0, err
	}
	budget, err := toolTokenBudget(args)
	if err != nil {
		return query.RetrieveOptions{}, 0, err
	}
	relations, err := optionalRelations(args)
	if err != nil {
		return query.RetrieveOptions{}, 0, err
	}
	inferRelations, err := optionalBool(args, "infer_relations", true)
	if err != nil && !affected {
		return query.RetrieveOptions{}, 0, err
	}
	return query.RetrieveOptions{
		Traversal: query.Traversal(traversal), Direction: query.Direction(direction), Relations: relations,
		DisableRelationInference: !inferRelations, SeedLimit: seedLimit, MaxDepth: maxDepth,
		MaxNodes: maxNodes, BranchFanout: branchFanout, HubDegreeThreshold: hubThreshold, TokenBudget: budget,
	}, budget, nil
}

func decodeArguments(raw json.RawMessage, allowed ...string) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return map[string]json.RawMessage{}, nil
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("arguments must be a JSON object: %w", err)
	}
	if args == nil {
		return nil, fmt.Errorf("arguments must be a JSON object")
	}
	allowedSet := map[string]bool{}
	for _, name := range allowed {
		allowedSet[name] = true
	}
	for name := range args {
		if !allowedSet[name] {
			return nil, fmt.Errorf("unknown argument %q", name)
		}
	}
	return args, nil
}

func requiredString(args map[string]json.RawMessage, name string) (string, error) {
	raw, ok := args[name]
	if !ok {
		return "", fmt.Errorf("%s is required", name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%s must be a string", name)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", name)
	}
	if utf8.RuneCountInString(value) > maximumToolTextBytes {
		return "", fmt.Errorf("%s must be at most %d characters", name, maximumToolTextBytes)
	}
	return value, nil
}

func optionalInt(args map[string]json.RawMessage, name string, fallback, minimum, maximum int) (int, error) {
	value, err := optionalIntMinimum(args, name, fallback, minimum)
	if err != nil {
		return 0, err
	}
	if value > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
	}
	return value, nil
}

func optionalIntMinimum(args map[string]json.RawMessage, name string, fallback, minimum int) (int, error) {
	raw, ok := args[name]
	if !ok {
		return fallback, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	if value < minimum {
		return 0, fmt.Errorf("%s must be at least %d", name, minimum)
	}
	return value, nil
}

func optionalBool(args map[string]json.RawMessage, name string, fallback bool) (bool, error) {
	raw, ok := args[name]
	if !ok {
		return fallback, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return value, nil
}

func optionalEnum(args map[string]json.RawMessage, name, fallback string, allowed ...string) (string, error) {
	raw, ok := args[name]
	if !ok {
		return fallback, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%s must be a string", name)
	}
	value = strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range allowed {
		if value == candidate {
			return value, nil
		}
	}
	return "", fmt.Errorf("%s must be one of %s", name, strings.Join(allowed, ", "))
}

func optionalRelations(args map[string]json.RawMessage) ([]graph.EdgeKind, error) {
	raw, ok := args["relations"]
	if !ok {
		return nil, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("relations must be an array of strings")
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("relations must be an array of strings")
	}
	if len(values) > 32 {
		return nil, fmt.Errorf("relations must contain at most 32 values")
	}
	seen := map[graph.EdgeKind]bool{}
	result := make([]graph.EdgeKind, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || utf8.RuneCountInString(value) > 80 {
			return nil, fmt.Errorf("each relation must contain between 1 and 80 characters")
		}
		kind := graph.EdgeKind(value)
		if seen[kind] {
			return nil, fmt.Errorf("relation %q is duplicated", value)
		}
		seen[kind] = true
		result = append(result, kind)
	}
	return result, nil
}

func toolTokenBudget(args map[string]json.RawMessage) (int, error) {
	return optionalInt(args, "token_budget", defaultToolTokenBudget, minimumTokenBudget, maximumTokenBudget)
}

func limitExplanation(explanation query.Explanation, limit int) (query.Explanation, int) {
	result := query.Explanation{Target: explanation.Target}
	remaining := limit
	if count := min(remaining, len(explanation.Outgoing)); count > 0 {
		result.Outgoing = append(result.Outgoing, explanation.Outgoing[:count]...)
		remaining -= count
	}
	if count := min(remaining, len(explanation.Incoming)); count > 0 {
		result.Incoming = append(result.Incoming, explanation.Incoming[:count]...)
	}
	total := len(explanation.Outgoing) + len(explanation.Incoming)
	return result, max(0, total-limit)
}

func boundText(value string, tokenBudget int) string {
	maximum := tokenBudget * 3
	if len(value) <= maximum {
		return value
	}
	marker := "\nTRUNCATED\treason=mcp_token_budget\n"
	cut := maximum - len(marker)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	if newline := strings.LastIndexByte(value[:cut], '\n'); newline >= cut/2 {
		cut = newline
	}
	return value[:cut] + marker
}

func invalidArguments(err error) *toolExecutionError {
	return &toolExecutionError{Code: "invalid_arguments", Message: oneLine(err.Error())}
}

func graphUnavailable(err error) *toolExecutionError {
	return &toolExecutionError{Code: "graph_unavailable", Message: oneLine(err.Error())}
}

func internalToolError(err error) *toolExecutionError {
	return &toolExecutionError{Code: "internal_error", Message: oneLine(err.Error())}
}

func toolFailure(code, message string) callToolResult {
	failure := toolExecutionError{Code: code, Message: oneLine(message)}
	return callToolResult{
		Content:           []textContent{{Type: "text", Text: "ERROR\t" + failure.Code + "\t" + failure.Message}},
		StructuredContent: map[string]any{"error": failure},
		IsError:           true,
	}
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func toolNames() []string {
	definitions := toolDefinitions()
	result := make([]string, len(definitions))
	for i, definition := range definitions {
		result[i] = definition.Name
	}
	sort.Strings(result)
	return result
}
