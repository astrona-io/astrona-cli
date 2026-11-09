package labops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"astrona/internal/apphome"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/hypervisor"
	"astrona/internal/labshell"
	"astrona/internal/lifecycle"
	"astrona/internal/runtime"
	"astrona/internal/sshopts"
	"astrona/internal/ui"
)

// LoadLabFunc loads the lab config cmd resolved from -c/--file/--git; the
// cleanup releases it. labops never reads those flags itself.
type LoadLabFunc func() (*config.LabConfig, func(), error)

// ResolveKindCluster picks the kind lab a kubeconfig/shell command acts on,
// in order: an explicit lab-name argument; the lab config from load (if it
// loads and is a kind lab); otherwise the only running astrona kind lab.
// Refuses to guess between several.
func ResolveKindCluster(labArg string, load LoadLabFunc) (string, error) {
	if labArg != "" {
		name := config.NormalizeClusterName(labArg)
		if !cluster.Exists(name) {
			return "", fmt.Errorf("no running kind lab named '%s' — run `astrona list` to see what's running", name)
		}
		return name, nil
	}

	if cfg, cleanup, err := load(); err == nil {
		defer cleanup()
		if cfg.Runtime.Type == "" || cfg.Runtime.Type == string(runtime.RuntimeKind) {
			name := config.NormalizeClusterName(cfg.Metadata.Name)
			if cluster.Exists(name) {
				return name, nil
			}
			return "", fmt.Errorf("lab '%s' from the config isn't running — start it with `astrona run`", name)
		}
		return "", fmt.Errorf("lab '%s' uses the %s runtime — use `astrona ssh` instead", cfg.Metadata.Name, cfg.Runtime.Type)
	}

	// A linked cluster is part of its lab, not a lab to pick.
	owners := LinkedClusterOwners()
	var kindClusters []string
	for _, r := range CollectKindRows() {
		if _, linked := owners[r.Name]; !linked && !strings.HasPrefix(r.Name, apphome.TestClusterPrefix()) {
			kindClusters = append(kindClusters, r.Name)
		}
	}
	switch len(kindClusters) {
	case 0:
		return "", fmt.Errorf("no astrona kind lab is running — start one with `astrona run`")
	case 1:
		return kindClusters[0], nil
	default:
		return "", fmt.Errorf("several kind labs are running — pick one: %s", strings.Join(kindClusters, ", "))
	}
}

// LabKubeconfig returns lab's isolated kubeconfig, (re)writing it first if
// it's missing — e.g. the lab was created by an astrona version from
// before kubeconfig isolation.
func LabKubeconfig(lab string) (string, error) {
	if path := cluster.ExistingKubeconfig(lab); path != "" {
		return path, nil
	}
	return cluster.WriteLabKubeconfig(lab, ui.Discard())
}

// ShellKubeconfigs is the KUBECONFIG list for a lab shell: the lab's own
// kubeconfig and every linked cluster's, with target's first (kubectl
// merges the list; the first file's current-context wins). target "" is
// the lab's own cluster. kubeconfigOf returns "" for a cluster without one.
func ShellKubeconfigs(own, target string, links []cluster.LinkState, kubeconfigOf func(string) string) []string {
	paths := []string{own}
	for _, l := range links {
		kc := kubeconfigOf(l.Cluster)
		switch {
		case kc == "":
		case l.Cluster == target:
			paths = append([]string{kc}, paths...)
		default:
			paths = append(paths, kc)
		}
	}
	return paths
}

// IsolatedLabCommand builds the isolated terminal container for a kind lab
// (labshell.Command); a variable so tests can stand in for the engine.
var IsolatedLabCommand = labshell.Command

// ShellCommand is the isolated terminal for the kind lab lab: a shell, or
// command when it is not empty. clusterName makes a linked cluster (its
// runtime.kind.clusters name) the default context. tty says whether the
// container gets a terminal — cmd decides, it owns the terminal. It
// returns the command and the cluster that is the default context.
func ShellCommand(ctx context.Context, lab, clusterName string, command []string, tty bool) (*exec.Cmd, string, error) {
	_, err := LabKubeconfig(lab)
	if err != nil {
		return nil, "", err
	}

	// Linked clusters' kubeconfigs join the lab's own, so `kubectl
	// --context kind-<linked cluster>` works in the shell too; --cluster
	// puts one first, making it the default context.
	links, _ := lifecycle.Links(lab)
	target := lab
	if clusterName != "" {
		if target, err = lifecycle.FindLinkedCluster(lab, clusterName, links); err != nil {
			return nil, "", err
		}
		// Never silently fall back to the main cluster when the selected
		// linked cluster's kubeconfig is missing.
		if _, err := LabKubeconfig(target); err != nil {
			return nil, "", err
		}
	}
	c, err := IsolatedLabCommand(ctx, lab, target, command, tty)
	return c, target, err
}

// ShellQuote quotes s for a POSIX shell — everything sent to the VM goes
// through the remote user's shell, so every piece of it is quoted.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// VMWorkspaceShell is the remote command that opens the student's shell in
// lab's per-run workspace inside the VM. ps1 "" keeps the VM's own prompt.
func VMWorkspaceShell(lab, ps1 string) string {
	// lab has passed ValidateName before it is used in a remote command.
	dir := `"$HOME/.astrona/runtime/` + lab + `"`
	command := "mkdir -p " + dir + " && cd " + dir + ` && export PATH="$HOME/.astrona-resources/bin:$PATH" ASTRONA_RESOURCE_DIR="$HOME/resources" ASTRONA_LAB=` + ShellQuote(lab) + "; exec "
	if ps1 != "" {
		command += "env PS1='" + ps1 + "' "
	}
	return command + "sh -i"
}

// AskpassEnvVar carries the deprecated `astrona ssh --password` to this
// executable when ssh runs it as its SSH_ASKPASS helper.
const AskpassEnvVar = "ASTRONA_INTERNAL_ASKPASS"

// SSHOptions are `astrona ssh`'s flags, read in cmd.
type SSHOptions struct {
	User        string // overrides the VM's SSH user
	Password    string // deprecated --password, answered through SSH_ASKPASS
	AskPassword bool   // ssh prompts for the password itself

	// PrepareWorkspace installs the lab's resources into the student's
	// VM home before a key-based login (cmd's resource workspace).
	PrepareWorkspace func(ctx context.Context, lab string, h *config.QEMUHandle) error
}

// SSHSession is an interactive SSH login into a qemu lab's VM, ready to run.
type SSHSession struct {
	Handle   *config.QEMUHandle
	User     string
	Path     string   // the ssh executable
	Args     []string // ssh's arguments, without argv[0]
	password string
}

// PrepareSSH resolves the qemu lab name (with or without the cluster
// prefix) and builds the ssh login into its VM: the client isolated from
// the host (sshopts.Isolated: no user ssh config, agent, forwarding or
// escapes), the lab's own known_hosts, and the remote shell opened in the
// lab's workspace. Only a qemu lab has the state LoadQEMUHandle reads.
func PrepareSSH(ctx context.Context, name string, opts SSHOptions) (*SSHSession, error) {
	name = config.NormalizeClusterName(name)
	if err := config.ValidateName(name); err != nil {
		return nil, err
	}

	handle, err := hypervisor.LoadQEMUHandle(name)
	if err != nil {
		return nil, err
	}
	lab := handle.LabName
	if lab == "" {
		lab = name
	}
	if opts.User == "" && opts.Password == "" && !opts.AskPassword && opts.PrepareWorkspace != nil {
		if err := opts.PrepareWorkspace(ctx, lab, handle); err != nil {
			return nil, err
		}
	}

	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return nil, fmt.Errorf("ssh not found in PATH: %w", err)
	}

	user := handle.SSHUser
	if opts.User != "" {
		user = opts.User
	}

	sshArgs := append(sshopts.Isolated(),
		"-tt",
		"-p", strconv.Itoa(handle.SSHPort),
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile="+handle.KnownHosts,
	)

	if opts.Password != "" || opts.AskPassword {
		sshArgs = append(sshArgs,
			"-o", "PubkeyAuthentication=no",
			"-o", "IdentitiesOnly=yes",
			"-o", "PreferredAuthentications=password,keyboard-interactive",
		)
	} else {
		sshArgs = append(sshArgs, "-i", handle.SSHKeyPath)
	}

	sshArgs = append(sshArgs, "-l", user, "--", handle.SSHHost, VMWorkspaceShell(lab, ""))
	return &SSHSession{Handle: handle, User: user, Path: sshPath, Args: sshArgs, password: opts.Password}, nil
}

// Command is the ssh process for s; cmd attaches it to the terminal.
//
// Deprecated --password: ssh has no way to take a password
// non-interactively, so astrona answers its SSH_ASKPASS prompt itself
// (main re-enters as the askpass helper). The secret is set only on this
// ssh process's environment — never astrona's own, and ssh doesn't forward
// ASTRONA_* to the VM — but ssh's local children (the askpass helper, a
// ProxyCommand) inherit it, and it is already in argv and shell history.
// --ask-password avoids all of that: ssh prompts itself, with no echo, and
// astrona never sees it.
func (s *SSHSession) Command() (*exec.Cmd, error) {
	sshCmd := exec.Command(s.Path, s.Args...)
	if s.password != "" {
		executablePath, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("failed to get executable path: %w", err)
		}
		env := os.Environ()
		env = append(env, "SSH_ASKPASS="+executablePath)
		env = append(env, "SSH_ASKPASS_REQUIRE=force")
		env = append(env, AskpassEnvVar+"="+s.password)
		env = append(env, "DISPLAY=dummy:0")
		sshCmd.Env = env
	}
	return sshCmd, nil
}
