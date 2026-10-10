package config

import (
	"fmt"
	"path"
	"strings"
)

// ResourcesDir is the folder next to config.yaml whose files and folders a
// student can show, copy or run with `astrona resource` — instead of
// copying commands and files out of the lab's docs.
const ResourcesDir = "resources"

// ResourceTypeFile marks a resource that is only ever shown or copied,
// never run.
const ResourceTypeFile = "file"

// LabResource is one entry of `resources:`. Everything in resources/ is a
// resource even without an entry; an entry adds a description and says how
// it runs. File is relative to resources/.
type LabResource struct {
	File        string `yaml:"file"`
	Description string `yaml:"description"`
	// Run is the command `astrona resource run` starts, in the resource's
	// folder (e.g. "go run ."). Empty: picked from the file (.sh → bash,
	// .yaml → kubectl apply, an executable with #! → itself, else a file).
	Run string `yaml:"run"`
	// Type "file": never run, only shown or copied.
	Type string `yaml:"type"`
	// VM is the VM a qemu lab's resource runs in (multi-VM labs).
	VM string `yaml:"vm"`
}

// ValidateResources checks the `resources:` entries — whether their files
// exist is checked when they're collected (internal/resources), which
// needs the lab's folder.
func ValidateResources(cfg *LabConfig) error {
	seen := map[string]bool{}
	for i, r := range cfg.Resources {
		where := fmt.Sprintf("resources[%d]", i)
		f := strings.TrimSpace(r.File)
		if f == "" {
			return fmt.Errorf("%s: file is required (a path inside %s/)", where, ResourcesDir)
		}
		clean := path.Clean(f)
		if path.IsAbs(f) || strings.Contains(f, `\`) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("%s: file %q must be a path inside %s/", where, r.File, ResourcesDir)
		}
		if seen[strings.ToLower(clean)] {
			return fmt.Errorf("%s: %s is listed twice", where, clean)
		}
		seen[strings.ToLower(clean)] = true
		switch r.Type {
		case "", ResourceTypeFile:
		default:
			return fmt.Errorf("%s: unsupported type %q (only %q, for a file that's never run)", where, r.Type, ResourceTypeFile)
		}
		if r.Type == ResourceTypeFile && strings.TrimSpace(r.Run) != "" {
			return fmt.Errorf("%s: type %q is never run — drop run, or drop type", where, ResourceTypeFile)
		}
		if r.VM != "" {
			if cfg.Runtime.Type != "qemu" {
				return fmt.Errorf("%s: vm is only for qemu labs", where)
			}
			found := false
			for _, vm := range cfg.Runtime.QEMU {
				found = found || vm.Name == r.VM
			}
			if !found {
				return fmt.Errorf("%s: no VM named %q in runtime.qemu", where, r.VM)
			}
		}
	}
	return nil
}
