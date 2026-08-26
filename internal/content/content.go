// Package content parses and validates Astrona content manifests — today
// just the Training Path (ATP) "path.yaml" shape. It is intentionally
// separate from internal/config: lab config.yaml describes a runnable lab,
// path.yaml describes a course structure that references other content
// repositories, and the two evolve independently.
package content

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// TrainingPathAPIVersion and TrainingPathKind are the only apiVersion/kind
// pair path.yaml is currently allowed to declare. Checked up front so a
// malformed or wrong-shape manifest fails fast with a clear message instead
// of silently zero-valuing every field during YAML decode.
const (
	TrainingPathAPIVersion = "content.astrona.io/v1alpha1"
	TrainingPathKind       = "TrainingPath"
)

// TrainingPathAuthor is one entry of metadata.authors.
type TrainingPathAuthor struct {
	Name    string `yaml:"name"`
	Email   string `yaml:"email"`
	Company string `yaml:"company"`
}

// TrainingPathMetadata is the metadata block of a path.yaml.
type TrainingPathMetadata struct {
	ID          string               `yaml:"id"`
	Slug        string               `yaml:"slug"`
	Title       string               `yaml:"title"`
	Description string               `yaml:"description"`
	Version     string               `yaml:"version"`
	Language    string               `yaml:"language"`
	Authors     []TrainingPathAuthor `yaml:"authors"`
	Tags        []string             `yaml:"tags"`
	License     string               `yaml:"license"`
}

// ExamPreparation is spec.examPreparation.
type ExamPreparation struct {
	Enabled             bool   `yaml:"enabled"`
	Title               string `yaml:"title"`
	OfficialSyllabusURL string `yaml:"officialSyllabusUrl"`
	ExamID              string `yaml:"examId"`
	Provider            string `yaml:"provider"`
}

// StageContent is one entry of stages[].content — a reference to an external
// content repository (typically an ATS lab) pulled into the stage.
type StageContent struct {
	Ref        string `yaml:"ref"`
	Repository string `yaml:"repository"`
	Path       string `yaml:"path"`
	Version    string `yaml:"version"`
}

// Stage is one entry of spec.stages.
type Stage struct {
	ID               string         `yaml:"id"`
	Title            string         `yaml:"title"`
	Description      string         `yaml:"description"`
	Weight           int            `yaml:"weight"`
	LearningOutcomes []string       `yaml:"learningOutcomes"`
	Content          []StageContent `yaml:"content"`
}

// TrainingPathSpec is the spec block of a path.yaml.
type TrainingPathSpec struct {
	Audience          string          `yaml:"audience"`
	Difficulty        string          `yaml:"difficulty"`
	EstimatedDuration string          `yaml:"estimatedDuration"`
	ExamPreparation   ExamPreparation `yaml:"examPreparation"`
	Prerequisites     []string        `yaml:"prerequisites"`
	LearningOutcomes  []string        `yaml:"learningOutcomes"`
	Stages            []Stage         `yaml:"stages"`
}

// TrainingPath is the full shape of a path.yaml (ATP) manifest.
type TrainingPath struct {
	APIVersion string               `yaml:"apiVersion"`
	Kind       string               `yaml:"kind"`
	Metadata   TrainingPathMetadata `yaml:"metadata"`
	Spec       TrainingPathSpec     `yaml:"spec"`
}

// LoadTrainingPath reads and parses a path.yaml from disk and checks its
// apiVersion/kind match the only shape this CLI understands, before any
// caller starts walking stages/content off of possibly-wrong-shaped data.
func LoadTrainingPath(path string) (*TrainingPath, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	var tp TrainingPath
	if err := yaml.Unmarshal(data, &tp); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	if tp.APIVersion != TrainingPathAPIVersion {
		return nil, fmt.Errorf("%s: unsupported apiVersion %q, expected %q", path, tp.APIVersion, TrainingPathAPIVersion)
	}
	if tp.Kind != TrainingPathKind {
		return nil, fmt.Errorf("%s: unsupported kind %q, expected %q", path, tp.Kind, TrainingPathKind)
	}

	return &tp, nil
}
