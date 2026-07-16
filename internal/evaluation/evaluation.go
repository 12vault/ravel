package evaluation

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/query"
)

type Case struct {
	ID               string   `json:"id"`
	Dataset          string   `json:"dataset"`
	Question         string   `json:"question"`
	ExpectedNodeIDs  []string `json:"expectedNodeIds"`
	ExpectedEvidence []string `json:"expectedEvidence,omitempty"`
	ExpectedKeyFacts []string `json:"expectedKeyFacts,omitempty"`
}

type CaseResult struct {
	ID                  string            `json:"id"`
	Dataset             string            `json:"dataset"`
	RetrievedNodeIDs    []string          `json:"retrievedNodeIds"`
	RetrievedEdgeIDs    []string          `json:"retrievedEdgeIds,omitempty"`
	Recall              float64           `json:"recall"`
	Precision           float64           `json:"precision"`
	EvidenceRecall      float64           `json:"evidenceRecall"`
	EvidencePrecision   float64           `json:"evidencePrecision"`
	ReciprocalRank      float64           `json:"reciprocalRank"`
	LatencyMicros       int64             `json:"latencyMicros"`
	LatencyMillis       float64           `json:"latencyMs"`
	EstimatedTokens     int               `json:"estimatedTokens"`
	RecallPer1KTokens   float64           `json:"recallPer1KTokens"`
	EvidencePer1KTokens float64           `json:"evidenceRecallPer1KTokens"`
	Truncated           bool              `json:"truncated"`
	Answer              *AnswerCaseResult `json:"answer,omitempty"`
}

type Metrics struct {
	Cases                   int     `json:"cases"`
	MeanRecall              float64 `json:"meanRecall"`
	MeanPrecision           float64 `json:"meanPrecision"`
	MeanReciprocalRank      float64 `json:"meanReciprocalRank"`
	MeanLatencyMicros       float64 `json:"meanLatencyMicros"`
	MeanLatencyMillis       float64 `json:"meanLatencyMs"`
	P50LatencyMillis        float64 `json:"p50LatencyMs"`
	P95LatencyMillis        float64 `json:"p95LatencyMs"`
	MeanEstimatedTokens     float64 `json:"meanEstimatedTokens"`
	MeanRecallPer1KTokens   float64 `json:"meanRecallPer1KTokens"`
	MeanEvidencePer1KTokens float64 `json:"meanEvidenceRecallPer1KTokens"`
	MeanEvidenceRecall      float64 `json:"meanEvidenceRecall"`
	MeanEvidencePrecision   float64 `json:"meanEvidencePrecision"`
	TruncationRate          float64 `json:"truncationRate"`
}

type Report struct {
	Version          int                      `json:"version"`
	Retriever        string                   `json:"retriever"`
	TopK             int                      `json:"topK"`
	GraphVersion     string                   `json:"graphVersion,omitempty"`
	GraphGeneratedAt time.Time                `json:"graphGeneratedAt,omitempty"`
	GraphSHA256      string                   `json:"graphSha256,omitempty"`
	GraphRevision    string                   `json:"graphRevision,omitempty"`
	GraphNodes       int                      `json:"graphNodes"`
	GraphEdges       int                      `json:"graphEdges"`
	Overall          Metrics                  `json:"overall"`
	Datasets         map[string]Metrics       `json:"datasets"`
	Results          []CaseResult             `json:"results"`
	AnswerQuality    *AnswerMetrics           `json:"answerQuality,omitempty"`
	AnswerDatasets   map[string]AnswerMetrics `json:"answerDatasets,omitempty"`
	AnswerSHA256     string                   `json:"answerSha256,omitempty"`
	QualityGate      *QualityGateResult       `json:"qualityGate,omitempty"`
	RetrievalOptions query.RetrieveOptions    `json:"retrievalOptions,omitempty"`
	IndexBuildMicros int64                    `json:"indexBuildMicros"`
	IndexBuildMillis float64                  `json:"indexBuildMs"`
	RavelVersion     string                   `json:"ravelVersion,omitempty"`
	DatasetRevision  string                   `json:"datasetRevision,omitempty"`
	DatasetSHA256    string                   `json:"datasetSha256,omitempty"`
	AdapterVersion   string                   `json:"adapterVersion,omitempty"`
	GoVersion        string                   `json:"goVersion"`
	GOOS             string                   `json:"goos"`
	GOARCH           string                   `json:"goarch"`
}

type RunOptions struct {
	Retriever       string
	TopK            int
	Retrieval       query.RetrieveOptions
	RavelVersion    string
	DatasetRevision string
	DatasetSHA256   string
	AdapterVersion  string
	GraphSHA256     string
	GraphRevision   string
}

func LoadJSONL(path string) ([]Case, error) {
	cases, _, err := LoadJSONLWithHash(path)
	return cases, err
}

// LoadJSONLWithHash parses and hashes the exact same byte stream so benchmark
// provenance cannot drift if a dataset file is replaced during a run.
func LoadJSONLWithHash(path string) ([]Case, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	hasher := sha256.New()
	cases, err := loadCasesJSONL(io.TeeReader(file, hasher))
	if err != nil {
		return nil, "", err
	}
	return cases, fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func loadCasesJSONL(reader io.Reader) ([]Case, error) {
	var cases []Case
	seen := map[string]bool{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for line := 1; scanner.Scan(); line++ {
		data := bytes.TrimSpace(scanner.Bytes())
		if len(data) == 0 {
			continue
		}
		if err := ensureUniqueJSONFields(data); err != nil {
			return nil, fmt.Errorf("dataset line %d: %w", line, err)
		}
		if err := ensureAllowedJSONFields(data, "id", "dataset", "question", "expectedNodeIds", "expectedEvidence", "expectedKeyFacts"); err != nil {
			return nil, fmt.Errorf("dataset line %d: %w", line, err)
		}
		var item Case
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&item); err != nil {
			return nil, fmt.Errorf("dataset line %d: %w", line, err)
		}
		if err := ensureJSONEOF(decoder); err != nil {
			return nil, fmt.Errorf("dataset line %d: %w", line, err)
		}
		if err := validateCase(item); err != nil {
			return nil, fmt.Errorf("dataset line %d: %w", line, err)
		}
		if seen[item.ID] {
			return nil, fmt.Errorf("dataset line %d: duplicate id %q", line, item.ID)
		}
		seen[item.ID] = true
		cases = append(cases, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, errors.New("dataset contains no cases")
	}
	return cases, nil
}

func validateCase(item Case) error {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Dataset) == "" || strings.TrimSpace(item.Question) == "" || (len(item.ExpectedNodeIDs) == 0 && len(item.ExpectedEvidence) == 0) {
		return errors.New("requires id, dataset, question, and expectedNodeIds or expectedEvidence")
	}
	for label, values := range map[string][]string{
		"expectedNodeIds":  item.ExpectedNodeIDs,
		"expectedEvidence": item.ExpectedEvidence,
		"expectedKeyFacts": item.ExpectedKeyFacts,
	} {
		seen := map[string]bool{}
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%s cannot contain an empty value", label)
			}
			if seen[value] {
				return fmt.Errorf("%s contains duplicate value %q", label, value)
			}
			seen[value] = true
		}
	}
	return nil
}

func Run(g graph.Graph, cases []Case, topK int) (Report, error) {
	return RunWithOptions(g, cases, RunOptions{Retriever: "flat", TopK: topK, AdapterVersion: "graph-query-jsonl-v2"})
}

func RunWithOptions(g graph.Graph, cases []Case, options RunOptions) (Report, error) {
	if options.TopK < 1 {
		return Report{}, errors.New("top-k must be positive")
	}
	if options.Retriever == "" {
		options.Retriever = "context"
	}
	if options.Retriever != "flat" && options.Retriever != "context" {
		return Report{}, fmt.Errorf("retriever must be flat or context")
	}
	startedIndex := time.Now()
	index := query.NewIndex(g)
	indexMicros := time.Since(startedIndex).Microseconds()
	if options.Retriever == "context" && options.Retrieval.MaxNodes == 0 {
		options.Retrieval.MaxNodes = options.TopK
	}
	report := Report{
		Version: 3, Retriever: options.Retriever, TopK: options.TopK,
		GraphVersion: g.Version, GraphGeneratedAt: g.GeneratedAt, GraphSHA256: options.GraphSHA256, GraphRevision: options.GraphRevision,
		GraphNodes: len(g.Nodes), GraphEdges: len(g.Edges), Datasets: map[string]Metrics{},
		RetrievalOptions: options.Retrieval, IndexBuildMicros: indexMicros, IndexBuildMillis: float64(indexMicros) / 1_000,
		RavelVersion: options.RavelVersion, DatasetRevision: options.DatasetRevision,
		DatasetSHA256: options.DatasetSHA256, AdapterVersion: options.AdapterVersion,
		GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	}
	for _, item := range cases {
		started := time.Now()
		var result CaseResult
		if options.Retriever == "context" {
			retrieval, err := index.Retrieve(item.Question, options.Retrieval)
			if err != nil {
				return Report{}, fmt.Errorf("case %s: %w", item.ID, err)
			}
			result = scoreRetrieval(item, retrieval)
		} else {
			matches := index.Search(item.Question, options.TopK)
			result = score(item, matches)
		}
		result.LatencyMicros = time.Since(started).Microseconds()
		result.LatencyMillis = float64(result.LatencyMicros) / 1_000
		if result.EstimatedTokens > 0 {
			result.RecallPer1KTokens = result.Recall * 1_000 / float64(result.EstimatedTokens)
			result.EvidencePer1KTokens = result.EvidenceRecall * 1_000 / float64(result.EstimatedTokens)
		}
		report.Results = append(report.Results, result)
	}
	sort.Slice(report.Results, func(i, j int) bool { return report.Results[i].ID < report.Results[j].ID })
	report.Overall = aggregate(report.Results)
	byDataset := map[string][]CaseResult{}
	for _, result := range report.Results {
		byDataset[result.Dataset] = append(byDataset[result.Dataset], result)
	}
	for dataset, results := range byDataset {
		report.Datasets[dataset] = aggregate(results)
	}
	return report, nil
}

func GraphHash(g graph.Graph) (string, error) {
	// Hash logical graph content, not checkout location or build time, so the
	// same analyzed repository produces the same provenance ID on another host.
	g.Root = ""
	g.GeneratedAt = time.Time{}
	data, err := json.Marshal(g)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:]), nil
}

func DatasetHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:]), nil
}

func Write(w io.Writer, report Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

func score(item Case, matches []query.SearchResult) CaseResult {
	result := CaseResult{ID: item.ID, Dataset: item.Dataset}
	for _, match := range matches {
		result.RetrievedNodeIDs = append(result.RetrievedNodeIDs, match.Node.ID)
	}
	var rendered bytes.Buffer
	_ = query.WriteSearch(&rendered, matches, false)
	result.EstimatedTokens = estimatedTokens(rendered.Bytes())
	result.Recall, result.Precision, result.ReciprocalRank = scoreIDs(item.ExpectedNodeIDs, result.RetrievedNodeIDs)
	expectedEvidence := item.ExpectedEvidence
	if len(expectedEvidence) == 0 {
		expectedEvidence = item.ExpectedNodeIDs
	}
	result.EvidenceRecall, result.EvidencePrecision, _ = scoreIDs(expectedEvidence, result.RetrievedNodeIDs)
	if len(item.ExpectedNodeIDs) == 0 {
		result.Recall, result.Precision, result.ReciprocalRank = scoreIDs(expectedEvidence, result.RetrievedNodeIDs)
	}
	return result
}

func scoreRetrieval(item Case, retrieval query.Retrieval) CaseResult {
	result := CaseResult{ID: item.ID, Dataset: item.Dataset, EstimatedTokens: retrieval.Stats.EstimatedTokens, Truncated: retrieval.Stats.Truncated}
	for _, node := range retrieval.Nodes {
		result.RetrievedNodeIDs = append(result.RetrievedNodeIDs, node.ID)
	}
	for _, edge := range retrieval.Edges {
		result.RetrievedEdgeIDs = append(result.RetrievedEdgeIDs, edge.ID)
	}
	result.Recall, result.Precision, result.ReciprocalRank = scoreIDs(item.ExpectedNodeIDs, result.RetrievedNodeIDs)
	expectedEvidence := item.ExpectedEvidence
	if len(expectedEvidence) == 0 {
		expectedEvidence = item.ExpectedNodeIDs
	}
	retrievedEvidence := append(append([]string(nil), result.RetrievedNodeIDs...), result.RetrievedEdgeIDs...)
	var evidenceRank float64
	result.EvidenceRecall, result.EvidencePrecision, evidenceRank = scoreIDs(expectedEvidence, retrievedEvidence)
	if len(item.ExpectedNodeIDs) == 0 {
		result.Recall = result.EvidenceRecall
		result.Precision = result.EvidencePrecision
		result.ReciprocalRank = evidenceRank
	}
	return result
}

func scoreIDs(expectedIDs, retrievedIDs []string) (recall, precision, reciprocalRank float64) {
	if len(expectedIDs) == 0 {
		return 0, 0, 0
	}
	expected := map[string]bool{}
	for _, id := range expectedIDs {
		expected[id] = true
	}
	hits := 0
	rank := 0
	for index, id := range retrievedIDs {
		if expected[id] {
			hits++
			if rank == 0 {
				rank = index + 1
			}
		}
	}
	recall = float64(hits) / float64(len(expected))
	if len(retrievedIDs) > 0 {
		precision = float64(hits) / float64(len(retrievedIDs))
	}
	if rank > 0 {
		reciprocalRank = 1 / float64(rank)
	}
	return recall, precision, reciprocalRank
}

func estimatedTokens(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	return (len(data) + 2) / 3
}

func aggregate(results []CaseResult) Metrics {
	metrics := Metrics{Cases: len(results)}
	if len(results) == 0 {
		return metrics
	}
	for _, result := range results {
		metrics.MeanRecall += result.Recall
		metrics.MeanPrecision += result.Precision
		metrics.MeanReciprocalRank += result.ReciprocalRank
		metrics.MeanLatencyMicros += float64(result.LatencyMicros)
		metrics.MeanLatencyMillis += result.LatencyMillis
		metrics.MeanEstimatedTokens += float64(result.EstimatedTokens)
		metrics.MeanRecallPer1KTokens += result.RecallPer1KTokens
		metrics.MeanEvidencePer1KTokens += result.EvidencePer1KTokens
		metrics.MeanEvidenceRecall += result.EvidenceRecall
		metrics.MeanEvidencePrecision += result.EvidencePrecision
		if result.Truncated {
			metrics.TruncationRate++
		}
	}
	count := float64(len(results))
	metrics.MeanRecall /= count
	metrics.MeanPrecision /= count
	metrics.MeanReciprocalRank /= count
	metrics.MeanLatencyMicros /= count
	metrics.MeanLatencyMillis /= count
	metrics.MeanEstimatedTokens /= count
	metrics.MeanRecallPer1KTokens /= count
	metrics.MeanEvidencePer1KTokens /= count
	metrics.MeanEvidenceRecall /= count
	metrics.MeanEvidencePrecision /= count
	metrics.TruncationRate /= count
	latencies := make([]float64, 0, len(results))
	for _, result := range results {
		latencies = append(latencies, result.LatencyMillis)
	}
	sort.Float64s(latencies)
	metrics.P50LatencyMillis = percentile(latencies, 0.50)
	metrics.P95LatencyMillis = percentile(latencies, 0.95)
	return metrics
}

func percentile(values []float64, fraction float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(values))*fraction)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}
