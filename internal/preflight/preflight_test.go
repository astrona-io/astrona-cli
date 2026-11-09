package preflight

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
	if err != nil || pod != (EngineInfo{MemBytes: 10386202624, CPUs: 6, Cgroup: "2", Rootless: true}) {
		t.Fatalf("podman = %+v, %v", pod, err)
	}
	dock, err := parseEngineInfo("docker", "8216641536|4|2|[name=seccomp,profile=builtin name=rootless name=cgroupns]")
	if err != nil || dock != (EngineInfo{MemBytes: 8216641536, CPUs: 4, Cgroup: "2", Rootless: true}) {
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

func statusOf(res []Result, name string) Status {
	for _, r := range res {
		if strings.HasSuffix(r.Name, name) {
			return r.Status
		}
	}
	return -1
}

func TestEvaluateEngine(t *testing.T) {
	cases := []struct {
		info              EngineInfo
		mem, cpu, cgroups Status
	}{
		{EngineInfo{MemBytes: 8 * gib, CPUs: 4, Cgroup: "2"}, OK, OK, -1},
		{EngineInfo{MemBytes: 3 * gib, CPUs: 1, Cgroup: "2"}, Warn, Warn, -1},
		{EngineInfo{MemBytes: 1 * gib, CPUs: 2, Cgroup: "2"}, Fail, OK, -1},
		{EngineInfo{MemBytes: 8 * gib, CPUs: 4, Cgroup: "1", Rootless: true}, OK, OK, Fail},
		{EngineInfo{MemBytes: 8 * gib, CPUs: 4, Cgroup: "2", Rootless: true}, OK, OK, OK},
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

	res := CheckInotify(proc)
	if got := statusOf(res, "max_user_watches"); got != Warn {
		t.Errorf("low max_user_watches = %d, want warn", got)
	}
	if got := statusOf(res, "max_user_instances"); got != OK {
		t.Errorf("max_user_instances = %d, want ok", got)
	}
	for _, r := range res {
		if r.Status == Warn && !strings.Contains(r.Hint, "sysctl fs.inotify.max_user_watches=524288") {
			t.Errorf("hint = %q", r.Hint)
		}
	}

	if got := statusOf(CheckInotify(t.TempDir()), "max_user_watches"); got != Warn {
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

	if r := evaluateLabMemory(nil, nil, 0, 8*gib); r.Status != OK {
		t.Errorf("small lab on 8 GiB = %d (%s)", r.Status, r.Detail)
	}
	if r := evaluateLabMemory(nil, []*config.KindConfig{nil, nil}, 0, 8*gib); !strings.Contains(r.Detail, "2 linked cluster(s)") {
		t.Errorf("linked labs not counted: %s", r.Detail)
	}
	r := evaluateLabMemory(nil, nil, 4, 6*gib) // 5 × 1.2 GiB on 6 GiB
	if r.Status != Warn || !strings.Contains(r.Detail, "4 running Kind cluster(s)") {
		t.Errorf("crowded engine = %d (%s)", r.Status, r.Detail)
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
	if res[0].Status != Fail || !strings.Contains(res[0].Hint, "lsof") {
		t.Errorf("busy port = %+v", res[0])
	}
}
