package main

import "fmt"

// RuntimeType is which backend a lab runs on.
type RuntimeType string

const (
	RuntimeKind RuntimeType = "kind"
	RuntimeQEMU RuntimeType = "qemu"
)

// resolveRuntimeType reads cfg.Type, defaulting an empty value to kind so
// every existing lab config (no runtime: block at all) keeps working
// unchanged.
func resolveRuntimeType(cfg RuntimeConfig) (RuntimeType, error) {
	switch cfg.Type {
	case "", string(RuntimeKind):
		return RuntimeKind, nil
	case string(RuntimeQEMU):
		return RuntimeQEMU, nil
	default:
		return "", fmt.Errorf("unsupported runtime type '%s' (must be 'kind' or 'qemu')", cfg.Type)
	}
}

// LabEnvironment is the uniform handle every cmd_*.go command works with,
// regardless of which backend actually created it.
type LabEnvironment struct {
	Type        RuntimeType
	Name        string
	KubeContext string         // "kind-"+name for kind; "" for qemu (no kubectl-reachable cluster this pass)
	Executor    ScriptExecutor // LocalExecutor for kind; SSHExecutor for qemu
}

// CreateEnvironment brings up a fresh lab environment: a kind cluster or a
// qemu VM, depending on cfg.Type.
func CreateEnvironment(name, baseDir string, cfg RuntimeConfig) (*LabEnvironment, error) {
	runtimeType, err := resolveRuntimeType(cfg)
	if err != nil {
		return nil, err
	}

	switch runtimeType {
	case RuntimeKind:
		if err := CreateKindCluster(name); err != nil {
			return nil, err
		}
		return &LabEnvironment{
			Type:        RuntimeKind,
			Name:        name,
			KubeContext: "kind-" + name,
			Executor:    LocalExecutor{},
		}, nil
	case RuntimeQEMU:
		if cfg.QEMU == nil {
			return nil, fmt.Errorf("runtime.type is 'qemu' but runtime.qemu config is missing")
		}
		handle, err := CreateQEMUVM(name, baseDir, cfg.QEMU)
		if err != nil {
			return nil, err
		}
		return &LabEnvironment{
			Type:        RuntimeQEMU,
			Name:        name,
			KubeContext: "",
			Executor:    sshExecutorFor(handle),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported runtime type '%s'", runtimeType)
	}
}

// LoadEnvironment rediscovers an already-created lab environment — used by
// `astrona submit` (always) and `astrona destroy` (when it has a loadable config),
// which run in a separate process invocation from whatever ran
// CreateEnvironment.
func LoadEnvironment(name string, cfg RuntimeConfig) (*LabEnvironment, error) {
	runtimeType, err := resolveRuntimeType(cfg)
	if err != nil {
		return nil, err
	}

	switch runtimeType {
	case RuntimeKind:
		// kind cluster state is owned by the container engine itself and
		// queryable by name from any process — no liveness check needed
		// here, kubectl will fail naturally downstream if it's gone.
		return &LabEnvironment{
			Type:        RuntimeKind,
			Name:        name,
			KubeContext: "kind-" + name,
			Executor:    LocalExecutor{},
		}, nil
	case RuntimeQEMU:
		handle, err := LoadQEMUHandle(name)
		if err != nil {
			return nil, err
		}
		return &LabEnvironment{
			Type:        RuntimeQEMU,
			Name:        name,
			KubeContext: "",
			Executor:    sshExecutorFor(handle),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported runtime type '%s'", runtimeType)
	}
}

// DestroyEnvironment tears down name's lab environment. Best-effort by
// design (mirrors cmd_down.go's existing lenient posture): a qemu VM with no
// persisted state is a no-op, not an error.
func DestroyEnvironment(name string, cfg RuntimeConfig) error {
	runtimeType, err := resolveRuntimeType(cfg)
	if err != nil {
		return err
	}

	switch runtimeType {
	case RuntimeKind:
		return DeleteKindCluster(name)
	case RuntimeQEMU:
		return DestroyQEMUVM(name)
	default:
		return fmt.Errorf("unsupported runtime type '%s'", runtimeType)
	}
}

func sshExecutorFor(h *QEMUHandle) SSHExecutor {
	return SSHExecutor{
		Host:       h.SSHHost,
		Port:       h.SSHPort,
		User:       h.SSHUser,
		KeyPath:    h.SSHKeyPath,
		KnownHosts: h.KnownHosts,
	}
}
