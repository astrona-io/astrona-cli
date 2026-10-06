// Package version parses astrona release versions (v0.2.1, 0.3.0-rc1)
// and the constraints a lab config puts on them (astronaVersion).
package version

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// V is a release version: major.minor.patch with an optional pre-release
// (rc1, beta.2), which sorts before the release itself.
type V struct {
	Major, Minor, Patch int
	Pre                 string
}

var pattern = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)(?:\.([0-9]+))?(?:-([0-9A-Za-z.-]+))?$`)

// Parse reads "v0.2.1", "0.2.1", "0.2" (patch 0) or "0.3.0-rc1".
func Parse(s string) (V, error) {
	m := pattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return V{}, fmt.Errorf("'%s' is not a version like 0.2.1", s)
	}
	n := func(x string) int { v, _ := strconv.Atoi(x); return v }
	return V{Major: n(m[1]), Minor: n(m[2]), Patch: n(m[3]), Pre: m[4]}, nil
}

func (v V) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare is -1, 0 or 1 as v is older than, equal to or newer than o.
func (v V) Compare(o V) int {
	for _, d := range [][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case v.Pre == o.Pre:
		return 0
	case v.Pre == "":
		return 1 // a release is newer than its pre-releases
	case o.Pre == "":
		return -1
	case v.Pre < o.Pre:
		return -1
	default:
		return 1
	}
}

type comparison struct {
	op string
	v  V
}

// Constraint is what versions a lab runs on: comparisons that must all
// hold, e.g. ">=0.2.0, <0.3.0" or "<=0.2.1". A bare version means exactly
// that one.
type Constraint struct {
	raw string
	all []comparison
}

var opPattern = regexp.MustCompile(`^(>=|<=|!=|>|<|=)?\s*(.+)$`)

// ParseConstraint reads a comma-separated list of comparisons.
func ParseConstraint(s string) (Constraint, error) {
	c := Constraint{raw: strings.TrimSpace(s)}
	if c.raw == "" {
		return c, fmt.Errorf("empty version constraint")
	}
	for _, part := range strings.Split(c.raw, ",") {
		m := opPattern.FindStringSubmatch(strings.TrimSpace(part))
		if m == nil {
			return c, fmt.Errorf("'%s' isn't a comparison like >=0.2.0", part)
		}
		v, err := Parse(m[2])
		if err != nil {
			return c, fmt.Errorf("in '%s': %w", c.raw, err)
		}
		op := m[1]
		if op == "" {
			op = "="
		}
		c.all = append(c.all, comparison{op, v})
	}
	return c, nil
}

// Allows reports whether v satisfies every comparison.
func (c Constraint) Allows(v V) bool {
	for _, cmp := range c.all {
		r := v.Compare(cmp.v)
		ok := map[string]bool{"=": r == 0, "!=": r != 0, ">": r > 0, ">=": r >= 0, "<": r < 0, "<=": r <= 0}[cmp.op]
		if !ok {
			return false
		}
	}
	return true
}

func (c Constraint) String() string { return c.raw }
