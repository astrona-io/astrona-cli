package labshell

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"gopkg.in/yaml.v3"
)

// Only inline Kind credentials cross into the terminal. Host paths, executable
// auth plugins, proxy settings and credentials for unrelated contexts do not.
type kubeConfig struct {
	APIVersion string        `yaml:"apiVersion"`
	Kind       string        `yaml:"kind"`
	Current    string        `yaml:"current-context"`
	Clusters   []kubeCluster `yaml:"clusters"`
	Users      []kubeUser    `yaml:"users"`
	Contexts   []kubeContext `yaml:"contexts"`
}
type kubeCluster struct {
	Name    string `yaml:"name"`
	Cluster struct {
		Server string `yaml:"server"`
		CA     string `yaml:"certificate-authority-data"`
	} `yaml:"cluster"`
}
type kubeUser struct {
	Name string `yaml:"name"`
	User struct {
		Cert string `yaml:"client-certificate-data"`
		Key  string `yaml:"client-key-data"`
	} `yaml:"user"`
}
type kubeContext struct {
	Name    string `yaml:"name"`
	Context struct {
		Cluster   string `yaml:"cluster"`
		User      string `yaml:"user"`
		Namespace string `yaml:"namespace,omitempty"`
	} `yaml:"context"`
}

func containerKubeconfig(lab string, data []byte) ([]byte, error) {
	if err := config.ValidateName(lab); err != nil {
		return nil, err
	}
	var source kubeConfig
	if err := yaml.Unmarshal(data, &source); err != nil {
		return nil, err
	}
	name := "kind-" + lab
	if source.Current != name || len(source.Contexts) != 1 || len(source.Clusters) != 1 || len(source.Users) != 1 {
		return nil, fmt.Errorf("%s needs its own single-cluster Kind kubeconfig", lab)
	}
	context, cluster, user := source.Contexts[0], source.Clusters[0], source.Users[0]
	if context.Name != name || context.Context.Cluster != cluster.Name || context.Context.User != user.Name || cluster.Cluster.CA == "" || user.User.Cert == "" || user.User.Key == "" {
		return nil, fmt.Errorf("%s needs inline Kind certificates for an isolated terminal", lab)
	}
	cluster.Cluster.Server = "https://" + lab + "-control-plane:6443"
	return yaml.Marshal(kubeConfig{APIVersion: "v1", Kind: "Config", Current: name, Clusters: []kubeCluster{cluster}, Users: []kubeUser{user}, Contexts: []kubeContext{context}})
}

func copyKubeconfig(root *os.Root, lab string) error {
	path, err := cluster.KubeconfigPath(lab)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4*1024*1024+1))
	if err != nil {
		return err
	}
	if len(data) > 4*1024*1024 {
		return fmt.Errorf("lab kubeconfig is too large")
	}
	data, err = containerKubeconfig(lab, data)
	if err != nil {
		return err
	}
	previous, err := root.ReadFile("config/" + lab + ".yaml")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	work, err := childDir(root, "work", false)
	if err != nil {
		return err
	}
	defer work.Close()
	kube, err := childDir(work, ".kube", true)
	if err != nil {
		return err
	}
	defer kube.Close()
	name := lab + ".yaml"
	info, err := kube.Lstat(name)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("workspace kubeconfig must be a regular file")
		}
		if bytes.Equal(previous, data) {
			return nil // Keep edits when the generated credentials have not changed.
		}
		file, err := kube.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		opened, err := file.Stat()
		if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			file.Close()
			return fmt.Errorf("workspace kubeconfig changed while opening it")
		}
		current, err := io.ReadAll(io.LimitReader(file, 4*1024*1024+1))
		file.Close()
		if err != nil {
			return err
		}
		if len(current) > 4*1024*1024 {
			return fmt.Errorf("workspace kubeconfig is too large")
		}
		updated, err := refreshCredentials(current, data)
		if err != nil {
			return fmt.Errorf("refresh %s terminal credentials: %w", lab, err)
		}
		if err := writePrivate(kube, name, updated); err != nil {
			return err
		}
	} else {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := writePrivate(kube, name, data); err != nil {
			return err
		}
	}
	return writePrivate(root, "config/"+lab+".yaml", data)
}

// Refresh only generated cluster/user fields. Contexts, namespaces, preferences
// and any extra entries belong to the student and survive a linked reset.
func refreshCredentials(current, generated []byte) ([]byte, error) {
	var working, fresh map[string]any
	if err := yaml.Unmarshal(current, &working); err != nil {
		return nil, err
	}
	if working == nil {
		return nil, fmt.Errorf("workspace kubeconfig must be a mapping")
	}
	if err := yaml.Unmarshal(generated, &fresh); err != nil {
		return nil, err
	}
	for _, field := range []struct{ list, value string }{{"clusters", "cluster"}, {"users", "user"}} {
		entries, ok := working[field.list].([]any)
		if working[field.list] != nil && !ok {
			return nil, fmt.Errorf("workspace kubeconfig %s must be a list", field.list)
		}
		for _, replacement := range fresh[field.list].([]any) {
			replace := replacement.(map[string]any)
			found := false
			for _, raw := range entries {
				entry, ok := raw.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("workspace kubeconfig %s entry must be a mapping", field.list)
				}
				if entry["name"] != replace["name"] {
					continue
				}
				values, ok := entry[field.value].(map[string]any)
				if !ok {
					return nil, fmt.Errorf("workspace kubeconfig %s must be a mapping", field.value)
				}
				for key, value := range replace[field.value].(map[string]any) {
					values[key] = value
				}
				found = true
			}
			if !found {
				entries = append(entries, replacement)
			}
		}
		working[field.list] = entries
	}
	data, err := yaml.Marshal(working)
	if err != nil {
		return nil, err
	}
	return data, nil
}
