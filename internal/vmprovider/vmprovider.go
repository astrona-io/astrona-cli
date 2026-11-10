// Package vmprovider is the seam between a lab's VMs and the backend that
// runs them. Everything above it (bootstrap scripts, RunOnEveryVM, proctor
// checks) only needs an SSH endpoint per VM, so that is what a Provider
// hands back.
//
// The only implementation today is vmprovider/qemu, which starts QEMU
// processes on the student's machine through internal/hypervisor. Hosted
// backends (KubeVirt, EC2) will implement the same interface; see
// docs/proposals/hosted-labs/providers.md in the workspace repo.
package vmprovider

import (
	"context"

	"astrona/internal/config"
)

// Spec describes one VM to create.
type Spec struct {
	// Name is the VM's ID: the lab name for a single-VM lab, <lab>-<vm>
	// for each VM of a multi-VM one.
	Name string
	// Lab is the lab the VM belongs to.
	Lab string
	// VM is the VM's name in runtime.qemu ("" for a single unnamed VM).
	// Providers use it to look up lab-wide settings resolved per VM, such
	// as network attachments.
	VM string
	// BaseDir is the lab config's directory; relative image paths resolve
	// against it.
	BaseDir string
	// Machine is the VM's definition from the lab config: image, CPUs,
	// memory, disks.
	Machine *config.QEMUConfig
}

// Instance is a running VM as its callers see it: where to reach it over
// SSH, and as whom the CLI runs scripts there.
type Instance struct {
	ID string
	// SSHHost and SSHPort are where SSH answers, reachable from wherever
	// the executor runs (a localhost port forward for qemu).
	SSHHost string
	SSHPort int
	// User and KeyPath are the CLI's own admin identity on the VM, used
	// for init, bootstrap, testing and teardown scripts. The student's
	// login is separate and not part of this interface.
	User       string
	KeyPath    string
	KnownHosts string
	// Console is where the serial log can be read, "" if nowhere.
	Console string
}

// Provider creates, finds and deletes VMs on one backend.
type Provider interface {
	// Create starts the VM described by s and returns once SSH answers.
	// On failure nothing of the VM is left behind.
	Create(ctx context.Context, s Spec) (Instance, error)
	// Get returns the running VM with this ID, or an error when it does
	// not exist or is no longer running.
	Get(ctx context.Context, id string) (Instance, error)
	// Delete removes the VM and everything it left behind. Deleting a VM
	// that is already gone succeeds.
	Delete(ctx context.Context, id string) error
}
