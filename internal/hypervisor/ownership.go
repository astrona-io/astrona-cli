package hypervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"astrona/internal/config"
)

// RequireQEMUOwnership checks that existing VM state belongs to lab. A config
// cannot claim another lab's VM merely by producing the same derived name.
func RequireQEMUOwnership(name, lab string) error {
	base, err := QEMUBaseDir()
	if err != nil {
		return err
	}
	dir, err := labDirWithin(base, name)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(dir, "handle.json"))
	if os.IsNotExist(err) {
		entries, readErr := os.ReadDir(dir)
		if os.IsNotExist(readErr) || (readErr == nil && len(entries) == 0) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
		// Old single-VM state has no separate parent to establish. A child
		// without a handle must be cleaned up explicitly by its own name.
		if name == lab {
			return nil
		}
		return fmt.Errorf("qemu VM %s has no saved owner; kept its state (inspect it, then use astrona destroy %s to remove it explicitly)", name, name)
	}
	if err != nil {
		return err
	}
	var h config.QEMUHandle
	if err := json.Unmarshal(data, &h); err != nil {
		return fmt.Errorf("cannot verify qemu VM %s ownership: %w; use astrona destroy %s for explicit cleanup", name, err, name)
	}
	owner := h.LabName
	if owner == "" && name == lab {
		owner = name // single-VM handles predating LabName
	}
	if h.ClusterName != name || owner != lab {
		return fmt.Errorf("qemu VM %s is not recorded as belonging to lab %s; its state was kept", name, lab)
	}
	return nil
}
