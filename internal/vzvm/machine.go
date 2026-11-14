// Package vzvm runs nano-cloud's VMs on macOS with Apple's
// Virtualization.framework. It implements hypervisor.Backend, so the API,
// reconcile loop and stats sampler run unchanged on top of it.
//
// The framework has no daemon: a VM lives and dies with the process that
// created it. Each running VM therefore gets a detached helper process
// (`nanocloud vz-machine`) that owns the machine and serves a unix socket, so
// restarting or upgrading the daemon leaves guests running.
//
// Only vz_darwin.go needs cgo and the macOS SDK. The rest is plain Go, tested
// on every platform against a fake Machine.
package vzvm

import (
	"fmt"
	"net"
	"os"
)

// MachineState is the lifecycle state of one running Virtualization.framework
// machine. The values mirror VZVirtualMachineState.
type MachineState int

// The states a Machine reports. Stopped and Error are terminal: a machine
// that reaches either is discarded, and the next start builds a new one.
const (
	MachineStopped MachineState = iota
	MachineRunning
	MachinePaused
	MachineError
	MachineStarting
	MachinePausing
	MachineResuming
	MachineStopping
	MachineSaving
	MachineRestoring
)

func (s MachineState) terminal() bool { return s == MachineStopped || s == MachineError }

// String names a state for messages ("cannot stop while starting").
func (s MachineState) String() string {
	names := [...]string{"stopped", "running", "paused", "in an error state", "starting",
		"pausing", "resuming", "stopping", "saving", "restoring"}
	if s < 0 || int(s) >= len(names) {
		return fmt.Sprintf("in state %d", int(s))
	}
	return names[s]
}

// Machine is one booted virtual machine.
type Machine interface {
	// Start boots the machine and returns once it is running or has failed.
	Start() error
	// RequestStop asks the guest to power off and returns once the request is
	// delivered, not once the guest is off.
	RequestStop() error
	// Stop powers the machine off immediately, without the guest's cooperation.
	Stop() error
	State() MachineState
	// StateChanges delivers every state change, in order, from creation on.
	// It is never closed; the consumer stops reading at a terminal state.
	StateChanges() <-chan MachineState
	// Pause freezes the guest's vCPUs; Resume lets them run again.
	Pause() error
	Resume() error
}

// Disk is one block device, in the order the guest enumerates them
// (/dev/vda, /dev/vdb, …).
type Disk struct {
	Path     string `json:"path"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// MachineConfig is the part of a machine's description that travels from the
// daemon to its helper process, as JSON.
type MachineConfig struct {
	Name        string `json:"name"`
	CPUs        uint   `json:"cpus"`
	MemoryBytes uint64 `json:"memory_bytes"`
	// Disks are attached as virtio block devices in this order: the boot disk
	// first, then extra disks, then the cloud-init seed read-only.
	Disks []Disk `json:"disks"`
	// MAC is the NIC's address, "52:54:00:ab:cd:ef".
	MAC string `json:"mac"`
	// NVRAMPath holds the EFI variable store; created when missing.
	NVRAMPath string `json:"nvram_path"`
	// MachineIDPath holds the platform's machine identifier; created when
	// missing so the guest sees the same machine on every boot.
	MachineIDPath string `json:"machine_id_path"`
}

// MachineSpec is everything needed to build one machine: its configuration,
// plus the console the helper process wires it to.
type MachineSpec struct {
	MachineConfig
	// SerialIn and SerialOut are the guest's console, /dev/hvc0.
	SerialIn  *os.File
	SerialOut *os.File
	// Network is the VM's end of its link into the switch, or nil for Apple's
	// NAT.
	Network *os.File
}

// HardwareAddr parses MAC.
func (c MachineConfig) HardwareAddr() (net.HardwareAddr, error) {
	return net.ParseMAC(c.MAC)
}

// Launcher builds a Machine from a spec without starting it.
type Launcher func(MachineSpec) (Machine, error)
