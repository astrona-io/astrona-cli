package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
)

func TestParseEngineInfo(t *testing.T) {
	pod, err := parseEngineInfo("podman", "10386202624|6|v2|true\n")
	if err != nil || pod != (engineInfo{MemBytes: 10386202624, CPUs: 6, Cgroup: "2", Rootless: true}) {
		t.Fatalf("podman = %+v, %v", pod, err)
	}
	dock, err := parseEngineInfo("docker", "8216641536|4|2|[name=seccomp,profile=builtin name=rootless name=cgroupns]")
	if err != nil || dock != (engineInfo{MemBytes: 8216641536, CPUs: 4, Cgroup: "2", Rootless: true}) {
		t.Fatalf("docker = %+v, %v", dock, err)
	}
	rootful, _ := parseEngineInfo("docker", "8216641536|4|2|[name=seccomp,profile=builtin]")
	if rootful.Rootless {
		t.Error("rootful docker reported as rootless")
	}
	for _, bad := range []string{"", "1|2|3", "x|4|2|true", "1|x|2|true"} {
		if _, err := parseEngineInfo("podman", bad); err == nil {
			t.Errorf("parseEngineInfo(%q) accepted garbage", bad)
		}
	}
}

func statusOf(res []checkResult, name string) checkStatus {
	for _, r := range res {
		if strings.HasSuffix(r.name, name) {
			return r.status
		}
	}
	return -1
}

func TestEvaluateEngine(t *testing.T) {
	cases := []struct {
		info              engineInfo
		mem, cpu, cgroups checkStatus
	}{
		{engineInfo{MemBytes: 8 * gib, CPUs: 4, Cgroup: "2"}, checkOK, checkOK, -1},
		{engineInfo{MemBytes: 3 * gib, CPUs: 1, Cgroup: "2"}, checkWarn, checkWarn, -1},
		{engineInfo{MemBytes: 1 * gib, CPUs: 2, Cgroup: "2"}, checkFail, checkOK, -1},
		{engineInfo{MemBytes: 8 * gib, CPUs: 4, Cgroup: "1", Rootless: true}, checkOK, checkOK, checkFail},
		{engineInfo{MemBytes: 8 * gib, CPUs: 4, Cgroup: "2", Rootless: true}, checkOK, checkOK, checkOK},
	}
	for _, c := range cases {
		res := evaluateEngine("podman", c.info)
		if got := statusOf(res, "memory"); got != c.mem {
			t.Errorf("%+v: memory = %d, want %d", c.info, got, c.mem)
		}
		if got := statusOf(res, "CPUs"); got != c.cpu {
			t.Errorf("%+v: CPUs = %d, want %d", c.info, got, c.cpu)
		}
		if got := statusOf(res, "rootless cgroups"); got != c.cgroups {
			t.Errorf("%+v: cgroups = %d, want %d", c.info, got, c.cgroups)
		}
	}
}

func TestCheckInotify(t *testing.T) {
	proc := t.TempDir()
	dir := filepath.Join(proc, "sys", "fs", "inotify")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "max_user_watches"), []byte("8192\n"), 0600)
	os.WriteFile(filepath.Join(dir, "max_user_instances"), []byte("1024\n"), 0600)

	res := checkInotify(proc)
	if got := statusOf(res, "max_user_watches"); got != checkWarn {
		t.Errorf("low max_user_watches = %d, want warn", got)
	}
	if got := statusOf(res, "max_user_instances"); got != checkOK {
		t.Errorf("max_user_instances = %d, want ok", got)
	}
	for _, r := range res {
		if r.status == checkWarn && !strings.Contains(r.hint, "sysctl fs.inotify.max_user_watches=524288") {
			t.Errorf("hint = %q", r.hint)
		}
	}

	if got := statusOf(checkInotify(t.TempDir()), "max_user_watches"); got != checkWarn {
		t.Errorf("unreadable limit = %d, want warn (not a crash, not a fail)", got)
	}
}

func TestLabMemory(t *testing.T) {
	small := estimateLabMemory(nil)
	big := estimateLabMemory(&config.KindConfig{
		Nodes:  config.KindNodes{ControlPlanes: 3, Workers: 6},
		Addons: config.KindAddons{CNI: "calico", CertManager: true, MetricsServer: true, GatewayAPI: "envoy"},
	})
	if small != 1200*mib {
		t.Errorf("default lab = %d MiB", small/mib)
	}
	if big <= 6*gib || big >= 8*gib {
		t.Errorf("big lab = %.1f GiB, expected ~7", inGiB(big))
	}

	if r := evaluateLabMemory(nil, nil, 0, 8*gib); r.status != checkOK {
		t.Errorf("small lab on 8 GiB = %d (%s)", r.status, r.detail)
	}
	if r := evaluateLabMemory(nil, []*config.KindConfig{nil, nil}, 0, 8*gib); !strings.Contains(r.detail, "2 linked cluster(s)") {
		t.Errorf("linked labs not counted: %s", r.detail)
	}
	r := evaluateLabMemory(nil, nil, 4, 6*gib) // 5 × 1.2 GiB on 6 GiB
	if r.status != checkWarn || !strings.Contains(r.detail, "4 running lab(s)") {
		t.Errorf("crowded engine = %d (%s)", r.status, r.detail)
	}
}

func TestHostPorts(t *testing.T) {
	rt := config.RuntimeConfig{
		Kind:         &config.KindConfig{Addons: config.KindAddons{GatewayAPI: "envoy"}},
		PortForwards: []config.PortForward{{Name: "web", HostPort: 18000}},
	}
	ports := labHostPorts(rt)
	if len(ports) != 3 || ports[8080] != "gateway http" || ports[8443] != "gateway https" || ports[18000] != "port forward 'web'" {
		t.Fatalf("labHostPorts = %v", ports)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	busy := l.Addr().(*net.TCPAddr).Port

	res := checkHostPorts(map[int]string{busy: "taken"})
	if res[0].status != checkFail || !strings.Contains(res[0].hint, "lsof") {
		t.Errorf("busy port = %+v", res[0])
	}
}

// Only "no lab here" is silent; a lab that is there but won't load is an
// error even when nothing was named.
func TestLoadLabForCheck(t *testing.T) {
	empty := t.TempDir()
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "config.yaml"), []byte("metadata: [not, a, map\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name     string
		dir      string
		flags    rootFlags
		explicit bool
		wantErr  bool
	}{
		{"no lab here", empty, rootFlags{configPath: ".", fileName: "config.yaml"}, false, false},
		{"malformed config here", bad, rootFlags{configPath: ".", fileName: "config.yaml"}, false, true},
		{"astrona use lab gone", empty, rootFlags{configPath: filepath.Join(empty, "gone"), fileName: "config.yaml", fromCurrent: true}, false, true},
		{"explicit -c missing", empty, rootFlags{configPath: filepath.Join(empty, "gone"), fileName: "config.yaml"}, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Chdir(c.dir)
			flags := c.flags
			cfg, _, cleanup, err := loadLabForCheck(&flags, c.explicit)
			cleanup()
			if cfg != nil || (err != nil) != c.wantErr {
				t.Fatalf("cfg=%v err=%v", cfg, err)
			}
		})
	}
}
