package config

import (
	"time"
)

// QEMUImageSource describes the source of the base image used for QEMU.
type QEMUImageSource struct {
	Type      string            `yaml:"type"` // "file", "url", or "oci"
	Source    string            `yaml:"source"`
	Checksum  string            `yaml:"checksum"`
	Checksums map[string]string `yaml:"checksums"`
	File      string            `yaml:"file"`
}

// QEMUExtraDisk describes one additional blank disk attached alongside the
// main disk.
type QEMUExtraDisk struct {
	SizeGB int    `yaml:"sizeGB"`
	Format string `yaml:"format"`
	Serial string `yaml:"serial"`
}

// QEMUNetworkDef declares one named virtual network segment at the
// runtime.networks level.
type QEMUNetworkDef struct {
	Name string `yaml:"name"`
	CIDR string `yaml:"cidr"`
}

// QEMUNetwork attaches one additional NIC to a VM, joining a segment already
// declared in runtime.networks.
type QEMUNetwork struct {
	Name string `yaml:"name"`
	IPv4 string `yaml:"ipv4"`
}

// QEMUNetworkStatus is what a resolved qemuNetworkSpec looks like once
// persisted into QEMUHandle.
type QEMUNetworkStatus struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
	MAC  string `json:"mac"`
}

// QEMUConfig is the qemu-specific block of a lab's runtime config.
type QEMUConfig struct {
	Image           QEMUImageSource `yaml:"image"`
	Arch            string          `yaml:"arch"`
	CPUs            int             `yaml:"cpus"`
	MemoryMB        int             `yaml:"memoryMB"`
	DiskSizeGB      int             `yaml:"diskSizeGB"`
	ExtraDisks      []QEMUExtraDisk `yaml:"extraDisks"`
	SSHPort         int             `yaml:"sshPort"`
	Display         bool            `yaml:"display"`
	SSHPasswordAuth bool            `yaml:"sshPasswordAuth"`
}

// QEMUHandle is what a running VM looks like to the rest of the CLI.
type QEMUHandle struct {
	ClusterName string              `json:"clusterName"`
	PID         int                 `json:"pid"`
	SSHHost     string              `json:"sshHost"`
	SSHPort     int                 `json:"sshPort"`
	SSHUser     string              `json:"sshUser"`
	SSHKeyPath  string              `json:"sshKeyPath"`
	KnownHosts  string              `json:"knownHosts"`
	StateDir    string              `json:"stateDir"`
	StartedAt   time.Time           `json:"startedAt"`
	Networks    []QEMUNetworkStatus `json:"networks"`
}
