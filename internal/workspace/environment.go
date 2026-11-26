package workspace

import (
	"fmt"

	"astrona/internal/config"
)

// Environment runtimes, see docs/reference/workspace-api.md.
const (
	RuntimeKind = "kind"
	RuntimeVM   = "vm"
)

// ExtendMinutes is how much time extending a playground would add. Extending
// is not available yet: runView.CanExtend is always false.
const ExtendMinutes = 30

// Environment describes what a run runs on, for the page's environment
// panel: the lab's main Kind cluster (a multi-cluster lab's own cluster,
// not its linked ones) or its VMs. `astrona run` fills it in from the lab
// config and what it saw while bringing the lab up; nothing is looked up
// while the page polls. A value that is not known is "" or empty, never a
// guess. Never a kubeconfig, credential or address.
type Environment struct {
	// Runtime is RuntimeKind or RuntimeVM.
	Runtime string `json:"runtime"`
	// KubernetesVersion is the node image's Kubernetes version
	// ("v1.35.0"); "" when unknown or for VMs.
	KubernetesVersion string `json:"kubernetesVersion"`
	// Engine is the container engine running the nodes (docker or
	// podman); "" for VMs.
	Engine string    `json:"engine"`
	Nodes  []EnvNode `json:"nodes"`
	// Network is the cluster's CNI by its display name ("kindnet",
	// "Calico"); "" when the lab installs its own or for VMs.
	Network string `json:"network"`
	// Images are the images loaded into the nodes before the lab starts
	// (runtime.kind.preloadImages).
	Images []string `json:"images"`
	// Context is the kubectl context of the cluster; "" for VMs.
	Context string `json:"context"`
	// Namespace is the namespace kubectl uses by default; "" for VMs.
	Namespace string `json:"namespace"`
	// Connect is a command that reaches the lab from the student's own
	// terminal.
	Connect string `json:"connect"`
	// VMs are a QEMU lab's machines; empty for Kind.
	VMs []EnvVM `json:"vms"`
}

// EnvNode is one Kubernetes node of the cluster.
type EnvNode struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

// EnvVM is one VM of a QEMU lab.
type EnvVM struct {
	Name string `json:"name"`
	// OS is the operating system read from the image's name ("Ubuntu
	// 24.04"); "" when the name does not say.
	OS        string `json:"os"`
	CPUs      int    `json:"cpus"`
	MemoryMiB int    `json:"memoryMiB"`
}

// SuggestedCommand is one command a playground's page offers to copy into
// the terminal; Label is "" when the command speaks for itself.
type SuggestedCommand struct {
	Command string `json:"command"`
	Label   string `json:"label"`
}

// Default suggested commands of a playground whose config lists none.
var (
	defaultKindCommands = []SuggestedCommand{
		{Command: "kubectl get nodes -o wide"},
		{Command: "kubectl get pods -A"},
		{Command: "kubectl run demo --image=busybox:1.37.0 -- sleep 3600"},
		{Command: "kubectl get svc -n kube-system"},
	}
	defaultVMCommands = []SuggestedCommand{
		{Command: "uname -a"},
		{Command: "ip -br addr"},
		{Command: "df -h"},
		{Command: "systemctl --failed"},
	}
)

// DefaultSuggestedCommands are a playground's suggested commands when its
// config lists none, for runtime (RuntimeKind or RuntimeVM).
func DefaultSuggestedCommands(runtime string) []SuggestedCommand {
	if runtime == RuntimeVM {
		return append([]SuggestedCommand(nil), defaultVMCommands...)
	}
	return append([]SuggestedCommand(nil), defaultKindCommands...)
}

// normalize gives every list a non-nil value, so the page always sees
// arrays.
func (e *Environment) normalize() {
	if e.Nodes == nil {
		e.Nodes = []EnvNode{}
	}
	if e.Images == nil {
		e.Images = []string{}
	}
	if e.VMs == nil {
		e.VMs = []EnvVM{}
	}
}

func (e *Environment) validate() error {
	if e.Runtime != RuntimeKind && e.Runtime != RuntimeVM {
		return fmt.Errorf("environment: unknown runtime %q", e.Runtime)
	}
	for _, vm := range e.VMs {
		if err := config.ValidateName(vm.Name); err != nil {
			return fmt.Errorf("environment vm name: %w", err)
		}
	}
	return nil
}

func validateSuggestedCommands(list []SuggestedCommand) error {
	if len(list) > config.MaxSuggestedCommands {
		return fmt.Errorf("at most %d suggested commands", config.MaxSuggestedCommands)
	}
	for _, c := range list {
		if err := config.CheckSuggestedCommand(c.Command, c.Label); err != nil {
			return fmt.Errorf("suggested command: %w", err)
		}
	}
	return nil
}

// environment is the run's environment for the page. A record from before
// environments has only its runtime, read from its first target.
func (r RunRecord) environment() *Environment {
	var e Environment
	if r.Environment != nil {
		e = *r.Environment
	} else {
		e.Runtime = RuntimeKind
		if len(r.Targets) > 0 && r.Targets[0].Kind == TargetVM {
			e.Runtime = RuntimeVM
		}
	}
	e.normalize()
	return &e
}

// suggestedCommands are the page's suggested commands: a playground's own
// or the defaults for its runtime; none for a lab or an exam.
func (r RunRecord) suggestedCommands() []SuggestedCommand {
	if !r.Playground || r.Exam {
		return []SuggestedCommand{}
	}
	if len(r.SuggestedCommands) > 0 {
		return r.SuggestedCommands
	}
	return DefaultSuggestedCommands(r.environment().Runtime)
}
