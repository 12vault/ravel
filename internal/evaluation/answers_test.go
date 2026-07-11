package evaluation

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAnswerJSONLIsStrictAndRejectsDuplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "answers.jsonl")
	data := "\n" +
		`{"id":"one","correct":true,"inputTokens":10,"outputTokens":4,"costUsd":0.01,"model":"fixture"}` + "\n" +
		`{"id":"two","keyFactsFound":[],"toolTokens":3,"judge":"human"}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	records, err := LoadAnswerJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Correct == nil || !*records[0].Correct || records[1].KeyFactsFound == nil {
		t.Fatalf("records = %#v", records)
	}

	for name, content := range map[string]string{
		"unknown-field":   `{"id":"one","correct":true,"rawAnswer":"must not be stored"}`,
		"duplicate":       "{\"id\":\"one\",\"correct\":true}\n{\"id\":\"one\",\"correct\":false}",
		"no-score":        `{"id":"one","inputTokens":1}`,
		"negative":        `{"id":"one","correct":true,"inputTokens":-1}`,
		"duplicate-field": `{"id":"one","correct":true,"correct":false}`,
		"multiple-json":   `{"id":"one","correct":true} {"id":"two","correct":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "answers.jsonl")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadAnswerJSONL(path); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestAttachAnswerQualityAggregatesJudgmentsFactsTokensAndSpend(t *testing.T) {
	cases := []Case{
		{ID: "retrieval", Dataset: "repo", ExpectedKeyFacts: []string{"uses IDF", "follows call edges"}},
		{ID: "safety", Dataset: "repo", ExpectedKeyFacts: []string{"rejects symlinks"}},
		{ID: "other", Dataset: "other", ExpectedKeyFacts: []string{"not adjudicated"}},
	}
	report := Report{Results: []CaseResult{
		{ID: "retrieval", Dataset: "repo"},
		{ID: "safety", Dataset: "repo"},
		{ID: "other", Dataset: "other"},
	}}
	correct, incorrect := true, false
	records := []AnswerRecord{
		{ID: "retrieval", Correct: &correct, KeyFactsFound: []string{"uses IDF"}, InputTokens: 10, OutputTokens: 20, ToolTokens: 5, CostUSD: 0.01, Model: "fixture-model", Judge: "human", RunID: "run-1"},
		{ID: "safety", Correct: &incorrect, KeyFactsFound: []string{}, InputTokens: 7, OutputTokens: 3, CostUSD: 0.02},
	}
	if err := AttachAnswerQuality(&report, cases, records); err != nil {
		t.Fatal(err)
	}
	if report.AnswerQuality == nil {
		t.Fatal("answer metrics were not attached")
	}
	got := *report.AnswerQuality
	if got.Cases != 2 || got.JudgedCases != 2 || got.CorrectCases != 1 || got.Accuracy != 0.5 || got.KeyFactCases != 2 || got.MeanKeyFactCoverage != 0.25 {
		t.Fatalf("quality metrics = %#v", got)
	}
	if got.TotalInputTokens != 17 || got.TotalOutputTokens != 23 || got.TotalToolTokens != 5 || got.TotalAgentTokens != 45 || got.MeanTotalAgentTokens != 22.5 || math.Abs(got.TotalCostUSD-0.03) > 1e-12 {
		t.Fatalf("accounting metrics = %#v", got)
	}
	if report.AnswerDatasets["repo"].Cases != 2 || len(report.AnswerDatasets) != 1 {
		t.Fatalf("dataset metrics = %#v", report.AnswerDatasets)
	}
	if report.Results[0].Answer == nil || report.Results[0].Answer.KeyFactCoverage != 0.5 || report.Results[0].Answer.TotalAgentTokens != 35 {
		t.Fatalf("retrieval answer = %#v", report.Results[0].Answer)
	}
	if report.Results[2].Answer != nil {
		t.Fatalf("partial ledger unexpectedly scored other case: %#v", report.Results[2].Answer)
	}

	var rendered bytes.Buffer
	if err := Write(&rendered, report); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.String(), "rawAnswer") || !strings.Contains(rendered.String(), `"answerQuality"`) {
		t.Fatalf("unexpected report JSON: %s", rendered.String())
	}
	var decoded Report
	if err := json.Unmarshal(rendered.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.AnswerQuality == nil || decoded.Results[0].Answer == nil {
		t.Fatalf("round-trip report = %#v", decoded)
	}
}

func TestAttachAnswerQualityRejectsInvalidOrMismatchedLedgersTransactionally(t *testing.T) {
	correct := true
	baseCases := []Case{{ID: "one", Dataset: "repo", ExpectedKeyFacts: []string{"fact"}}}
	baseReport := Report{Results: []CaseResult{{ID: "one", Dataset: "repo"}}}
	tests := []struct {
		name    string
		cases   []Case
		report  Report
		records []AnswerRecord
	}{
		{name: "unknown-case", cases: baseCases, report: baseReport, records: []AnswerRecord{{ID: "missing", Correct: &correct}}},
		{name: "duplicate-record", cases: baseCases, report: baseReport, records: []AnswerRecord{{ID: "one", Correct: &correct}, {ID: "one", Correct: &correct}}},
		{name: "missing-score", cases: baseCases, report: baseReport, records: []AnswerRecord{{ID: "one"}}},
		{name: "unknown-fact", cases: baseCases, report: baseReport, records: []AnswerRecord{{ID: "one", KeyFactsFound: []string{"different"}}}},
		{name: "duplicate-found-fact", cases: baseCases, report: baseReport, records: []AnswerRecord{{ID: "one", KeyFactsFound: []string{"fact", "fact"}}}},
		{name: "no-expected-facts", cases: []Case{{ID: "one", Dataset: "repo"}}, report: baseReport, records: []AnswerRecord{{ID: "one", KeyFactsFound: []string{}}}},
		{name: "duplicate-expected-fact", cases: []Case{{ID: "one", Dataset: "repo", ExpectedKeyFacts: []string{"fact", "fact"}}}, report: baseReport, records: []AnswerRecord{{ID: "one", KeyFactsFound: []string{"fact"}}}},
		{name: "dataset-mismatch", cases: baseCases, report: Report{Results: []CaseResult{{ID: "one", Dataset: "other"}}}, records: []AnswerRecord{{ID: "one", Correct: &correct}}},
		{name: "negative-tokens", cases: baseCases, report: baseReport, records: []AnswerRecord{{ID: "one", Correct: &correct, ToolTokens: -1}}},
		{name: "token-overflow", cases: baseCases, report: baseReport, records: []AnswerRecord{{ID: "one", Correct: &correct, InputTokens: math.MaxInt64, OutputTokens: 1}}},
		{name: "invalid-cost", cases: baseCases, report: baseReport, records: []AnswerRecord{{ID: "one", Correct: &correct, CostUSD: math.NaN()}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := test.report
			marker := AnswerMetrics{Cases: 99}
			report.AnswerQuality = &marker
			if err := AttachAnswerQuality(&report, test.cases, test.records); err == nil {
				t.Fatal("expected validation error")
			}
			if report.AnswerQuality != &marker || report.AnswerQuality.Cases != 99 {
				t.Fatalf("invalid ledger partially mutated report: %#v", report.AnswerQuality)
			}
		})
	}
}

func TestAttachAnswerQualityDetectsAggregateOverflow(t *testing.T) {
	correct := true
	cases := []Case{{ID: "one", Dataset: "repo"}, {ID: "two", Dataset: "repo"}}
	report := Report{Results: []CaseResult{{ID: "one", Dataset: "repo"}, {ID: "two", Dataset: "repo"}}}
	records := []AnswerRecord{
		{ID: "one", Correct: &correct, InputTokens: math.MaxInt64},
		{ID: "two", Correct: &correct, InputTokens: 1},
	}
	if err := AttachAnswerQuality(&report, cases, records); err == nil || !strings.Contains(err.Error(), "overflow") {
		t.Fatalf("error = %v, want aggregate overflow", err)
	}
}
