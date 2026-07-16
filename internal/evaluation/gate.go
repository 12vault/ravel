package evaluation

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/12vault/ravel/internal/graph"
)

const qualityGateVersion = 1

// QualityGate defines deterministic retrieval thresholds for CI and local
// regression checks. Pointer metrics distinguish an omitted threshold from an
// explicit zero threshold.
type QualityGate struct {
	Version                      int      `json:"version"`
	MinimumCases                 int      `json:"minimumCases"`
	MinimumMeanRecall            *float64 `json:"minimumMeanRecall,omitempty"`
	MinimumMeanPrecision         *float64 `json:"minimumMeanPrecision,omitempty"`
	MinimumMeanReciprocalRank    *float64 `json:"minimumMeanReciprocalRank,omitempty"`
	MinimumMeanEvidenceRecall    *float64 `json:"minimumMeanEvidenceRecall,omitempty"`
	MinimumMeanEvidencePrecision *float64 `json:"minimumMeanEvidencePrecision,omitempty"`
	MaximumTruncationRate        *float64 `json:"maximumTruncationRate,omitempty"`
	RequireFreshExpectations     bool     `json:"requireFreshExpectations"`
}

type QualityGateResult struct {
	Passed       bool     `json:"passed"`
	ConfigSHA256 string   `json:"configSha256"`
	Failures     []string `json:"failures,omitempty"`
}

func LoadQualityGateWithHash(path string) (QualityGate, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return QualityGate{}, "", err
	}
	if err := ensureUniqueJSONFields(data); err != nil {
		return QualityGate{}, "", fmt.Errorf("quality gate: %w", err)
	}
	if err := ensureAllowedJSONFields(data,
		"version", "minimumCases", "minimumMeanRecall", "minimumMeanPrecision",
		"minimumMeanReciprocalRank", "minimumMeanEvidenceRecall",
		"minimumMeanEvidencePrecision", "maximumTruncationRate",
		"requireFreshExpectations"); err != nil {
		return QualityGate{}, "", fmt.Errorf("quality gate: %w", err)
	}
	var gate QualityGate
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&gate); err != nil {
		return QualityGate{}, "", fmt.Errorf("quality gate: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return QualityGate{}, "", fmt.Errorf("quality gate: %w", err)
	}
	if err := validateQualityGate(gate); err != nil {
		return QualityGate{}, "", err
	}
	sum := sha256.Sum256(data)
	return gate, fmt.Sprintf("%x", sum[:]), nil
}

func validateQualityGate(gate QualityGate) error {
	if gate.Version != qualityGateVersion {
		return fmt.Errorf("quality gate version must be %d", qualityGateVersion)
	}
	if gate.MinimumCases < 1 {
		return errors.New("quality gate minimumCases must be positive")
	}
	for name, threshold := range map[string]*float64{
		"minimumMeanRecall":            gate.MinimumMeanRecall,
		"minimumMeanPrecision":         gate.MinimumMeanPrecision,
		"minimumMeanReciprocalRank":    gate.MinimumMeanReciprocalRank,
		"minimumMeanEvidenceRecall":    gate.MinimumMeanEvidenceRecall,
		"minimumMeanEvidencePrecision": gate.MinimumMeanEvidencePrecision,
		"maximumTruncationRate":        gate.MaximumTruncationRate,
	} {
		if threshold != nil && (*threshold < 0 || *threshold > 1) {
			return fmt.Errorf("quality gate %s must be between 0 and 1", name)
		}
	}
	return nil
}

// ValidateExpectedIDs rejects stale benchmark ground truth before scoring. A
// node expectation must name a current graph node; evidence may name a current
// node or edge because the JSONL contract accepts both forms.
func ValidateExpectedIDs(g graph.Graph, cases []Case) error {
	nodes := make(map[string]bool, len(g.Nodes))
	for _, node := range g.Nodes {
		nodes[node.ID] = true
	}
	edges := make(map[string]bool, len(g.Edges))
	for _, edge := range g.Edges {
		edges[edge.ID] = true
	}
	var failures []string
	for _, item := range cases {
		for _, id := range item.ExpectedNodeIDs {
			if !nodes[id] {
				failures = append(failures, fmt.Sprintf("case %s: expected node %s is absent from the graph", item.ID, id))
			}
		}
		for _, id := range item.ExpectedEvidence {
			if !nodes[id] && !edges[id] {
				failures = append(failures, fmt.Sprintf("case %s: expected evidence %s is absent from the graph", item.ID, id))
			}
		}
	}
	if len(failures) == 0 {
		return nil
	}
	sort.Strings(failures)
	return errors.New("stale benchmark expectations:\n- " + strings.Join(failures, "\n- "))
}

func EvaluateQualityGate(report Report, gate QualityGate, configHash string) QualityGateResult {
	result := QualityGateResult{Passed: true, ConfigSHA256: configHash}
	failMinimumInt := func(name string, actual, minimum int) {
		if actual < minimum {
			result.Failures = append(result.Failures, fmt.Sprintf("%s %d is below minimum %d", name, actual, minimum))
		}
	}
	failMinimum := func(name string, actual float64, minimum *float64) {
		if minimum != nil && actual < *minimum {
			result.Failures = append(result.Failures, fmt.Sprintf("%s %.6f is below minimum %.6f", name, actual, *minimum))
		}
	}
	failMaximum := func(name string, actual float64, maximum *float64) {
		if maximum != nil && actual > *maximum {
			result.Failures = append(result.Failures, fmt.Sprintf("%s %.6f exceeds maximum %.6f", name, actual, *maximum))
		}
	}

	failMinimumInt("cases", report.Overall.Cases, gate.MinimumCases)
	failMinimum("meanRecall", report.Overall.MeanRecall, gate.MinimumMeanRecall)
	failMinimum("meanPrecision", report.Overall.MeanPrecision, gate.MinimumMeanPrecision)
	failMinimum("meanReciprocalRank", report.Overall.MeanReciprocalRank, gate.MinimumMeanReciprocalRank)
	failMinimum("meanEvidenceRecall", report.Overall.MeanEvidenceRecall, gate.MinimumMeanEvidenceRecall)
	failMinimum("meanEvidencePrecision", report.Overall.MeanEvidencePrecision, gate.MinimumMeanEvidencePrecision)
	failMaximum("truncationRate", report.Overall.TruncationRate, gate.MaximumTruncationRate)
	sort.Strings(result.Failures)
	result.Passed = len(result.Failures) == 0
	return result
}

func (result QualityGateResult) Error() error {
	if result.Passed {
		return nil
	}
	return errors.New("retrieval quality gate failed:\n- " + strings.Join(result.Failures, "\n- "))
}
