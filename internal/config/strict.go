package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// UnknownField is a key in a lab config that no config field reads — almost
// always a typo ("waitfor", "manifest") that would otherwise be silently
// ignored, leaving the lab without whatever the author meant to configure.
type UnknownField struct {
	Line       int
	Field      string
	In         string // where it appeared, e.g. "bootstrap/testing"
	Suggestion string // closest known field, "" if nothing is close
}

func (u UnknownField) String() string {
	s := fmt.Sprintf("line %d: unknown field %q in %s", u.Line, u.Field, u.In)
	if u.Suggestion != "" {
		s += fmt.Sprintf(" — did you mean %q?", u.Suggestion)
	}
	return s
}

// yaml.v3 reports each unknown key (with KnownFields) as one line of a
// *yaml.TypeError, e.g. "line 3: field waitfor not found in type
// config.BootstrapConfig".
var unknownFieldPattern = regexp.MustCompile(`^line (\d+): field (\S+) not found in type config\.(\w+)$`)

// typeLabels names config types the way an author knows them (by their
// YAML location) instead of their Go type name.
var typeLabels = map[string]string{
	"LabConfig":        "the top level",
	"MetadataConfig":   "metadata",
	"DocsConfig":       "metadata.docs",
	"RuntimeConfig":    "runtime",
	"KindConfig":       "runtime.kind",
	"KindNodes":        "runtime.kind.nodes",
	"KindNetworking":   "runtime.kind.networking",
	"KindAddons":       "runtime.kind.addons",
	"GatewayPorts":     "runtime.kind.addons.gatewayPorts",
	"PortForward":      "a runtime.portForwards entry",
	"QEMUVM":           "a runtime.qemu entry",
	"BootstrapConfig":  "bootstrap/testing",
	"TeardownConfig":   "teardown",
	"ValidationConfig": "validation",
	"ValidationCheck":  "a validation.checks entry",
	"ResourceItem":     "a script/manifest entry",
	"WaitFor":          "a waitFor entry",
}

// FindUnknownFields re-decodes body strictly and returns every key no
// config field reads. A YAML syntax error is returned as an error (the
// normal loader reports it too).
func FindUnknownFields(body []byte) ([]UnknownField, error) {
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	var scratch LabConfig
	err := dec.Decode(&scratch)

	var typeErr *yaml.TypeError
	if err == nil || errors.Is(err, io.EOF) { // io.EOF: empty document
		return nil, nil
	}
	if !errors.As(err, &typeErr) {
		return nil, err
	}

	types := configTypes()
	var unknown []UnknownField
	for _, msg := range typeErr.Errors {
		m := unknownFieldPattern.FindStringSubmatch(msg)
		if m == nil {
			continue // a type mismatch, not an unknown key — the normal decode reports it
		}
		line, _ := strconv.Atoi(m[1])
		u := UnknownField{Line: line, Field: m[2], In: typeLabels[m[3]]}
		if u.In == "" {
			u.In = m[3]
		}
		if t, ok := types[m[3]]; ok {
			u.Suggestion = closestField(m[2], yamlFieldNames(t))
		}
		unknown = append(unknown, u)
	}
	return unknown, nil
}

// configTypes indexes every struct type reachable from LabConfig by name.
func configTypes() map[string]reflect.Type {
	out := map[string]reflect.Type{}
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || out[t.Name()] != nil {
			return
		}
		out[t.Name()] = t
		for i := 0; i < t.NumField(); i++ {
			walk(t.Field(i).Type)
		}
	}
	walk(reflect.TypeOf(LabConfig{}))
	return out
}

// yamlFieldNames lists the YAML keys a struct type accepts.
func yamlFieldNames(t reflect.Type) []string {
	var names []string
	for i := 0; i < t.NumField(); i++ {
		tag := strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0]
		if tag != "" && tag != "-" {
			names = append(names, tag)
		}
	}
	sort.Strings(names)
	return names
}

// closestField suggests the known field a typo most likely meant:
// a case-insensitive match, a singular/plural slip, or an edit distance of
// at most 2.
func closestField(field string, known []string) string {
	lower := strings.ToLower(field)
	best, bestDist := "", 3
	for _, k := range known {
		kl := strings.ToLower(k)
		if kl == lower || kl == lower+"s" || kl+"s" == lower {
			return k
		}
		if d := editDistance(lower, kl); d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
