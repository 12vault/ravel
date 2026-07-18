package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/query"
	"github.com/12vault/ravel/internal/store"
)

const contextBatchMaxRequestBytes = 1024 * 1024

type contextBatchRequest struct {
	ID           string   `json:"id"`
	Question     string   `json:"question"`
	TraceNodeIDs []string `json:"traceNodeIds,omitempty"`
}

type contextBatchReady struct {
	Type          string  `json:"type"`
	Version       int     `json:"version"`
	GraphLoadMS   float64 `json:"graphLoadMs"`
	IndexBuildMS  float64 `json:"indexBuildMs"`
	IndexCacheHit bool    `json:"indexCacheHit,omitempty"`
	GraphNodes    int     `json:"graphNodes"`
	GraphEdges    int     `json:"graphEdges"`
}

type contextBatchResponse struct {
	Type      string           `json:"type"`
	ID        string           `json:"id,omitempty"`
	QueryMS   float64          `json:"queryMs,omitempty"`
	Retrieval *query.Retrieval `json:"retrieval,omitempty"`
	Error     string           `json:"error,omitempty"`
	Line      int              `json:"line,omitempty"`
}

// runContextBatch serves a fixed graph snapshot over JSONL. It exists for
// benchmark suites and other finite callers that need to reuse one query index.
func runContextBatch(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	configPath := flagValue(args, "config", ".reporavel.yaml")
	cfg, err := loadCommandConfig(args, configPath)
	if err != nil {
		return err
	}
	fs := newFlagSet("context-batch")
	configFlag := fs.String("config", configPath, "configuration file")
	outDir := fs.String("out", cfg.Output.Dir, "graph directory")
	traversal := fs.String("traversal", cfg.Retrieval.Traversal, "bfs or dfs")
	direction := fs.String("direction", cfg.Retrieval.Direction, "out, in, or both")
	relations := fs.String("relations", cfg.Retrieval.Relations, "comma-separated edge kinds")
	inferRelations := fs.Bool("infer-relations", cfg.Retrieval.InferRelations, "infer relation filters from the question")
	seedLimit := fs.Int("seed-limit", cfg.Retrieval.SeedLimit, "maximum lexical seeds")
	maxDepth := fs.Int("max-depth", cfg.Retrieval.MaxDepth, "graph traversal depth")
	maxNodes := fs.Int("max-nodes", cfg.Retrieval.MaxNodes, "hard node limit")
	branchFanout := fs.Int("branch-fanout", cfg.Retrieval.BranchFanout, "0 for automatic, positive for neighbors expanded per node")
	hubThreshold := fs.Int("hub-degree-threshold", cfg.Retrieval.HubDegreeThreshold, "0 for automatic, -1 to disable")
	tokenBudget := fs.Int("token-budget", cfg.Retrieval.TokenBudget, "approximate output-token budget")
	communityBoost := fs.Bool("community-boost", cfg.Retrieval.CommunityBoost, "prioritize neighbors in the same detected community")
	candidateShortlist := fs.Bool("candidate-shortlist", false, "favor a compact ranked candidate list over explanatory edges")
	valueFlags := []string{"config", "out", "traversal", "direction", "relations", "seed-limit", "max-depth", "max-nodes", "branch-fanout", "hub-degree-threshold", "token-budget"}
	if err := fs.Parse(flexibleFlags(args, valueFlags...)); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("context-batch does not accept positional arguments")
	}
	if *configFlag != configPath {
		return errors.New("internal config flag parsing mismatch")
	}

	var edgeKinds []graph.EdgeKind
	for _, value := range strings.Split(*relations, ",") {
		if value = strings.TrimSpace(value); value != "" && !strings.EqualFold(value, "all") {
			edgeKinds = append(edgeKinds, graph.EdgeKind(value))
		}
	}
	options := query.RetrieveOptions{
		Traversal: query.Traversal(strings.ToLower(*traversal)), Direction: query.Direction(strings.ToLower(*direction)),
		Relations: edgeKinds, DisableRelationInference: !*inferRelations, SeedLimit: *seedLimit,
		MaxDepth: *maxDepth, MaxNodes: *maxNodes, BranchFanout: *branchFanout, HubDegreeThreshold: *hubThreshold, TokenBudget: *tokenBudget,
		CommunityBoost:     *communityBoost,
		CandidateShortlist: *candidateShortlist,
	}

	loadStarted := time.Now()
	graphData, err := store.LoadGraphData(*outDir)
	if err != nil {
		return err
	}
	graphLoadMS := durationMS(time.Since(loadStarted))
	indexStarted := time.Now()
	index, cacheHit, err := query.LoadOrBuildIndex(graphData, filepath.Join(*outDir, ".state", "cache"))
	if err != nil {
		return err
	}
	indexBuildMS := durationMS(time.Since(indexStarted))
	graphNodes, graphEdges := index.Counts()

	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(contextBatchReady{
		Type: "ready", Version: 1, GraphLoadMS: graphLoadMS, IndexBuildMS: indexBuildMS,
		IndexCacheHit: cacheHit, GraphNodes: graphNodes, GraphEdges: graphEdges,
	}); err != nil {
		return err
	}

	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 64*1024), contextBatchMaxRequestBytes)
	line := 0
	for scanner.Scan() {
		line++
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(strings.TrimSpace(scanner.Text())) == 0 {
			continue
		}
		var request contextBatchRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			if writeErr := encoder.Encode(contextBatchResponse{Type: "error", Error: fmt.Sprintf("invalid JSON: %v", err), Line: line}); writeErr != nil {
				return writeErr
			}
			continue
		}
		if strings.TrimSpace(request.ID) == "" || strings.TrimSpace(request.Question) == "" {
			message := "request requires non-empty id and question"
			if err := encoder.Encode(contextBatchResponse{Type: "error", ID: request.ID, Error: message, Line: line}); err != nil {
				return err
			}
			continue
		}
		started := time.Now()
		requestOptions := options
		requestOptions.TraceNodeIDs = append([]string(nil), request.TraceNodeIDs...)
		result, err := index.Retrieve(request.Question, requestOptions)
		queryMS := durationMS(time.Since(started))
		if err != nil {
			if writeErr := encoder.Encode(contextBatchResponse{Type: "error", ID: request.ID, Error: err.Error(), QueryMS: queryMS, Line: line}); writeErr != nil {
				return writeErr
			}
			continue
		}
		if err := encoder.Encode(contextBatchResponse{Type: "result", ID: request.ID, QueryMS: queryMS, Retrieval: &result}); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read context-batch request: %w", err)
	}
	return ctx.Err()
}

func durationMS(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}
