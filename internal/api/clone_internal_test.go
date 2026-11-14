package api

import (
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

// cloneEntry decides what a clone inherits. Each row is something it must not
// share with its source; shared disks would corrupt both VMs.
func TestCloneEntry_KeepsTheShapeAndNotTheIdentity(t *testing.T) {
	source := metadata.VMEntry{
		ID: "web-01", Hostname: "web-01", PoolPath: "/pool",
		UUID: "46acc901-1f2e-4a3b-8c7d-5e6f70819203", MAC: "52:54:00:dc:cb:18",
		VCPUs: 4, MemoryMB: 4096, ImageID: "ubuntu-24.04", NetworkMode: "bridge",
		CloudInitUser: "alex", StartedAtUnix: 1754800000,
		Disks:      []metadata.Disk{{Target: "vdb", Path: "/pool/web-01-vdb.qcow2", SizeGB: 20}},
		PCIDevices: []metadata.PCIDevice{{Address: "0000:01:00.0"}},
	}
	source.SetRunning(true)

	got, warnings := cloneEntry(source, "web-02", false)

	if got.ID != "web-02" || got.Hostname != "web-02" {
		t.Errorf("id/hostname = %q/%q, want web-02 for both", got.ID, got.Hostname)
	}
	// A copied UUID fails the define outright; a copied MAC puts two guests on
	// one DHCP lease.
	if got.UUID != "" || got.MAC != "" {
		t.Errorf("clone inherited the source's identity (uuid=%q mac=%q); a clone is a second machine and needs its own", got.UUID, got.MAC)
	}
	if got.Disks != nil {
		t.Errorf("disks = %+v, want none — the caller refills these with the clone's own copies", got.Disks)
	}
	if running, _ := got.DesiredRunning(); running {
		t.Error("running = true, want the request's answer (false) rather than the source's")
	}
	if got.StartedAtUnix != 0 {
		t.Errorf("started_at = %d, want 0 — a clone that is not started has no uptime", got.StartedAtUnix)
	}
	if len(got.PCIDevices) != 0 {
		t.Errorf("pci_devices = %+v, want none: a host device attaches to one running VM at a time", got.PCIDevices)
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %v, want one saying the devices were not copied", warnings)
	}

	// The shape does carry over, or this would pass for a clone that
	// inherited nothing.
	if got.VCPUs != 4 || got.MemoryMB != 4096 || got.ImageID != "ubuntu-24.04" ||
		got.NetworkMode != "bridge" || got.CloudInitUser != "alex" || got.PoolPath != "/pool" {
		t.Errorf("clone lost part of the machine it was copied from: %+v", got)
	}

	// And the source is untouched: it is passed by value, but its slices are not.
	if len(source.Disks) != 1 || len(source.PCIDevices) != 1 {
		t.Errorf("cloning mutated the source: %+v", source)
	}
}

func TestCloneEntry_StartedClonesGetAnUptime(t *testing.T) {
	got, _ := cloneEntry(metadata.VMEntry{ID: "web-01"}, "web-02", true)

	if running, _ := got.DesiredRunning(); !running {
		t.Error("running = false for a clone the request asked to start")
	}
	if got.StartedAtUnix == 0 {
		t.Error("started_at = 0 for a started clone, which reports as running with no uptime")
	}
}

// A copy of a service's VM is a plain VM.
func TestCloneEntry_DropsTheServiceLabel(t *testing.T) {
	got, _ := cloneEntry(metadata.VMEntry{ID: "db", Service: "db"}, "db-copy", false)
	if got.Service != "" {
		t.Errorf("clone of a service VM keeps service %q", got.Service)
	}
}
