package diagnostics

import (
	"encoding/json"
	"strings"
	"testing"
)

const podsJSON = `{"items":[
 {"metadata":{"name":"ok","namespace":"default"},"status":{"phase":"Running","containerStatuses":[{"name":"c","ready":true,"restartCount":0,"state":{}}]}},
 {"metadata":{"name":"done","namespace":"default"},"status":{"phase":"Succeeded","containerStatuses":[{"name":"c","ready":false,"restartCount":0,"state":{"terminated":{"reason":"Completed","exitCode":0}}}]}},
 {"metadata":{"name":"pull","namespace":"shop"},"status":{"phase":"Pending","containerStatuses":[{"name":"web","ready":false,"restartCount":0,"state":{"waiting":{"reason":"ImagePullBackOff","message":"Back-off pulling image \"nope:1\"\nmore"}}}]}},
 {"metadata":{"name":"crash","namespace":"shop"},"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":false,"restartCount":4,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}},
 {"metadata":{"name":"unsched","namespace":"default"},"status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"False","reason":"Unschedulable","message":"0/1 nodes are available: 1 Insufficient memory."}]}},
 {"metadata":{"name":"probe","namespace":"default"},"status":{"phase":"Running","containerStatuses":[{"name":"c","ready":false,"restartCount":0,"state":{"running":{}}}]}},
 {"metadata":{"name":"initfail","namespace":"default"},"status":{"phase":"Pending","initContainerStatuses":[{"name":"migrate","ready":false,"restartCount":0,"state":{"terminated":{"reason":"Error","exitCode":2}}}]}}
]}`

func TestFindProblems(t *testing.T) {
	var list podList
	if err := json.Unmarshal([]byte(podsJSON), &list); err != nil {
		t.Fatal(err)
	}
	probs := findProblems(list)

	got := map[string]string{}
	for _, p := range probs {
		got[p.Namespace+"/"+p.Name] = p.Reason
	}
	if _, ok := got["default/ok"]; ok {
		t.Error("healthy running pod reported")
	}
	if _, ok := got["default/done"]; ok {
		t.Error("succeeded pod reported")
	}

	want := map[string]string{
		"shop/pull":        `web: ImagePullBackOff (Back-off pulling image "nope:1")`,
		"shop/crash":       "app: CrashLoopBackOff; app restarted 4×",
		"default/unsched":  "Unschedulable (0/1 nodes are available: 1 Insufficient memory.)",
		"default/probe":    "c: not ready",
		"default/initfail": "migrate: Error (exit 2)",
	}
	for pod, reason := range want {
		if got[pod] != reason {
			t.Errorf("%s: reason = %q, want %q", pod, got[pod], reason)
		}
	}
	if len(probs) != len(want) {
		t.Errorf("got %d problem pods, want %d: %v", len(probs), len(want), got)
	}

	for _, p := range probs {
		if p.Name == "crash" && (len(p.Containers) != 1 || p.Containers[0].Restarts != 4 || p.Containers[0].NoLogs) {
			t.Errorf("crash containers = %+v, want restarts recorded for --previous logs", p.Containers)
		}
		if p.Name == "pull" && !p.Containers[0].NoLogs {
			t.Errorf("image-pull container should be marked NoLogs: %+v", p.Containers)
		}
	}
}

func TestRenderSummary(t *testing.T) {
	sum := Summary{Problems: []string{"shop/pull: web: ImagePullBackOff"}, Warnings: 30}
	var warnings []string
	for i := range 30 {
		warnings = append(warnings, "w"+strings.Repeat("x", i))
	}
	out := string(renderSummary("astro-x", sum, warnings, true))

	for _, want := range []string{"# Diagnostics: astro-x", "## Unhealthy pods (1)", "- shop/pull: web: ImagePullBackOff", "first 25 only", "## Warning events (30, newest last)", "kind-logs/"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "- w\n") {
		t.Error("oldest warning events should be trimmed to the newest 25")
	}

	empty := string(renderSummary("astro-x", Summary{}, nil, false))
	if strings.Count(empty, "None.") != 2 {
		t.Errorf("empty summary should say None. twice:\n%s", empty)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("  a\nb"); got != "a" {
		t.Errorf("firstLine = %q", got)
	}
	if got := firstLine(strings.Repeat("x", 300)); len(got) > 205 {
		t.Errorf("firstLine not truncated: %d bytes", len(got))
	}
}
