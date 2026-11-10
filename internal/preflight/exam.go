package preflight

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/config"
)

// ErrExamTooBig: the exam's clusters don't fit the container engine's
// memory. For a lab, going over the headroom is a warning; for a full exam
// it is a refusal, since a cluster killed for memory an hour into the clock
// costs the attempt.
var ErrExamTooBig = errors.New("not enough memory for this exam")

// ExamMemory is the exam preflight's verdict: what count clusters of
// profile are estimated to use, what other running kind clusters use, and
// what the engine has.
type ExamMemory struct {
	Clusters      int
	ExamBytes     int64
	OtherClusters int
	OtherBytes    int64
	EngineBytes   int64
}

// NeededBytes is the engine memory the exam needs: everything running,
// within the headroom.
func (m ExamMemory) NeededBytes() int64 {
	return int64(float64(m.ExamBytes+m.OtherBytes) / labMemoryHeadroom)
}

// Fits reports whether the exam fits within the headroom.
func (m ExamMemory) Fits() bool {
	return float64(m.ExamBytes+m.OtherBytes) <= labMemoryHeadroom*float64(m.EngineBytes)
}

// Summary is one line for the terminal: the estimate and the engine.
func (m ExamMemory) Summary() string {
	s := fmt.Sprintf("%d kind clusters, about %.1f GiB", m.Clusters, inGiB(m.ExamBytes))
	if m.OtherClusters > 0 {
		s += fmt.Sprintf(", plus %d other running cluster(s) at about %.1f GiB", m.OtherClusters, inGiB(m.OtherBytes))
	}
	return s + fmt.Sprintf("; the container engine has %.1f GiB", inGiB(m.EngineBytes))
}

// Err is nil when the exam fits, else ErrExamTooBig with what it needs.
func (m ExamMemory) Err() error {
	if m.Fits() {
		return nil
	}
	others := ""
	if m.OtherClusters > 0 {
		others = fmt.Sprintf(", and %d other kind cluster(s) already running use about %.1f GiB", m.OtherClusters, inGiB(m.OtherBytes))
	}
	return fmt.Errorf("%w: this exam prepares %d kind clusters, about %.1f GiB%s. Your container engine has %.1f GiB; the exam needs at least %.1f GiB. Give the engine more memory, or stop other clusters (astrona list)",
		ErrExamTooBig, m.Clusters, inGiB(m.ExamBytes), others, inGiB(m.EngineBytes), inGiB(m.NeededBytes()))
}

// EstimateExamMemory is the exam preflight without the engine: count
// clusters built from profile.
func EstimateExamMemory(profile *config.KindConfig, count int) int64 {
	return int64(count) * estimateLabMemory(profile)
}

// CheckExamMemory measures count clusters of profile against the engine's
// memory, counting every running kind cluster except those in exclude (the
// exam's own, when it is resumed). The error is for an engine that can't
// be asked; whether it fits is the verdict's Err.
func CheckExamMemory(profile *config.KindConfig, count int, exclude []string) (ExamMemory, error) {
	m := ExamMemory{Clusters: count, ExamBytes: EstimateExamMemory(profile, count)}
	res, info := CheckEngine()
	if info == nil {
		detail := "the container engine did not answer"
		if len(res) > 0 {
			detail = res[0].Detail
			if res[0].Hint != "" {
				detail += " (" + res[0].Hint + ")"
			}
		}
		return m, fmt.Errorf("can't check the exam's memory: %s", detail)
	}
	m.EngineBytes = info.MemBytes
	engine, err := cluster.DetectContainerEngine()
	if err != nil {
		return m, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, engine.Path, "ps", "--filter", "label=io.x-k8s.kind.cluster", "--format",
		`{{.Label "io.x-k8s.kind.cluster"}}|{{.Label "io.x-k8s.kind.role"}}`).Output()
	if err != nil {
		return m, fmt.Errorf("list Kind nodes: %w", err)
	}
	skip := map[string]bool{}
	for _, n := range exclude {
		skip[n] = true
	}
	others := parseOtherKindMemory(string(out), skip)
	m.OtherClusters, m.OtherBytes = others.clusters, others.bytes
	return m, nil
}
