package config

import (
	"strings"
	"testing"
	"time"
)

func TestValidateExam(t *testing.T) {
	cases := []struct {
		e       ExamConfig
		wantErr string
	}{
		{ExamConfig{}, ""},
		{ExamConfig{TimeLimit: "2h", HideHints: true, Strict: true}, ""},
		{ExamConfig{TimeLimit: "soon"}, "not a duration"},
		{ExamConfig{TimeLimit: "30s"}, "between"},
		{ExamConfig{TimeLimit: "48h"}, "between"},
		{ExamConfig{Strict: true}, "need exam.timeLimit"},
	}
	for _, c := range cases {
		err := ValidateExam(&LabConfig{Exam: c.e})
		if (c.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%+v: err = %v, want %q", c.e, err, c.wantErr)
		}
	}
	if !(ExamConfig{TimeLimit: "90m"}).Enabled() || (ExamConfig{}).Enabled() || (ExamConfig{TimeLimit: "90m"}).Limit() != 90*time.Minute {
		t.Error("Enabled/Limit wrong")
	}
}
