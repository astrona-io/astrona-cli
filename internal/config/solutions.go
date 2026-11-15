package config

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

// DefaultSolutionName is the name of a lab's default solution:
// metadata.docs.solution (or the older guide), else solution.md.
const DefaultSolutionName = "default"

// solutionNamePattern is the name part of an extra solution file,
// solution.<name>.md: lowercase words joined by single hyphens.
var solutionNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// SolutionFile is one of a lab's extra solutions: solution.<name>.md next
// to config.yaml. Extra solutions are found by their file name and are
// never listed in config.yaml.
type SolutionFile struct {
	Name string // the <name> in solution.<name>.md
	File string // the file name, relative to config.yaml
}

// ValidSolutionName reports whether name may name an extra solution:
// it matches ^[a-z0-9]+(-[a-z0-9]+)*$ and is not "default", which names
// the default solution.
func ValidSolutionName(name string) bool {
	return name != DefaultSolutionName && solutionNamePattern.MatchString(name)
}

// SolutionFileName is the file an extra solution named name is read from.
func SolutionFileName(name string) string { return "solution." + name + ".md" }

// solutionFileStem returns the <name> of a file named solution.<name>.md
// (any case), and whether the file has that shape at all.
func solutionFileStem(file string) (string, bool) {
	lower := strings.ToLower(file)
	if !strings.HasPrefix(lower, "solution.") || !strings.HasSuffix(lower, ".md") || len(file) <= len("solution..md") {
		return "", false
	}
	return file[len("solution.") : len(file)-len(".md")], true
}

// DiscoverSolutionFiles lists the extra solutions in dir (the folder of
// config.yaml): every regular file solution.<name>.md whose name is
// valid, by name, without defaultFile (the default solution, which may
// itself have that shape). invalid lists files of that shape whose name
// is not valid; they are not solutions, and `astrona validate` names them.
func DiscoverSolutionFiles(dir, defaultFile string) (found []SolutionFile, invalid []string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("list the solutions in %s: %w", dir, err)
	}
	def := path.Clean(defaultFile)
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		name, ok := solutionFileStem(e.Name())
		if !ok || (defaultFile != "" && e.Name() == def) {
			continue
		}
		if !ValidSolutionName(name) || e.Name() != SolutionFileName(name) {
			invalid = append(invalid, e.Name())
			continue
		}
		found = append(found, SolutionFile{Name: name, File: e.Name()})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	sort.Strings(invalid)
	return found, invalid, nil
}
