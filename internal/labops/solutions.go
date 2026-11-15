package labops

import (
	"sort"
	"strings"

	"astrona/internal/config"

	"gopkg.in/yaml.v3"
)

// Solution is one of a lab's solutions as `astrona docs solutions` and
// the browser workspace list them: the default (metadata.docs.solution,
// else solution.md) and every solution.<name>.md. Title, Summary and
// Order come from the file's front matter.
type Solution struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
	Order   *int   `json:"order,omitempty"`
	File    string `json:"file"`
	// Body is the solution's Markdown, sanitized, without front matter.
	Body string `json:"-"`
	// Err is why the file could not be read ("" Body then).
	Err error `json:"-"`
}

// LabSolutions reads every solution of the lab with read (a file path
// relative to config.yaml → its text) and returns them in display order
// (SortSolutions). A solution that cannot be read is kept with Err set,
// titled by its name, so the caller decides whether that is fatal.
func LabSolutions(cfg *config.LabConfig, read func(file string) (string, error)) []Solution {
	var files []config.SolutionFile
	if f := cfg.Metadata.Docs.Solution; f != "" {
		files = append(files, config.SolutionFile{Name: config.DefaultSolutionName, File: f})
	}
	files = append(files, cfg.Metadata.Docs.Solutions...)
	out := make([]Solution, 0, len(files))
	for _, f := range files {
		s := Solution{Name: f.Name, File: f.File}
		text, err := read(f.File)
		if err != nil {
			s.Err = err
			s.Title = fallbackSolutionTitle(f.Name)
			out = append(out, s)
			continue
		}
		meta, body := DocFrontMatter(SanitizeTerminalText(text))
		s.Body = body
		if t, ok := meta["title"].(string); ok {
			s.Title = SanitizeLine(strings.TrimSpace(t))
		}
		if s.Title == "" {
			s.Title = firstHeading(body)
		}
		if s.Title == "" {
			s.Title = fallbackSolutionTitle(f.Name)
		}
		if sum, ok := meta["summary"].(string); ok {
			s.Summary = SanitizeLine(strings.TrimSpace(sum))
		}
		if o, ok := meta["order"].(int); ok {
			s.Order = &o
		}
		out = append(out, s)
	}
	SortSolutions(out)
	return out
}

// SortSolutions puts solutions in display order: those with an order
// first, ascending, ties by title then name; then those without, the
// default first, then by title, then name.
func SortSolutions(list []Solution) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if (a.Order != nil) != (b.Order != nil) {
			return a.Order != nil
		}
		if a.Order != nil && *a.Order != *b.Order {
			return *a.Order < *b.Order
		}
		if a.Order == nil {
			if ad, bd := a.Name == config.DefaultSolutionName, b.Name == config.DefaultSolutionName; ad != bd {
				return ad
			}
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.Name < b.Name
	})
}

func fallbackSolutionTitle(name string) string {
	if name == config.DefaultSolutionName {
		return "Solution"
	}
	return name
}

// firstHeading is the text of the first "# " heading of a Markdown doc,
// outside fenced code ("" when there is none).
func firstHeading(body string) string {
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if !fenced && strings.HasPrefix(line, "# ") {
			return SanitizeLine(strings.TrimSpace(strings.TrimPrefix(line, "# ")))
		}
	}
	return ""
}

// DocFrontMatter splits a YAML front-matter block — a "---" line, YAML,
// a "---" line — from the very start of a doc. A "---" anywhere else is a
// horizontal rule and stays, as does a leading block that isn't YAML
// (nil meta then). Input must be sanitized first.
func DocFrontMatter(src string) (map[string]any, string) {
	lines := strings.Split(src, "\n")
	if len(lines) < 2 || strings.TrimRight(lines[0], " ") != "---" {
		return nil, src
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " ") == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, src
	}
	var meta map[string]any
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &meta); err != nil {
		return nil, src
	}
	return meta, strings.TrimLeft(strings.Join(lines[end+1:], "\n"), "\n")
}
