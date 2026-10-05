package config

import (
	"fmt"
	"time"
)

// ExamConfig is the top-level exam: block — a time limit and stricter
// grading, for certification-style practice.
type ExamConfig struct {
	// TimeLimit is a Go duration ("2h", "90m"). The clock starts when
	// `astrona run` (or reset) has the lab ready.
	TimeLimit string `yaml:"timeLimit"`
	// HideHints: `astrona submit` never shows check hints.
	HideHints bool `yaml:"hideHints"`
	// Strict: a submission after the time limit can't pass.
	Strict bool `yaml:"strict"`
}

const (
	minExamTime = time.Minute
	maxExamTime = 24 * time.Hour
)

// Enabled reports whether the lab is an exam (has a time limit).
func (e ExamConfig) Enabled() bool { return e.TimeLimit != "" }

// Limit parses TimeLimit. Only call after ValidateExam.
func (e ExamConfig) Limit() time.Duration {
	d, _ := time.ParseDuration(e.TimeLimit)
	return d
}

// ValidateExam checks the exam block.
func ValidateExam(cfg *LabConfig) error {
	e := cfg.Exam
	if !e.Enabled() {
		if e.HideHints || e.Strict {
			return fmt.Errorf("exam.hideHints / exam.strict need exam.timeLimit")
		}
		return nil
	}
	d, err := time.ParseDuration(e.TimeLimit)
	if err != nil {
		return fmt.Errorf("exam.timeLimit '%s' is not a duration (e.g. '2h', '90m'): %w", e.TimeLimit, err)
	}
	if d < minExamTime || d > maxExamTime {
		return fmt.Errorf("exam.timeLimit %s must be between %s and %s", d, minExamTime, maxExamTime)
	}
	return nil
}
