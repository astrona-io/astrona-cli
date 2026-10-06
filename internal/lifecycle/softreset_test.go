package lifecycle

import (
	"strings"
	"testing"
)

func TestLabNamespaces(t *testing.T) {
	baseline := []string{"default", "kube-system", "kube-public", "kube-node-lease", "local-path-storage", "cert-manager"}
	current := []string{"auth", "cert-manager", "default", "kube-node-lease", "kube-public", "kube-system", "lab-ns", "local-path-storage"}
	if got := strings.Join(labNamespaces(current, baseline), ","); got != "auth,lab-ns" {
		t.Errorf("labNamespaces = %s — only what came after the baseline, never default", got)
	}
	if got := labNamespaces([]string{"default"}, nil); len(got) != 0 {
		t.Errorf("default must never be deleted: %v", got)
	}
}
