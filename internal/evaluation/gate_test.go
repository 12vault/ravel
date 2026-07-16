package evaluation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/12vault/ravel/internal/graph"
)

func TestValidateExpectedIDsRejectsMissingNodesAndEvidence(t *testing.T) {
	g := graph.Graph{
		Nodes: []graph.Node{{ID: "node://current"}},
		Edges: []graph.Edge{{ID: "edge://current"}},
	}
	err := ValidateExpectedIDs(g, []Case{
		{ID: "stale", ExpectedNodeIDs: []string{"node://missing"}, ExpectedEvidence: []string{"edge://missing"}},
		{ID: "current", ExpectedNodeIDs: []string{"node://current"}, ExpectedEvidence: []string{"edge://current", "node://current"}},
	})
	if err == nil || !strings.Contains(err.Error(), "case stale: expected node node://missing") || !strings.Contains(err.Error(), "case stale: expected evidence edge://missing") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadAndEvaluateQualityGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gate.json")
	data := `{"version":1,"minimumCases":50,"minimumMeanRecall":0.7,"minimumMeanEvidenceRecall":0.5,"maximumTruncationRate":0.25,"requireFreshExpectations":true}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	gate, hash, err := LoadQualityGateWithHash(path)
	if err != nil {
		t.Fatal(err)
	}
	result := EvaluateQualityGate(Report{Overall: Metrics{Cases: 49, MeanRecall: 0.69, MeanEvidenceRecall: 0.5, TruncationRate: 0.5}}, gate, hash)
	if result.Passed || result.ConfigSHA256 == "" || len(result.Failures) != 3 {
		t.Fatalf("result = %#v", result)
	}
	if err := result.Error(); err == nil || !strings.Contains(err.Error(), "cases 49") {
		t.Fatalf("gate error = %v", err)
	}

	passing := EvaluateQualityGate(Report{Overall: Metrics{Cases: 50, MeanRecall: 0.7, MeanEvidenceRecall: 0.6, TruncationRate: 0.2}}, gate, hash)
	if !passing.Passed || passing.Error() != nil {
		t.Fatalf("passing result = %#v", passing)
	}
}

func TestLoadQualityGateRejectsUnknownOrInvalidValues(t *testing.T) {
	for name, data := range map[string]string{
		"unknown":       `{"version":1,"minimumCases":1,"typo":true}`,
		"bad-version":   `{"version":2,"minimumCases":1}`,
		"missing-cases": `{"version":1}`,
		"bad-threshold": `{"version":1,"minimumCases":1,"minimumMeanRecall":1.1}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "gate.json")
			if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := LoadQualityGateWithHash(path); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
