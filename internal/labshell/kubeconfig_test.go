package labshell

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"astrona/internal/cluster"
	"gopkg.in/yaml.v3"
)

func TestLinkedResetRefreshesCredentialsAndKeepsPreferences(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, path, err := prepareWorkspace("astro-parent")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	lab := "astro-parent-idp"
	source, err := cluster.KubeconfigPath(lab)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
		t.Fatal(err)
	}
	original := kubeFixture(lab)
	if err := os.WriteFile(source, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyKubeconfig(root, lab); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, "work", ".kube", lab+".yaml")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var edited map[string]any
	if err := yaml.Unmarshal(data, &edited); err != nil {
		t.Fatal(err)
	}
	edited["current-context"] = "my-context"
	edited["preferences"] = map[string]any{"colors": true}
	edited["contexts"] = []any{map[string]any{
		"name":    "my-context",
		"context": map[string]any{"cluster": "kind-" + lab, "user": "kind-" + lab, "namespace": "student-work"},
	}}
	extraUser := map[string]any{"name": "student", "user": map[string]any{"token": "in-lab-token"}}
	edited["users"] = append(edited["users"].([]any), extraUser)
	data, err = yaml.Marshal(edited)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	fresh := strings.NewReplacer("S0VZ", "TkVX", "Q0VSVA==", "TkVXQ0VSVA==", "Q0E=", "TkVXQ0E=").Replace(original)
	if err := os.WriteFile(source, []byte(fresh), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyKubeconfig(root, lab); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"current-context", "contexts", "preferences"} {
		if !reflect.DeepEqual(got[key], edited[key]) {
			t.Errorf("changed student %s: got %#v, want %#v", key, got[key], edited[key])
		}
	}
	users := got["users"].([]any)
	if !reflect.DeepEqual(users[1], extraUser) {
		t.Fatal("changed student's additional credentials")
	}
	user := users[0].(map[string]any)["user"].(map[string]any)
	cluster := got["clusters"].([]any)[0].(map[string]any)["cluster"].(map[string]any)
	if user["client-key-data"] != "TkVX" || user["client-certificate-data"] != "TkVXQ0VSVA==" || cluster["certificate-authority-data"] != "TkVXQ0E=" {
		t.Fatalf("credentials were not refreshed: %s", data)
	}
	if err := copyKubeconfig(root, lab); err != nil {
		t.Fatal(err)
	}
	if again, err := os.ReadFile(file); err != nil || string(again) != string(data) {
		t.Fatalf("unchanged credentials rewrote user config: %v", err)
	}
}

func TestCredentialRefreshRejectsMalformedWorkspace(t *testing.T) {
	fresh, err := containerKubeconfig("astro-lab", []byte(kubeFixture("astro-lab")))
	if err != nil {
		t.Fatal(err)
	}
	for _, current := range []string{"", "not a kubeconfig", "clusters: invalid", "clusters: [invalid]", "users: [{name: kind-astro-lab, user: invalid}]"} {
		if _, err := refreshCredentials([]byte(current), fresh); err == nil {
			t.Errorf("accepted malformed workspace: %q", current)
		}
	}
}
