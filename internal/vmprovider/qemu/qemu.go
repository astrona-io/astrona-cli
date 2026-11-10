// Package qemu is the vmprovider.Provider that runs VMs as QEMU processes
// on this machine. It wraps internal/hypervisor, which owns the processes,
// disks, cloud-init seeds and the per-VM state under ~/.astrona/qemu.
package qemu

import (
	"context"
	"path/filepath"

	"astrona/internal/config"
	"astrona/internal/hypervisor"
	"astrona/internal/ui"
	"astrona/internal/vmprovider"
)

// Provider runs VMs with hypervisor.CreateQEMUVM. It does not use the
// context: QEMU launches and teardowns are not cancellable today.
type Provider struct {
	rep *ui.Reporter
	// networks and trust are resolved once for the whole lab before any
	// VM boots (hypervisor.ResolveNetworkTopology and
	// hypervisor.ResolveInterVMTrust), keyed by Spec.VM.
	networks map[string][]hypervisor.QEMUNetworkSpec
	trust    map[string]*hypervisor.InterVMTrust
}

var _ vmprovider.Provider = (*Provider)(nil)

// New returns a provider that reports progress on rep. Use it for Get and
// Delete, and for Create on a VM with no lab networks.
func New(rep *ui.Reporter) *Provider {
	return &Provider{rep: rep}
}

// ForLab returns a provider for creating a lab's VMs, with the lab's
// resolved networks and inter-VM trust. Either map may be nil.
func ForLab(rep *ui.Reporter, networks map[string][]hypervisor.QEMUNetworkSpec, trust map[string]*hypervisor.InterVMTrust) *Provider {
	return &Provider{rep: rep, networks: networks, trust: trust}
}

// Create boots the VM and waits until it is SSH-ready.
func (p *Provider) Create(_ context.Context, s vmprovider.Spec) (vmprovider.Instance, error) {
	h, err := hypervisor.CreateQEMUVM(s.Name, s.Lab, s.BaseDir, s.Machine, p.networks[s.VM], p.trust[s.VM], p.rep)
	if err != nil {
		return vmprovider.Instance{}, err
	}
	return instance(h), nil
}

// Get loads the VM's saved handle and checks its qemu process still runs.
func (p *Provider) Get(_ context.Context, id string) (vmprovider.Instance, error) {
	h, err := hypervisor.LoadQEMUHandle(id)
	if err != nil {
		return vmprovider.Instance{}, err
	}
	return instance(h), nil
}

// Delete stops the VM's qemu process and removes its state dir. No state
// at all is not an error.
func (p *Provider) Delete(_ context.Context, id string) error {
	return hypervisor.DestroyQEMUVM(id, p.rep)
}

func instance(h *config.QEMUHandle) vmprovider.Instance {
	console := ""
	if h.StateDir != "" {
		console = filepath.Join(h.StateDir, "console.log")
	}
	return vmprovider.Instance{
		ID:         h.ClusterName,
		SSHHost:    h.SSHHost,
		SSHPort:    h.SSHPort,
		User:       h.AdminUser,
		KeyPath:    h.AdminKeyPath,
		KnownHosts: h.KnownHosts,
		Console:    console,
	}
}
