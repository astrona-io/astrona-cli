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
//
// Executor is the single executor for a single-environment lab — kind, or
// qemu with a one-entry, unnamed runtime.qemu list. Executors is populated
// instead, keyed by VM name, for a multi-VM qemu lab (isMultiVM); Executor
// is left nil in that case specifically, so "is this lab multi-VM" can
// always be answered by checking which field is set, without re-consulting
// the config. Every orchestration call site (scripts.go's runBootstrap/
// runOnEveryVM, proctor.go's gradeScripts) branches on env.Executor != nil
// for exactly this reason.
type LabEnvironment struct {
	Type        RuntimeType
	Name        string
	KubeContext string                    // "kind-"+name for kind; "" for qemu (no kubectl-reachable cluster this pass)
	Executor    ScriptExecutor            // LocalExecutor for kind; SSHExecutor for a single-VM qemu lab; nil for multi-VM qemu
	Executors   map[string]ScriptExecutor // vm name -> SSHExecutor; only set for a multi-VM qemu lab
}

// executorForVM looks up name in Executors — only ever called once a
// caller already knows it's dealing with a multi-VM lab and which VM it
// wants (runtime.qemu's own list is always the source of the name), so a
// miss here means Executors and the VM list it was built from have gone
// out of sync, not a config authoring mistake.
func (env *LabEnvironment) executorForVM(name string) (ScriptExecutor, error) {
	executor, ok := env.Executors[name]
	if !ok {
		return nil, fmt.Errorf("no vm named '%s' in this lab's environment (have: %v)", name, vmNames(env.Executors))
	}
	return executor, nil
}

func vmNames(executors map[string]ScriptExecutor) []string {
	names := make([]string, 0, len(executors))
	for name := range executors {
		names = append(names, name)
	}
	return names
}

// CreateEnvironment brings up a fresh lab environment: a kind cluster, or —
// for qemu — one VM or several named ones (runtime.qemu, see isMultiVM).
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
		if err := validateQEMUVMs(cfg.QEMU); err != nil {
			return nil, err
		}
		if !isMultiVM(cfg.QEMU) {
			handle, err := CreateQEMUVM(name, baseDir, cfg.QEMU[0].asQEMUConfig())
			if err != nil {
				return nil, err
			}
			return &LabEnvironment{
				Type:        RuntimeQEMU,
				Name:        name,
				KubeContext: "",
				Executor:    sshExecutorFor(handle),
			}, nil
		}
		return createMultiQEMUEnvironment(name, baseDir, cfg.QEMU)
	default:
		return nil, fmt.Errorf("unsupported runtime type '%s'", runtimeType)
	}
}

// createMultiQEMUEnvironment boots every named VM in vms, each fully
// reusing CreateQEMUVM under its own synthesized name ("<name>-<vm.Name>")
// — its own state dir, overlay disk, SSH key, and (already-existing)
// already-running guard. `astrona list`/`astrona ssh` need no changes to
// see/reach each VM individually: they already work off whatever names show
// up under ~/.astrona/qemu, and each VM gets its own directory there.
//
// If any VM fails to start, every VM already started in this same call is
// torn down before returning the error — a half-started multi-VM lab never
// lingers as a set of ghost VMs the caller doesn't know exist yet (nothing
// has been returned to it to run `astrona destroy` against).
func createMultiQEMUEnvironment(name, baseDir string, vms []QEMUVM) (*LabEnvironment, error) {
	executors := make(map[string]ScriptExecutor, len(vms))
	var started []string

	for _, vm := range vms {
		handle, err := CreateQEMUVM(name+"-"+vm.Name, baseDir, vm.asQEMUConfig())
		if err != nil {
			for _, s := range started {
				if derr := DestroyQEMUVM(name + "-" + s); derr != nil {
					fmt.Printf("[WARN] failed to roll back vm '%s' after vm '%s' failed to start: %s\n", s, vm.Name, derr)
				}
			}
			return nil, fmt.Errorf("failed to start vm '%s': %w", vm.Name, err)
		}
		executors[vm.Name] = sshExecutorFor(handle)
		started = append(started, vm.Name)
	}

	return &LabEnvironment{Type: RuntimeQEMU, Name: name, Executors: executors}, nil
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
		if err := validateQEMUVMs(cfg.QEMU); err != nil {
			return nil, err
		}
		if !isMultiVM(cfg.QEMU) {
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
		}
		executors := make(map[string]ScriptExecutor, len(cfg.QEMU))
		for _, vm := range cfg.QEMU {
			handle, err := LoadQEMUHandle(name + "-" + vm.Name)
			if err != nil {
				return nil, fmt.Errorf("vm '%s': %w", vm.Name, err)
			}
			executors[vm.Name] = sshExecutorFor(handle)
		}
		return &LabEnvironment{Type: RuntimeQEMU, Name: name, Executors: executors}, nil
	default:
		return nil, fmt.Errorf("unsupported runtime type '%s'", runtimeType)
	}
}

// DestroyEnvironment tears down name's lab environment. Best-effort by
// design (mirrors cmd_down.go's existing lenient posture): a qemu VM with no
// persisted state is a no-op, not an error. For a multi-VM qemu lab, every
// VM is attempted even if one fails, so one stuck VM never blocks the rest
// from being torn down; failures are collected into a single returned error
// naming every VM that failed, not just the first.
func DestroyEnvironment(name string, cfg RuntimeConfig) error {
	runtimeType, err := resolveRuntimeType(cfg)
	if err != nil {
		return err
	}

	switch runtimeType {
	case RuntimeKind:
		return DeleteKindCluster(name)
	case RuntimeQEMU:
		// An empty cfg.QEMU (config missing/unreadable at destroy time —
		// see cmd_destroy.go's loadTeardownInfo, which never fails) can't
		// tell multi-VM from single-VM apart; DestroyQEMUVM(name) is the
		// same best-effort fallback the single-VM path always was, rather
		// than silently no-op-ing an empty VM list.
		if isMultiVM(cfg.QEMU) && len(cfg.QEMU) > 0 {
			var failed []string
			for _, vm := range cfg.QEMU {
				if err := DestroyQEMUVM(name + "-" + vm.Name); err != nil {
					failed = append(failed, fmt.Sprintf("%s: %s", vm.Name, err))
				}
			}
			if len(failed) > 0 {
				return fmt.Errorf("failed to destroy %d/%d vm(s): %v", len(failed), len(cfg.QEMU), failed)
			}
			return nil
		}
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
