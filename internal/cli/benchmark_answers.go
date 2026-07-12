package cli

import (
	"fmt"

	"github.com/12vault/ravel/internal/evaluation"
)

func attachAnswerLedger(report *evaluation.Report, cases []evaluation.Case, path string) error {
	if path == "" {
		return nil
	}
	records, hash, err := evaluation.LoadAnswerJSONLWithHash(path)
	if err != nil {
		return fmt.Errorf("load answer ledger: %w", err)
	}
	if err := evaluation.AttachAnswerQuality(report, cases, records); err != nil {
		return fmt.Errorf("attach answer ledger: %w", err)
	}
	report.AnswerSHA256 = hash
	return nil
}
