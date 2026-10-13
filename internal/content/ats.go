package content

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// A training repository (Astrona Training Series, ATS) is an astrona.yaml
// manifest listing readings, labs, playgrounds and knowledge checks (quizzes)
// per module. ValidateATS checks it the way astrona.io's content sync reads
// it, so an author finds out before a push what the site would refuse or
// leave out: errors make the sync fail or drop the item, warnings are things
// worth a look.

// ATS item types the site knows.
var atsTypes = map[string]bool{"reading": true, "lab": true, "playground": true, "quiz": true}

// Quiz banks: the limits astrona.io's content sync applies.
const (
	maxQuizQuestions = 30
	minQuizOptions   = 2
	maxQuizOptions   = 6
)

var quizDifficulties = map[string]bool{"beginner": true, "intermediate": true, "advanced": true}

// ATSReport is what ValidateATS found.
type ATSReport struct {
	TrainingID  string
	Readings    int
	Labs        int
	Playgrounds int
	Quizzes     int
	Questions   int
	Errors      []string
	Warnings    []string
}

func (r *ATSReport) errorf(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

func (r *ATSReport) warnf(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

type atsItem struct {
	Type          string `yaml:"type"`
	Title         string `yaml:"title"`
	Path          string `yaml:"path"`
	PassScore     any    `yaml:"pass_score"`
	QuestionCount any    `yaml:"question_count"`
}

type atsModule struct {
	ID      string    `yaml:"id"`
	Title   string    `yaml:"title"`
	Content []atsItem `yaml:"content"`
}

type atsManifest struct {
	Training struct {
		ID    string `yaml:"id"`
		Title string `yaml:"title"`
	} `yaml:"training"`
	Modules  []atsModule `yaml:"modules"`
	Sections []struct {
		ID       string      `yaml:"id"`
		Modules  []atsModule `yaml:"modules"`
		Capstone []atsItem   `yaml:"capstone"`
	} `yaml:"sections"`
}

// placedItem is a manifest item with where it sits, for messages.
type placedItem struct {
	atsItem
	where string
}

func (m atsManifest) items() []placedItem {
	var out []placedItem
	add := func(where string, items []atsItem) {
		for i, it := range items {
			out = append(out, placedItem{it, fmt.Sprintf("%s item %d", where, i+1)})
		}
	}
	for _, mod := range m.Modules {
		add("module "+mod.ID, mod.Content)
	}
	for _, sec := range m.Sections {
		for _, mod := range sec.Modules {
			add("section "+sec.ID+" module "+mod.ID, mod.Content)
		}
		add("section "+sec.ID+" capstone", sec.Capstone)
	}
	return out
}

// cleanRepoPath is p inside the repository, or "" when it is empty, absolute
// or leaves it.
func cleanRepoPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return ""
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return ""
	}
	return c
}

// ValidateATS checks the training repository in dir (its astrona.yaml).
func ValidateATS(dir string) (*ATSReport, error) {
	data, err := os.ReadFile(filepath.Join(dir, "astrona.yaml"))
	if err != nil {
		return nil, fmt.Errorf("astrona.yaml not found in %s: %w", dir, err)
	}
	var m atsManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("astrona.yaml isn't valid YAML: %w", err)
	}
	r := &ATSReport{TrainingID: strings.TrimSpace(m.Training.ID)}
	if r.TrainingID == "" {
		r.errorf("astrona.yaml: training.id is missing")
	}
	if strings.TrimSpace(m.Training.Title) == "" {
		r.warnf("astrona.yaml: training.title is missing")
	}

	items := m.items()
	if len(items) == 0 {
		r.errorf("astrona.yaml: no content items (modules[].content or sections[].modules[].content)")
	}
	readings := map[string]bool{}
	listed := map[string]bool{}
	for _, it := range items {
		if p := cleanRepoPath(it.Path); p != "" {
			listed[p] = true
			if it.Type == "reading" {
				readings[p] = true
			}
		}
	}

	for _, it := range items {
		title := strings.TrimSpace(it.Title)
		label := it.where
		if title != "" {
			label = fmt.Sprintf("%s (%q)", it.where, title)
		}
		if title == "" {
			r.errorf("%s: title is missing", it.where)
		}
		if !atsTypes[it.Type] {
			r.errorf("%s: type %q is unknown — astrona.io takes reading, lab, playground and quiz", label, it.Type)
			continue
		}
		p := cleanRepoPath(it.Path)
		if p == "" {
			r.errorf("%s: path %q is missing or leaves the repository", label, it.Path)
			continue
		}
		full := filepath.Join(dir, filepath.FromSlash(p))
		info, statErr := os.Stat(full)
		if statErr != nil {
			r.errorf("%s: %s does not exist", label, p)
			continue
		}
		switch it.Type {
		case "reading":
			r.Readings++
			if info.IsDir() || !strings.HasSuffix(strings.ToLower(p), ".md") {
				r.errorf("%s: a reading is a Markdown file, not %s", label, p)
			}
		case "lab", "playground":
			if it.Type == "lab" {
				r.Labs++
			} else {
				r.Playgrounds++
			}
			if !info.IsDir() {
				r.errorf("%s: a %s is a directory, not %s", label, it.Type, p)
			} else if _, err := os.Stat(filepath.Join(full, "config.yaml")); err != nil {
				r.errorf("%s: %s has no config.yaml", label, p)
			}
		case "quiz":
			r.Quizzes++
			if info.IsDir() || !strings.HasSuffix(p, ".yaml") && !strings.HasSuffix(p, ".yml") {
				r.errorf("%s: a quiz is a question bank YAML file, not %s", label, p)
				continue
			}
			checkPassScore(r, label, it.PassScore)
			n := validateQuizBank(r, dir, p, label, readings)
			r.Questions += n
			if want, ok := asInt(it.QuestionCount); ok && n > 0 && want != n {
				r.warnf("%s: question_count says %d, the bank has %d usable questions (the site counts the bank)", label, want, n)
			}
		}
	}

	// Quiz banks on disk that the manifest doesn't list never reach the site.
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if !d.IsDir() && (d.Name() == "quiz.yaml" || d.Name() == "quiz.yml") {
			rel, relErr := filepath.Rel(dir, p)
			if relErr == nil && !listed[filepath.ToSlash(rel)] {
				r.warnf("%s is not in astrona.yaml, so it is not on the site", filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(r.Warnings)
	return r, nil
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	}
	return 0, false
}

func checkPassScore(r *ATSReport, label string, v any) {
	if v == nil {
		return
	}
	var score float64
	switch n := v.(type) {
	case int:
		score = float64(n)
	case float64:
		score = n
	default:
		r.errorf("%s: pass_score %v should be a percentage (e.g. 70)", label, v)
		return
	}
	if score <= 0 || score > 100 {
		r.errorf("%s: pass_score %v should be above 0 and at most 100", label, v)
	}
}

type quizBank struct {
	SchemaVersion int    `yaml:"schema_version"`
	Kind          string `yaml:"kind"`
	ID            string `yaml:"id"`
	Status        string `yaml:"status"`
	Questions     []struct {
		ID            string   `yaml:"id"`
		Type          string   `yaml:"type"`
		Difficulty    string   `yaml:"difficulty"`
		Prompt        string   `yaml:"prompt"`
		CorrectOption string   `yaml:"correct_option"`
		Explanation   string   `yaml:"explanation"`
		SourceReading []string `yaml:"source_readings"`
		Options       []struct {
			ID          string `yaml:"id"`
			Text        string `yaml:"text"`
			Explanation string `yaml:"explanation"`
		} `yaml:"options"`
	} `yaml:"questions"`
}

// validateQuizBank checks one bank and returns how many of its questions the
// site would use. A question the site would leave out is an error here: the
// author wrote it to be asked.
func validateQuizBank(r *ATSReport, dir, p, label string, readings map[string]bool) int {
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
	if err != nil {
		r.errorf("%s: %v", label, err)
		return 0
	}
	var bank quizBank
	if err := yaml.Unmarshal(data, &bank); err != nil {
		r.errorf("%s: %s isn't valid YAML: %v", label, p, err)
		return 0
	}
	if bank.Kind != "question_bank" {
		r.errorf("%s: %s: kind must be question_bank, not %q", label, p, bank.Kind)
		return 0
	}
	if bank.SchemaVersion != 1 {
		r.errorf("%s: %s: schema_version must be 1, not %d", label, p, bank.SchemaVersion)
		return 0
	}
	if bank.Status == "draft" {
		r.warnf("%s: %s is status draft — review the questions before it ships", label, p)
	}
	if len(bank.Questions) == 0 {
		r.errorf("%s: %s has no questions", label, p)
		return 0
	}
	if len(bank.Questions) > maxQuizQuestions {
		r.warnf("%s: %s has %d questions; the site uses the first %d", label, p, len(bank.Questions), maxQuizQuestions)
	}
	usable := 0
	seen := map[string]bool{}
	for i, q := range bank.Questions {
		if i >= maxQuizQuestions {
			break
		}
		where := fmt.Sprintf("%s: %s question %d", label, p, i+1)
		if q.ID != "" {
			where = fmt.Sprintf("%s: %s question %s", label, p, q.ID)
		}
		ok := true
		bad := func(format string, args ...any) {
			r.errorf(where+": "+format, args...)
			ok = false
		}
		switch {
		case strings.TrimSpace(q.ID) == "":
			bad("id is missing")
		case seen[q.ID]:
			bad("id repeats an earlier question")
		}
		seen[q.ID] = true
		if q.Type != "" && q.Type != "single_choice" {
			bad("type %q is not supported (single_choice)", q.Type)
		}
		if strings.TrimSpace(q.Prompt) == "" {
			bad("prompt is missing")
		}
		ids := map[string]bool{}
		for _, o := range q.Options {
			if strings.TrimSpace(o.ID) == "" || strings.TrimSpace(o.Text) == "" || ids[o.ID] {
				bad("every option needs a unique id and a text")
				break
			}
			ids[o.ID] = true
			if strings.TrimSpace(o.Explanation) == "" {
				r.warnf("%s: option %s has no explanation", where, o.ID)
			}
		}
		if n := len(q.Options); n < minQuizOptions || n > maxQuizOptions {
			bad("%d options; %d–%d needed", n, minQuizOptions, maxQuizOptions)
		}
		if !ids[q.CorrectOption] {
			bad("correct_option %q is not one of its options", q.CorrectOption)
		}
		if q.Difficulty != "" && !quizDifficulties[strings.ToLower(q.Difficulty)] {
			r.warnf("%s: difficulty %q is not beginner, intermediate or advanced (ignored)", where, q.Difficulty)
		}
		if strings.TrimSpace(q.Explanation) == "" {
			r.warnf("%s: no explanation for the answer", where)
		}
		for _, src := range q.SourceReading {
			sp := cleanRepoPath(src)
			switch {
			case sp == "":
				bad("source_readings %q leaves the repository", src)
			case !fileExists(filepath.Join(dir, filepath.FromSlash(sp))):
				bad("source_readings %s does not exist", sp)
			case !readings[sp]:
				r.warnf("%s: source_readings %s is not a reading in astrona.yaml, so it gets no review link", where, sp)
			}
		}
		if ok {
			usable++
		}
	}
	return usable
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
