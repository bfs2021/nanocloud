package hypervisor_test

import (
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/hypervisor"
	"github.com/nanocloud-io/nanocloud/internal/vm"
)

// The libvirt backend must keep satisfying Backend and its optional
// interfaces.
var (
	_ hypervisor.Backend          = (*vm.Manager)(nil)
	_ hypervisor.Snapshotter      = (*vm.Manager)(nil)
	_ hypervisor.VNCConsoler      = (*vm.Manager)(nil)
	_ hypervisor.HostDeviceLister = (*vm.Manager)(nil)
)

func TestCapabilitiesOfLibvirtManager(t *testing.T) {
	got := hypervisor.CapabilitiesOf(vm.NewLazy("test:///nonexistent"))
	if got != hypervisor.LibvirtCapabilities() {
		t.Fatalf("CapabilitiesOf(*vm.Manager) = %+v, want the libvirt set", got)
	}
	if got.SerialConsole {
		t.Error("libvirt backend must not advertise a serial console the API never exposed")
	}
}

func TestCapabilitiesOfNil(t *testing.T) {
	if got := hypervisor.CapabilitiesOf(nil); got.Backend != hypervisor.BackendLibvirt {
		t.Fatalf("CapabilitiesOf(nil).Backend = %q, want %q", got.Backend, hypervisor.BackendLibvirt)
	}
}

type capableFake struct {
	hypervisor.Backend
}

func (capableFake) Capabilities() hypervisor.Capabilities {
	return hypervisor.Capabilities{Backend: hypervisor.BackendVZ, SerialConsole: true}
}

func TestCapabilitiesOfCapableBackend(t *testing.T) {
	got := hypervisor.CapabilitiesOf(capableFake{})
	if got.Backend != hypervisor.BackendVZ || !got.SerialConsole || got.Snapshots {
		t.Fatalf("CapabilitiesOf(capable) = %+v, want the backend's own set", got)
	}
}
