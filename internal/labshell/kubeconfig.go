package labshell

import (
	"errors"
	"fmt"
	"io"
	"os"

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
	if err := writePrivate(root, "config/"+lab+".yaml", data); err != nil {
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
		return nil // Keep namespace/context edits made in another terminal tab.
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writePrivate(kube, name, data)
}
