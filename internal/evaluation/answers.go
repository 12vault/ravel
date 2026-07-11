package evaluation

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

// AnswerRecord is an externally adjudicated answer-quality observation. It
// deliberately contains no raw model answer: benchmark artifacts can retain
// correctness, key-fact coverage, token use, and spend without retaining
// potentially sensitive generated text.
type AnswerRecord struct {
	ID            string   `json:"id"`
	Correct       *bool    `json:"correct,omitempty"`
	KeyFactsFound []string `json:"keyFactsFound,omitempty"`
	InputTokens   int64    `json:"inputTokens,omitempty"`
	OutputTokens  int64    `json:"outputTokens,omitempty"`
	ToolTokens    int64    `json:"toolTokens,omitempty"`
	CostUSD       float64  `json:"costUsd,omitempty"`
	Model         string   `json:"model,omitempty"`
	Judge         string   `json:"judge,omitempty"`
	RunID         string   `json:"runId,omitempty"`
}

type AnswerCaseResult struct {
	Correct          *bool    `json:"correct,omitempty"`
	KeyFactScored    bool     `json:"keyFactScored"`
	ExpectedKeyFacts int      `json:"expectedKeyFacts"`
	FoundKeyFacts    int      `json:"foundKeyFacts"`
	KeyFactCoverage  float64  `json:"keyFactCoverage"`
	KeyFactsFound    []string `json:"keyFactsFound,omitempty"`
	InputTokens      int64    `json:"inputTokens"`
	OutputTokens     int64    `json:"outputTokens"`
	ToolTokens       int64    `json:"toolTokens"`
	TotalAgentTokens int64    `json:"totalAgentTokens"`
	CostUSD          float64  `json:"costUsd"`
	Model            string   `json:"model,omitempty"`
	Judge            string   `json:"judge,omitempty"`
	RunID            string   `json:"runId,omitempty"`
}

type AnswerMetrics struct {
	Cases                int     `json:"cases"`
	JudgedCases          int     `json:"judgedCases"`
	CorrectCases         int     `json:"correctCases"`
	Accuracy             float64 `json:"accuracy"`
	KeyFactCases         int     `json:"keyFactCases"`
	MeanKeyFactCoverage  float64 `json:"meanKeyFactCoverage"`
	TotalInputTokens     int64   `json:"totalInputTokens"`
	TotalOutputTokens    int64   `json:"totalOutputTokens"`
	TotalToolTokens      int64   `json:"totalToolTokens"`
	TotalAgentTokens     int64   `json:"totalAgentTokens"`
	MeanTotalAgentTokens float64 `json:"meanTotalAgentTokens"`
	TotalCostUSD         float64 `json:"totalCostUsd"`
}

// LoadAnswerJSONL loads a privacy-preserving answer ledger. Unknown fields are
// rejected so misspelled judgment or accounting fields cannot silently skew a
// published result.
func LoadAnswerJSONL(path string) ([]AnswerRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	seen := map[string]bool{}
	var records []AnswerRecord
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for line := 1; scanner.Scan(); line++ {
		data := bytes.TrimSpace(scanner.Bytes())
		if len(data) == 0 {
			continue
		}
		if err := ensureUniqueJSONFields(data); err != nil {
			return nil, fmt.Errorf("answer ledger line %d: %w", line, err)
		}
		var record AnswerRecord
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			return nil, fmt.Errorf("answer ledger line %d: %w", line, err)
		}
		if err := ensureJSONEOF(decoder); err != nil {
			return nil, fmt.Errorf("answer ledger line %d: %w", line, err)
		}
		if err := validateAnswerRecord(record); err != nil {
			return nil, fmt.Errorf("answer ledger line %d: %w", line, err)
		}
		if seen[record.ID] {
			return nil, fmt.Errorf("answer ledger line %d: duplicate id %q", line, record.ID)
		}
		seen[record.ID] = true
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, errors.New("answer ledger contains no records")
	}
	return records, nil
}

// AttachAnswerQuality validates and attaches external answer judgments to an
// existing retrieval report. A ledger may cover a subset of retrieval cases;
// AnswerMetrics.Cases makes that evaluated denominator explicit.
func AttachAnswerQuality(report *Report, cases []Case, records []AnswerRecord) error {
	if report == nil {
		return errors.New("report is nil")
	}
	if len(records) == 0 {
		return errors.New("answer ledger contains no records")
	}

	caseByID := make(map[string]Case, len(cases))
	for _, item := range cases {
		if strings.TrimSpace(item.ID) == "" {
			return errors.New("dataset contains a case with an empty id")
		}
		if _, exists := caseByID[item.ID]; exists {
			return fmt.Errorf("dataset contains duplicate case id %q", item.ID)
		}
		caseByID[item.ID] = item
	}

	resultIndex := make(map[string]int, len(report.Results))
	for index, result := range report.Results {
		if _, exists := resultIndex[result.ID]; exists {
			return fmt.Errorf("report contains duplicate case id %q", result.ID)
		}
		resultIndex[result.ID] = index
	}

	attachments := make(map[string]AnswerCaseResult, len(records))
	byDataset := make(map[string][]AnswerCaseResult)
	seen := make(map[string]bool, len(records))
	all := make([]AnswerCaseResult, 0, len(records))
	for _, record := range records {
		if err := validateAnswerRecord(record); err != nil {
			return fmt.Errorf("answer %q: %w", record.ID, err)
		}
		if seen[record.ID] {
			return fmt.Errorf("answer ledger contains duplicate id %q", record.ID)
		}
		seen[record.ID] = true

		item, exists := caseByID[record.ID]
		if !exists {
			return fmt.Errorf("answer %q does not match a dataset case", record.ID)
		}
		index, exists := resultIndex[record.ID]
		if !exists {
			return fmt.Errorf("answer %q does not match a report result", record.ID)
		}
		if report.Results[index].Dataset != item.Dataset {
			return fmt.Errorf("answer %q dataset mismatch: report has %q, cases have %q", record.ID, report.Results[index].Dataset, item.Dataset)
		}

		answer, err := scoreAnswer(item, record)
		if err != nil {
			return fmt.Errorf("answer %q: %w", record.ID, err)
		}
		attachments[record.ID] = answer
		all = append(all, answer)
		byDataset[item.Dataset] = append(byDataset[item.Dataset], answer)
	}

	overall, err := aggregateAnswers(all)
	if err != nil {
		return err
	}
	datasets := make(map[string]AnswerMetrics, len(byDataset))
	for dataset, answers := range byDataset {
		metrics, err := aggregateAnswers(answers)
		if err != nil {
			return fmt.Errorf("answer dataset %q: %w", dataset, err)
		}
		datasets[dataset] = metrics
	}

	for index := range report.Results {
		report.Results[index].Answer = nil
		if answer, exists := attachments[report.Results[index].ID]; exists {
			copy := answer
			report.Results[index].Answer = &copy
		}
	}
	report.AnswerQuality = &overall
	report.AnswerDatasets = datasets
	report.AnswerSHA256 = ""
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values on one line")
	}
	return err
}

// ensureUniqueJSONFields rejects duplicate top-level object keys before
// encoding/json can silently let the last value win. Benchmark records are
// intentionally flat objects, so checking the top level protects every field
// that can affect retrieval or answer-quality scoring.
func ensureUniqueJSONFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	opening, ok := token.(json.Delim)
	if !ok || opening != '{' {
		return errors.New("record must be a JSON object")
	}

	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := token.(string)
		if !ok {
			return errors.New("object field name must be a string")
		}
		if seen[name] {
			return fmt.Errorf("duplicate field %q", name)
		}
		seen[name] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func validateAnswerRecord(record AnswerRecord) error {
	if strings.TrimSpace(record.ID) == "" {
		return errors.New("id is required")
	}
	if record.Correct == nil && record.KeyFactsFound == nil {
		return errors.New("requires correct or keyFactsFound")
	}
	if record.InputTokens < 0 || record.OutputTokens < 0 || record.ToolTokens < 0 {
		return errors.New("token counts must be non-negative")
	}
	if record.CostUSD < 0 || math.IsNaN(record.CostUSD) || math.IsInf(record.CostUSD, 0) {
		return errors.New("costUsd must be finite and non-negative")
	}
	if _, err := totalTokens(record.InputTokens, record.OutputTokens, record.ToolTokens); err != nil {
		return err
	}
	return nil
}

func scoreAnswer(item Case, record AnswerRecord) (AnswerCaseResult, error) {
	total, err := totalTokens(record.InputTokens, record.OutputTokens, record.ToolTokens)
	if err != nil {
		return AnswerCaseResult{}, err
	}
	answer := AnswerCaseResult{
		InputTokens: record.InputTokens, OutputTokens: record.OutputTokens, ToolTokens: record.ToolTokens,
		TotalAgentTokens: total, CostUSD: record.CostUSD, Model: record.Model, Judge: record.Judge, RunID: record.RunID,
	}
	if record.Correct != nil {
		correct := *record.Correct
		answer.Correct = &correct
	}
	if record.KeyFactsFound == nil {
		return answer, nil
	}
	if len(item.ExpectedKeyFacts) == 0 {
		return AnswerCaseResult{}, errors.New("keyFactsFound requires expectedKeyFacts in the dataset case")
	}

	expected := make(map[string]bool, len(item.ExpectedKeyFacts))
	for _, fact := range item.ExpectedKeyFacts {
		if strings.TrimSpace(fact) == "" {
			return AnswerCaseResult{}, errors.New("expectedKeyFacts cannot contain an empty fact")
		}
		if expected[fact] {
			return AnswerCaseResult{}, fmt.Errorf("duplicate expected key fact %q", fact)
		}
		expected[fact] = true
	}
	found := make(map[string]bool, len(record.KeyFactsFound))
	for _, fact := range record.KeyFactsFound {
		if !expected[fact] {
			return AnswerCaseResult{}, fmt.Errorf("keyFactsFound contains unknown fact %q", fact)
		}
		if found[fact] {
			return AnswerCaseResult{}, fmt.Errorf("duplicate found key fact %q", fact)
		}
		found[fact] = true
	}
	answer.KeyFactScored = true
	answer.ExpectedKeyFacts = len(expected)
	answer.FoundKeyFacts = len(found)
	answer.KeyFactCoverage = float64(len(found)) / float64(len(expected))
	answer.KeyFactsFound = append([]string(nil), record.KeyFactsFound...)
	return answer, nil
}

func totalTokens(values ...int64) (int64, error) {
	var total int64
	for _, value := range values {
		if value > math.MaxInt64-total {
			return 0, errors.New("total agent tokens overflow int64")
		}
		total += value
	}
	return total, nil
}

func aggregateAnswers(answers []AnswerCaseResult) (AnswerMetrics, error) {
	metrics := AnswerMetrics{Cases: len(answers)}
	for _, answer := range answers {
		if answer.Correct != nil {
			metrics.JudgedCases++
			if *answer.Correct {
				metrics.CorrectCases++
			}
		}
		if answer.KeyFactScored {
			metrics.KeyFactCases++
			metrics.MeanKeyFactCoverage += answer.KeyFactCoverage
		}
		var err error
		metrics.TotalInputTokens, err = totalTokens(metrics.TotalInputTokens, answer.InputTokens)
		if err != nil {
			return AnswerMetrics{}, errors.New("total input tokens overflow int64")
		}
		metrics.TotalOutputTokens, err = totalTokens(metrics.TotalOutputTokens, answer.OutputTokens)
		if err != nil {
			return AnswerMetrics{}, errors.New("total output tokens overflow int64")
		}
		metrics.TotalToolTokens, err = totalTokens(metrics.TotalToolTokens, answer.ToolTokens)
		if err != nil {
			return AnswerMetrics{}, errors.New("total tool tokens overflow int64")
		}
		metrics.TotalAgentTokens, err = totalTokens(metrics.TotalAgentTokens, answer.TotalAgentTokens)
		if err != nil {
			return AnswerMetrics{}, errors.New("total agent tokens overflow int64")
		}
		metrics.TotalCostUSD += answer.CostUSD
		if math.IsInf(metrics.TotalCostUSD, 0) || math.IsNaN(metrics.TotalCostUSD) {
			return AnswerMetrics{}, errors.New("total costUsd overflow")
		}
	}
	if metrics.JudgedCases > 0 {
		metrics.Accuracy = float64(metrics.CorrectCases) / float64(metrics.JudgedCases)
	}
	if metrics.KeyFactCases > 0 {
		metrics.MeanKeyFactCoverage /= float64(metrics.KeyFactCases)
	}
	if metrics.Cases > 0 {
		metrics.MeanTotalAgentTokens = float64(metrics.TotalAgentTokens) / float64(metrics.Cases)
	}
	return metrics, nil
}
