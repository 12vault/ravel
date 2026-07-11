package evaluation

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/12ya/reporavel/internal/graph"
	"github.com/12ya/reporavel/internal/query"
)

type Case struct {
	ID              string   `json:"id"`
	Dataset         string   `json:"dataset"`
	Question        string   `json:"question"`
	ExpectedNodeIDs []string `json:"expectedNodeIds"`
}

type CaseResult struct {
	ID               string   `json:"id"`
	Dataset          string   `json:"dataset"`
	RetrievedNodeIDs []string `json:"retrievedNodeIds"`
	Recall           float64  `json:"recall"`
	Precision        float64  `json:"precision"`
	ReciprocalRank   float64  `json:"reciprocalRank"`
	LatencyMicros    int64    `json:"latencyMicros"`
}

type Metrics struct {
	Cases              int     `json:"cases"`
	MeanRecall         float64 `json:"meanRecall"`
	MeanPrecision      float64 `json:"meanPrecision"`
	MeanReciprocalRank float64 `json:"meanReciprocalRank"`
	MeanLatencyMicros  float64 `json:"meanLatencyMicros"`
}

type Report struct {
	Version    int                `json:"version"`
	TopK       int                `json:"topK"`
	GraphNodes int                `json:"graphNodes"`
	GraphEdges int                `json:"graphEdges"`
	Overall    Metrics            `json:"overall"`
	Datasets   map[string]Metrics `json:"datasets"`
	Results    []CaseResult       `json:"results"`
}

func LoadJSONL(path string) ([]Case, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var cases []Case
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for line := 1; scanner.Scan(); line++ {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var item Case
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, fmt.Errorf("dataset line %d: %w", line, err)
		}
		if item.ID == "" || item.Dataset == "" || item.Question == "" || len(item.ExpectedNodeIDs) == 0 {
			return nil, fmt.Errorf("dataset line %d requires id, dataset, question, and expectedNodeIds", line)
		}
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

func Run(g graph.Graph, cases []Case, topK int) (Report, error) {
	if topK < 1 {
		return Report{}, errors.New("top-k must be positive")
	}
	report := Report{Version: 1, TopK: topK, GraphNodes: len(g.Nodes), GraphEdges: len(g.Edges), Datasets: map[string]Metrics{}}
	for _, item := range cases {
		started := time.Now()
		matches := query.Search(g, item.Question, topK)
		result := score(item, matches)
		result.LatencyMicros = time.Since(started).Microseconds()
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

func Write(w io.Writer, report Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

func score(item Case, matches []query.SearchResult) CaseResult {
	expected := map[string]bool{}
	for _, id := range item.ExpectedNodeIDs {
		expected[id] = true
	}
	hits := 0
	rank := 0
	result := CaseResult{ID: item.ID, Dataset: item.Dataset}
	for index, match := range matches {
		result.RetrievedNodeIDs = append(result.RetrievedNodeIDs, match.Node.ID)
		if expected[match.Node.ID] {
			hits++
			if rank == 0 {
				rank = index + 1
			}
		}
	}
	result.Recall = float64(hits) / float64(len(expected))
	if len(matches) > 0 {
		result.Precision = float64(hits) / float64(len(matches))
	}
	if rank > 0 {
		result.ReciprocalRank = 1 / float64(rank)
	}
	return result
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
	}
	count := float64(len(results))
	metrics.MeanRecall /= count
	metrics.MeanPrecision /= count
	metrics.MeanReciprocalRank /= count
	metrics.MeanLatencyMicros /= count
	return metrics
}
