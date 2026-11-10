package runtime

import (
	"fmt"
	"slices"

	"astrona/internal/config"
	"astrona/internal/hypervisor"
	"astrona/internal/labstate"
)

// reserveQEMUVMs holds child names against standalone runs while the caller
// holds the parent lab's mutation lock. Check every owner before changing any VM.
func reserveQEMUVMs(lab string, vms []config.QEMUVM) (func(), error) {
	names := []string{lab}
	if config.IsMultiVM(vms) && len(vms) > 0 {
		names = nil
		for _, vm := range vms {
			names = append(names, lab+"-"+vm.Name)
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	var releases []func()
	release := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	for _, name := range names {
		if name != lab {
			unlock, err := labstate.LockMutation(name)
			if err != nil {
				release()
				return nil, err
			}
			releases = append(releases, unlock)
			st, err := labstate.Load(name)
			if err != nil || st != nil {
				release()
				if err != nil {
					return nil, err
				}
				return nil, fmt.Errorf("qemu VM name %s is reserved by a standalone lab; nothing was changed", name)
			}
		}
		if err := hypervisor.RequireQEMUOwnership(name, lab); err != nil {
			release()
			return nil, err
		}
	}
	return release, nil
}
