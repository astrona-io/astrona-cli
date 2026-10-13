package labshell

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"astrona/internal/apphome"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/labstate"
)

//go:embed Containerfile
var containerfile string

const terminalLabel = "io.astrona.terminal"

func imageName() string {
	digest := sha256.Sum256([]byte(containerfile))
	return fmt.Sprintf("localhost/astrona-terminal:%x", digest[:8])
}

var imageBuild = make(chan struct{}, 1)

func ensureImage(ctx context.Context, engine string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	select {
	case imageBuild <- struct{}{}:
		defer func() { <-imageBuild }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := exec.CommandContext(ctx, engine, "image", "inspect", imageName()).Run(); err == nil {
		return nil
	}
	dir, err := os.MkdirTemp("", "astrona-terminal-image-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	cmd := exec.CommandContext(ctx, engine, "build", "--tag", imageName(), "--file", "-", dir)
	cmd.Stdin = strings.NewReader(containerfile)
	var output limitedOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("prepare isolated terminal tools: %w\n%s", err, output.String())
	}
	return nil
}

type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 64*1024 - b.Len(); remaining > 0 {
		_, _ = b.Buffer.Write(p[:min(n, remaining)])
	}
	return n, nil
}

// Command builds the terminal image when needed, then launches a container.
// Environment variables are passed individually; host secrets and sockets are
// never inherited by the container. Normal Linux paths belong to its image.
func Command(ctx context.Context, lab, target string, command []string, tty bool) (*exec.Cmd, error) {
	if err := config.ValidateName(lab); err != nil {
		return nil, err
	}
	if err := config.ValidateName(target); err != nil {
		return nil, err
	}
	engine, err := cluster.DetectContainerEngine()
	if err != nil {
		return nil, fmt.Errorf("isolated terminal requires a running container engine: %w", err)
	}
	release, err := labstate.LockMutation(lab)
	if err != nil {
		return nil, err
	}
	defer release()
	root, path, err := prepareWorkspace(lab)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	links, err := cluster.ReadLinks(lab)
	if err != nil {
		return nil, err
	}
	names := []string{lab}
	var hosts []string
	env := []string{"HOME=/workspace", "TERM=xterm-256color", "ASTRONA_LAB=" + lab}
	for _, link := range links {
		if err := config.ValidateName(link.Cluster); err != nil {
			return nil, err
		}
		if err := config.ValidateName(link.Name); err != nil {
			return nil, err
		}
		names = append(names, link.Cluster)
		ip, err := nodeIP(ctx, engine.Path, link.Cluster)
		if err != nil {
			return nil, err
		}
		hosts = append(hosts, link.Hostname()+":"+ip)
		for _, prefix := range []string{link.EnvPrefix(), "ASTRONA_LINK_" + strings.ToUpper(strings.ReplaceAll(link.Name, "-", "_"))} {
			env = append(env, prefix+"_HOST="+link.Host(), prefix+"_HOSTNAME="+link.Hostname(), prefix+"_CONTEXT="+link.Context(), prefix+"_KUBECONFIG=/workspace/.kube/"+link.Cluster+".yaml")
		}
	}
	var kubeconfigs []string
	found := false
	for _, name := range names {
		if err := copyKubeconfig(root, name); err != nil {
			return nil, fmt.Errorf("prepare %s terminal credentials: %w", name, err)
		}
		p := "/workspace/.kube/" + name + ".yaml"
		if name == target {
			kubeconfigs = append([]string{p}, kubeconfigs...)
			found = true
		} else {
			kubeconfigs = append(kubeconfigs, p)
		}
	}
	if !found {
		return nil, fmt.Errorf("cluster %s does not belong to lab %s", target, lab)
	}
	env = append(env, "KUBECONFIG="+strings.Join(kubeconfigs, ":"))
	if ca, ok := cluster.ExistingLabCA(lab); ok {
		data, err := os.ReadFile(ca.CertPath)
		if err != nil {
			return nil, err
		}
		if err := writePrivate(root, "config/ca.crt", data); err != nil {
			return nil, err
		}
		env = append(env, "ASTRONA_CA_CERT=/run/astrona/ca.crt")
	}
	prompt := `\[\e[38;5;117m\]astrona-cli >\[\e[97m\] `
	if os.Getenv("NO_COLOR") != "" {
		prompt = "astrona-cli > "
		env = append(env, "NO_COLOR=1")
	}
	env = append(env, "PS1="+prompt)
	if err := writePrivate(root, "engine", []byte(engine.Name)); err != nil {
		return nil, err
	}
	if err := ensureImage(ctx, engine.Path); err != nil {
		return nil, err
	}
	name := "astrona-terminal-" + strings.ToLower(rand.Text())
	args, err := runArgs(engine.Name, path, lab, name, env, hosts, command, tty)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, engine.Path, args...)
	cmd.Dir = filepath.Join(path, "work")
	// The CLI and UI call Cancel after Wait too, so a detached container cannot
	// survive a closed tab or an interrupted container-engine client.
	var once sync.Once
	cmd.Cancel = func() error {
		once.Do(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = exec.CommandContext(cleanup, engine.Path, "rm", "--force", name).Run()
		})
		if cmd.Process != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	return cmd, nil
}

func runArgs(engine, path, lab, name string, env, hosts, command []string, tty bool) ([]string, error) {
	// --mount uses commas as separators. Never reinterpret a user's home path
	// as extra mount options.
	if strings.ContainsAny(path, ",\n\r") {
		return nil, fmt.Errorf("terminal workspace path cannot contain commas or newlines")
	}
	mountOptions := ""
	if engine == "podman" {
		mountOptions = ",relabel=shared"
	}
	args := []string{"run", "--rm", "--interactive", "--name", name, "--label", terminalLabel + "=" + apphome.Name() + ":" + lab,
		"--network", "kind", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--read-only", "--pids-limit=256",
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=256m", "--workdir", "/workspace",
		"--mount", "type=bind,source=" + filepath.Join(path, "work") + ",target=/workspace" + mountOptions,
		"--mount", "type=bind,source=" + filepath.Join(path, "config") + ",target=/run/astrona,readonly" + mountOptions}
	if engine == "podman" {
		args = append(args, "--userns=keep-id")
	} else {
		args = append(args, "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()))
	}
	if tty {
		args = append(args, "--tty")
	}
	for _, host := range hosts {
		args = append(args, "--add-host", host)
	}
	for _, value := range env {
		args = append(args, "--env", value)
	}
	args = append(args, imageName())
	if len(command) > 0 {
		args = append(args, command...)
	}
	return args, nil
}

// Remove closes terminals before removing their workspace. No engine access is
// needed for labs which never opened an isolated terminal.
func Remove(ctx context.Context, lab string) error {
	if err := config.ValidateName(lab); err != nil {
		return err
	}
	r, _, err := openRuntime(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer r.Close()
	root, err := childDir(r, lab, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	data, err := root.ReadFile("engine")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		engine := string(data)
		if engine != "docker" && engine != "podman" {
			return fmt.Errorf("invalid terminal engine for %s", lab)
		}
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, engine, "ps", "--all", "--quiet", "--filter", "label="+terminalLabel+"="+apphome.Name()+":"+lab).Output()
		if err != nil {
			return fmt.Errorf("find lab terminals: %w", err)
		}
		ids := strings.Fields(string(out))
		for _, id := range ids {
			if !containerID.MatchString(id) {
				return fmt.Errorf("invalid terminal container id")
			}
		}
		if len(ids) > 0 {
			if err := exec.CommandContext(ctx, engine, append([]string{"rm", "--force"}, ids...)...).Run(); err != nil {
				return fmt.Errorf("close lab terminals: %w", err)
			}
		}
	}
	return r.RemoveAll(lab)
}

var containerID = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

func nodeIP(ctx context.Context, engine, lab string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, engine, "inspect", "--format", `{{with index .NetworkSettings.Networks "kind"}}{{.IPAddress}}{{end}}`, lab+"-control-plane").Output()
	if err != nil {
		return "", fmt.Errorf("find linked cluster %s on the Kind network: %w", lab, err)
	}
	ip := net.ParseIP(strings.TrimSpace(string(out)))
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("%s has no IPv4 address on the Kind network", lab)
	}
	return ip.String(), nil
}
