//go:build darwin && cgo

package vzvm

import (
	"errors"
	"fmt"
	"os"
	"sync"

	vz "github.com/Code-Hex/vz/v3"
)

// This is the only file that calls Virtualization.framework, and it runs only
// in a VM's helper process. The binary must be signed with the
// com.apple.security.virtualization entitlement or every machine fails to
// validate.

// Launch builds a Virtualization.framework machine from s.
func Launch(s MachineSpec) (Machine, error) {
	if err := checkLimits(s); err != nil {
		return nil, err
	}

	store, err := efiVariableStore(s.NVRAMPath)
	if err != nil {
		return nil, fmt.Errorf("EFI variable store %s: %w", s.NVRAMPath, err)
	}
	boot, err := vz.NewEFIBootLoader(vz.WithEFIVariableStore(store))
	if err != nil {
		return nil, fmt.Errorf("EFI boot loader: %w", err)
	}
	cfg, err := vz.NewVirtualMachineConfiguration(boot, s.CPUs, s.MemoryBytes)
	if err != nil {
		return nil, fmt.Errorf("machine configuration: %w", err)
	}

	id, err := machineIdentifier(s.MachineIDPath)
	if err != nil {
		return nil, fmt.Errorf("machine identifier %s: %w", s.MachineIDPath, err)
	}
	platform, err := vz.NewGenericPlatformConfiguration(vz.WithGenericMachineIdentifier(id))
	if err != nil {
		return nil, fmt.Errorf("platform: %w", err)
	}
	cfg.SetPlatformVirtualMachineConfiguration(platform)

	disks := make([]vz.StorageDeviceConfiguration, 0, len(s.Disks))
	for _, d := range s.Disks {
		att, derr := diskAttachment(d)
		if derr != nil {
			return nil, fmt.Errorf("disk %s: %w", d.Path, derr)
		}
		dev, derr := vz.NewVirtioBlockDeviceConfiguration(att)
		if derr != nil {
			return nil, fmt.Errorf("disk %s: %w", d.Path, derr)
		}
		disks = append(disks, dev)
	}
	cfg.SetStorageDevicesVirtualMachineConfiguration(disks)

	nic, err := networkDevice(s.Network)
	if err != nil {
		return nil, fmt.Errorf("network device: %w", err)
	}
	hw, err := s.HardwareAddr()
	if err != nil {
		return nil, fmt.Errorf("MAC %q: %w", s.MAC, err)
	}
	mac, err := vz.NewMACAddress(hw)
	if err != nil {
		return nil, fmt.Errorf("MAC %s: %w", s.MAC, err)
	}
	nic.SetMACAddress(mac)
	cfg.SetNetworkDevicesVirtualMachineConfiguration([]*vz.VirtioNetworkDeviceConfiguration{nic})

	serial, err := vz.NewFileHandleSerialPortAttachment(s.SerialIn, s.SerialOut)
	if err != nil {
		return nil, fmt.Errorf("serial console: %w", err)
	}
	console, err := vz.NewVirtioConsoleDeviceSerialPortConfiguration(serial)
	if err != nil {
		return nil, fmt.Errorf("serial console: %w", err)
	}
	cfg.SetSerialPortsVirtualMachineConfiguration([]*vz.VirtioConsoleDeviceSerialPortConfiguration{console})

	entropy, err := vz.NewVirtioEntropyDeviceConfiguration()
	if err != nil {
		return nil, fmt.Errorf("entropy device: %w", err)
	}
	cfg.SetEntropyDevicesVirtualMachineConfiguration([]*vz.VirtioEntropyDeviceConfiguration{entropy})

	balloon, err := vz.NewVirtioTraditionalMemoryBalloonDeviceConfiguration()
	if err != nil {
		return nil, fmt.Errorf("memory balloon: %w", err)
	}
	cfg.SetMemoryBalloonDevicesVirtualMachineConfiguration([]vz.MemoryBalloonDeviceConfiguration{balloon})

	if ok, verr := cfg.Validate(); verr != nil || !ok {
		if verr == nil {
			verr = errors.New("rejected without a reason")
		}
		return nil, fmt.Errorf("invalid machine (is the binary signed with com.apple.security.virtualization? see `nanocloud doctor`): %w", verr)
	}

	v, err := vz.NewVirtualMachine(cfg)
	if err != nil {
		return nil, fmt.Errorf("create machine: %w", err)
	}
	return &vzMachine{vm: v}, nil
}

// networkDevice is a NIC on the VM's link into the switch, or on Apple's NAT
// when it has none.
func networkDevice(link *os.File) (*vz.VirtioNetworkDeviceConfiguration, error) {
	if link == nil {
		nat, err := vz.NewNATNetworkDeviceAttachment()
		if err != nil {
			return nil, fmt.Errorf("NAT network: %w", err)
		}
		return vz.NewVirtioNetworkDeviceConfiguration(nat)
	}
	// The framework duplicates the descriptor, so link outlives each boot.
	att, err := vz.NewFileHandleNetworkDeviceAttachment(link)
	if err != nil {
		return nil, fmt.Errorf("switch link: %w", err)
	}
	return vz.NewVirtioNetworkDeviceConfiguration(att)
}

// checkLimits names the limit a spec breaks, in the units the UI uses,
// instead of the framework's generic validation failure.
func checkLimits(s MachineSpec) error {
	if lo, hi := vz.VirtualMachineConfigurationMinimumAllowedCPUCount(), vz.VirtualMachineConfigurationMaximumAllowedCPUCount(); s.CPUs < lo || s.CPUs > hi {
		return fmt.Errorf("%d vCPUs is outside what this Mac allows (%d–%d)", s.CPUs, lo, hi)
	}
	if lo, hi := vz.VirtualMachineConfigurationMinimumAllowedMemorySize(), vz.VirtualMachineConfigurationMaximumAllowedMemorySize(); s.MemoryBytes < lo || s.MemoryBytes > hi {
		return fmt.Errorf("%d MiB of memory is outside what this Mac allows (%d–%d MiB)", s.MemoryBytes>>20, lo>>20, hi>>20)
	}
	return nil
}

// diskAttachment sets cached mode with fsync explicitly: under the framework's
// automatic modes, guests writing heavily got I/O errors and corrupt files.
func diskAttachment(d Disk) (*vz.DiskImageStorageDeviceAttachment, error) {
	if d.ReadOnly {
		return vz.NewDiskImageStorageDeviceAttachment(d.Path, true)
	}
	return vz.NewDiskImageStorageDeviceAttachmentWithCacheAndSync(d.Path, false,
		vz.DiskImageCachingModeCached, vz.DiskImageSynchronizationModeFsync)
}

// efiVariableStore opens the VM's NVRAM, creating it on first boot. It must
// persist because it holds the boot entries the guest's installer wrote.
func efiVariableStore(path string) (*vz.EFIVariableStore, error) {
	if _, err := os.Stat(path); err == nil {
		return vz.NewEFIVariableStore(path)
	}
	return vz.NewEFIVariableStore(path, vz.WithCreatingEFIVariableStore())
}

// machineIdentifier loads the VM's identifier, creating and saving one on
// first boot, so the guest sees the same machine every time.
func machineIdentifier(path string) (*vz.GenericMachineIdentifier, error) {
	if _, err := os.Stat(path); err == nil {
		return vz.NewGenericMachineIdentifierWithDataPath(path)
	}
	id, err := vz.NewGenericMachineIdentifier()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, id.DataRepresentation(), 0o600); err != nil {
		return nil, err
	}
	return id, nil
}

// vzMachine adapts *vz.VirtualMachine to Machine. Holding vm keeps the
// framework object alive for as long as the machine runs.
type vzMachine struct {
	vm *vz.VirtualMachine

	once   sync.Once
	states chan MachineState
}

func (m *vzMachine) Start() error { return m.vm.Start() }

func (m *vzMachine) RequestStop() error {
	if !m.vm.CanRequestStop() {
		return fmt.Errorf("the guest cannot be asked to stop while %s", stateOf(m.vm.State()))
	}
	ok, err := m.vm.RequestStop()
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("the guest did not accept the stop request")
	}
	return nil
}

func (m *vzMachine) Stop() error { return m.vm.Stop() }

func (m *vzMachine) Pause() error {
	if !m.vm.CanPause() {
		return fmt.Errorf("the VM cannot be paused while %s", stateOf(m.vm.State()))
	}
	return m.vm.Pause()
}

func (m *vzMachine) Resume() error {
	if !m.vm.CanResume() {
		return fmt.Errorf("the VM cannot be resumed while %s", stateOf(m.vm.State()))
	}
	return m.vm.Resume()
}

func (m *vzMachine) State() MachineState { return stateOf(m.vm.State()) }

// StateChanges forwards the framework's notifications, which it buffers from
// the moment the machine is created, so nothing before the first call is lost.
func (m *vzMachine) StateChanges() <-chan MachineState {
	m.once.Do(func() {
		m.states = make(chan MachineState, 16)
		go func() {
			for st := range m.vm.StateChangedNotify() {
				s := stateOf(st)
				m.states <- s
				if s.terminal() {
					return
				}
			}
		}()
	})
	return m.states
}

func stateOf(s vz.VirtualMachineState) MachineState {
	switch s {
	case vz.VirtualMachineStateRunning:
		return MachineRunning
	case vz.VirtualMachineStatePaused:
		return MachinePaused
	case vz.VirtualMachineStateError:
		return MachineError
	case vz.VirtualMachineStateStarting:
		return MachineStarting
	case vz.VirtualMachineStatePausing:
		return MachinePausing
	case vz.VirtualMachineStateResuming:
		return MachineResuming
	case vz.VirtualMachineStateStopping:
		return MachineStopping
	case vz.VirtualMachineStateSaving:
		return MachineSaving
	case vz.VirtualMachineStateRestoring:
		return MachineRestoring
	default:
		return MachineStopped
	}
}
