package lifecycle

import (
	"astrona/internal/addons"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/executor"
	"astrona/internal/manifests"
	"astrona/internal/portforward"
	"astrona/internal/runtime"
	"astrona/internal/scripts"
	"astrona/internal/ui"
	"fmt"
)

// Validate checks everything about cfg that can be checked
// before anything is created — a bad gate or forward entry is a config
// mistake, not something to discover after a 1-minute cluster boot (or,
// for `astrona reset`, after the old lab is already gone).
func Validate(cfg *config.LabConfig) error {
	if err := config.ValidateAPIVersion(cfg); err != nil {
		return err
	}
	if err := config.ValidateKindConfig(cfg.Runtime); err != nil {
		return err
	}
	if cfg.Runtime.Type == string(runtime.RuntimeQEMU) {
		if err := config.ValidateQEMUVMs(cfg.Runtime.QEMU); err != nil {
			return err
		}
	}
	if err := config.ValidateKindClusters(cfg); err != nil {
		return err
	}
	if err := config.ValidateTimeLimit(cfg); err != nil {
		return err
	}
	if err := config.ValidateExam(cfg); err != nil {
		return err
	}
	if err := config.ValidateResources(cfg); err != nil {
		return err
	}
	if err := config.ValidateChecks(cfg); err != nil {
		return err
	}
	if err := config.ValidateScoring(cfg); err != nil {
		return err
	}
	if err := config.ValidateWaitFor(cfg); err != nil {
		return err
	}
	if err := config.ValidatePortForwards(cfg.Runtime); err != nil {
		return err
	}
	return nil
}

// Up creates cfg's environment under clusterName and runs everything
// `astrona run` does on top — preload, addons, lab CA, bootstrap,
// manifests, readiness gates, port forwards (not forTest) — with its
// linked clusters' addresses made available to it. The one pipeline for
// the lab itself (run, reset, test) and for each linked cluster. No
// summary output.
//
// Once the environment exists it's returned even when a later step fails,
// so the caller can collect diagnostics and tear it down (astrona test).
func Up(cfg *config.LabConfig, baseDir, clusterName string, links []cluster.LinkState, forTest bool, rep *ui.Reporter) (*runtime.LabEnvironment, []portforward.Forward, error) {
	// A test copy of a lab mustn't fight its real `run` for host ports.
	if forTest && cfg.Runtime.Kind != nil {
		k := *cfg.Runtime.Kind
		k.Addons.SkipHostPorts = true
		cfg.Runtime.Kind = &k
	}
	rep.Section("Lab: %s", cfg.Metadata.Name)

	// Tools first: failing halfway through creating a cluster is slower
	// to find out and leaves something to clean up.
	if cfg.Runtime.Type == "" || cfg.Runtime.Type == string(runtime.RuntimeKind) {
		if _, err := cluster.DetectContainerEngine(); err != nil {
			return nil, nil, err
		}
		if _, err := executor.LookKubectl(); err != nil {
			return nil, nil, err
		}
	}

	env, err := runtime.CreateEnvironment(clusterName, baseDir, cfg.Runtime, rep)
	if err != nil {
		return nil, nil, fmt.Errorf("lab setup failed: %w", err)
	}
	if err := AttachLinks(env, clusterName, links, rep); err != nil {
		return env, nil, err
	}

	// Before addons and bootstrap, so anything they start can use
	// the preloaded images.
	if k := cfg.Runtime.Kind; k != nil && len(k.PreloadImages) > 0 {
		rep.Section("Images")
		if err := cluster.PreloadImages(clusterName, k.PreloadImages, rep); err != nil {
			return env, nil, fmt.Errorf("image preload failed: %w", err)
		}
	}

	if k := cfg.Runtime.Kind; k != nil && !k.Addons.IsZero() {
		rep.Section("Addons")
		if err := addons.Install(k.Addons, env.KubeContext, rep); err != nil {
			return env, nil, fmt.Errorf("addons failed: %w", err)
		}
	}

	if err := InstallSharedCA(cfg, env, rep); err != nil {
		return env, nil, err
	}

	if err := WaitForClusterDNS(cfg, env, rep); err != nil {
		return env, nil, fmt.Errorf("cluster DNS not ready: %w", err)
	}

	// What exists now is the platform (kube-system, addons, …); the soft
	// reset deletes every namespace created after it.
	if err := recordBaseline(env); err != nil {
		rep.Warn("could not record the lab's baseline (reset --soft won't work): %s", err)
	}
	if err := Bootstrap(cfg, baseDir, env, rep); err != nil {
		return env, nil, err
	}

	// Started last, once manifests are applied and readiness gates
	// passed, so there's something to forward to. A forward that
	// fails or isn't ready yet never fails the run — the lab itself
	// is up, and the supervisor keeps retrying.
	var forwards []portforward.Forward
	if len(cfg.Runtime.PortForwards) > 0 && !forTest {
		rep.Section("Port forwards")
		forwards, err = StartPortForwards(clusterName, cfg.Runtime.PortForwards, rep)
		if err != nil {
			rep.Warn("some port forwards could not be started — fix and retry with `astrona port-forward start -c <config>`")
		}
	}
	return env, forwards, nil
}

// Bootstrap runs cfg's bootstrap on env: init scripts, manifests, then
// readiness gates. Part of Up; reset --soft runs it again.
func Bootstrap(cfg *config.LabConfig, baseDir string, env *runtime.LabEnvironment, rep *ui.Reporter) error {
	if scripts.HasBootstrapInit(cfg) {
		rep.Section("Bootstrap")
		if err := scripts.RunBootstrap(cfg, baseDir, env, rep); err != nil {
			return fmt.Errorf("init scripts failed: %w", err)
		}
	}

	if len(cfg.Bootstrap.Manifests) > 0 {
		if env.KubeContext == "" {
			return fmt.Errorf("bootstrap.manifests requires a kubectl-reachable cluster, but runtime '%s' has none", env.Type)
		}
		rep.Section("Manifests")
		if err := manifests.ApplyManifests(cfg.Bootstrap.Manifests, baseDir, env.KubeContext, rep); err != nil {
			return fmt.Errorf("bootstrap manifests failed: %w", err)
		}
	}

	if len(cfg.Bootstrap.WaitFor) > 0 {
		rep.Section("Readiness")
		if err := manifests.WaitFor(cfg.Bootstrap.WaitFor, env.KubeContext, rep); err != nil {
			return fmt.Errorf("lab did not become ready: %w", err)
		}
	}
	return nil
}
