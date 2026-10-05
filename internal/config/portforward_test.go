package config

import (
	"strings"
	"testing"
)

func validForward() PortForward {
	return PortForward{Name: "web", Resource: "svc/frontend", HostPort: 8080, TargetPort: 80}
}

func TestPortForwardValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*PortForward)
		wantErr string
	}{
		{"valid", func(*PortForward) {}, ""},
		{"long kind spelling", func(p *PortForward) { p.Resource = "service/frontend" }, ""},
		{"deployment", func(p *PortForward) { p.Resource = "deployment/api" }, ""},
		{"dotted resource name", func(p *PortForward) { p.Resource = "pod/api-0.x" }, ""},
		{"explicit namespace and scheme", func(p *PortForward) { p.Namespace = "data"; p.Scheme = "HTTPS" }, ""},
		{"empty name", func(p *PortForward) { p.Name = "" }, "name"},
		{"name with path separator", func(p *PortForward) { p.Name = "../x" }, "name"},
		{"uppercase name", func(p *PortForward) { p.Name = "Web" }, "name"},
		{"resource without kind", func(p *PortForward) { p.Resource = "frontend" }, "<kind>/<name>"},
		{"unsupported kind", func(p *PortForward) { p.Resource = "node/x" }, "unsupported resource kind"},
		{"resource name injection", func(p *PortForward) { p.Resource = "svc/x --address=0.0.0.0" }, "not a valid Kubernetes name"},
		{"nested resource", func(p *PortForward) { p.Resource = "svc/a/b" }, "not a valid Kubernetes name"},
		{"bad namespace", func(p *PortForward) { p.Namespace = "-bad" }, "namespace"},
		{"privileged host port", func(p *PortForward) { p.HostPort = 80 }, "hostPort"},
		{"host port too high", func(p *PortForward) { p.HostPort = 70000 }, "hostPort"},
		{"zero target port", func(p *PortForward) { p.TargetPort = 0 }, "targetPort"},
		{"bad scheme", func(p *PortForward) { p.Scheme = "ftp" }, "scheme"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pf := validForward()
			c.mutate(&pf)
			err := pf.Validate()
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, c.wantErr)
			}
		})
	}
}

func TestPortForwardNormalized(t *testing.T) {
	got := PortForward{Name: "web", Resource: "Service/frontend", Scheme: "HTTP", HostPort: 8080, TargetPort: 80}.Normalized()
	if got.Resource != "svc/frontend" {
		t.Errorf("Resource = %q, want svc/frontend", got.Resource)
	}
	if got.Namespace != "default" {
		t.Errorf("Namespace = %q, want default", got.Namespace)
	}
	if got.Scheme != "http" {
		t.Errorf("Scheme = %q, want http", got.Scheme)
	}

	if s := validForward().Normalized().Scheme; s != "tcp" {
		t.Errorf("default Scheme = %q, want tcp", s)
	}
}

func TestValidatePortForwards(t *testing.T) {
	t.Run("none is fine on any runtime", func(t *testing.T) {
		if err := ValidatePortForwards(RuntimeConfig{Type: "qemu"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("kind and default runtime accept forwards", func(t *testing.T) {
		for _, typ := range []string{"", "kind"} {
			rt := RuntimeConfig{Type: typ, PortForwards: []PortForward{validForward()}}
			if err := ValidatePortForwards(rt); err != nil {
				t.Fatalf("type %q: unexpected error: %v", typ, err)
			}
		}
	})

	t.Run("qemu rejects forwards", func(t *testing.T) {
		rt := RuntimeConfig{Type: "qemu", PortForwards: []PortForward{validForward()}}
		if err := ValidatePortForwards(rt); err == nil || !strings.Contains(err.Error(), "only supported for the kind runtime") {
			t.Fatalf("error = %v, want kind-only error", err)
		}
	})

	t.Run("duplicate name", func(t *testing.T) {
		b := validForward()
		b.HostPort = 9090
		rt := RuntimeConfig{PortForwards: []PortForward{validForward(), b}}
		if err := ValidatePortForwards(rt); err == nil || !strings.Contains(err.Error(), "duplicate port forward name") {
			t.Fatalf("error = %v, want duplicate name error", err)
		}
	})

	t.Run("duplicate host port", func(t *testing.T) {
		b := validForward()
		b.Name = "api"
		rt := RuntimeConfig{PortForwards: []PortForward{validForward(), b}}
		if err := ValidatePortForwards(rt); err == nil || !strings.Contains(err.Error(), "both use hostPort 8080") {
			t.Fatalf("error = %v, want duplicate port error", err)
		}
	})
}
