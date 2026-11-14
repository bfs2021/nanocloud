package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/nanocloud-io/nanocloud/internal/hostdev"
	"github.com/nanocloud-io/nanocloud/internal/hypervisor"
	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/vm"
	"github.com/nanocloud-io/nanocloud/internal/vswitch"
)

// This file holds the API's side of features only some hypervisor backends
// have. The libvirt backend has all of them.

// capabilities reports what this daemon's backend can do.
func (s *Server) capabilities() hypervisor.Capabilities {
	return hypervisor.CapabilitiesOf(s.vmManager)
}

// snapshots returns the backend as a Snapshotter, or a stand-in whose every
// method fails with hypervisor.ErrUnsupported.
func (s *Server) snapshots() hypervisor.Snapshotter {
	if sn, ok := s.vmManager.(hypervisor.Snapshotter); ok {
		return sn
	}
	return noSnapshots{backend: s.capabilities().Backend}
}

type noSnapshots struct{ backend string }

func (n noSnapshots) err() error {
	return fmt.Errorf("snapshots on the %s backend: %w", n.backend, hypervisor.ErrUnsupported)
}

func (n noSnapshots) ListSnapshots(string) ([]vm.Snapshot, error) { return nil, n.err() }
func (n noSnapshots) CreateSnapshot(string, string, string) error { return n.err() }
func (n noSnapshots) DeleteSnapshot(string, string) error         { return n.err() }
func (n noSnapshots) RestoreSnapshot(string, string) error        { return n.err() }

// noPassthroughReport is GET /system/devices on a backend with no PCI or USB
// passthrough: the IOMMU-off report, without the kernel-cmdline remedy.
func noPassthroughReport(backend string) hostdev.Report {
	return hostdev.Report{
		IOMMU: hostdev.IOMMUStatus{
			Message: fmt.Sprintf("PCI/USB passthrough is not available on the %s backend", backend),
		},
		PCI: []hostdev.PCIDevice{},
		USB: []hostdev.USBDevice{},
	}
}

// featureAsk is the part of a create or spec-change request that a backend
// might not support.
type featureAsk struct {
	SecureBoot bool
	TPM        bool
	// CPUTuned is a CPU shape other than the default: a CPU model, a fixed
	// topology, or pinning.
	CPUTuned    bool
	Devices     int
	NetworkMode string
	VLANID      int
}

// unsupportedFeature names the first thing in a that this backend cannot do,
// or returns nil. Handlers call it before writing anything.
func (s *Server) unsupportedFeature(a featureAsk) error {
	c := s.capabilities()
	var missing []string
	if a.SecureBoot && !c.UEFIToggle {
		missing = append(missing, "secure boot")
	}
	if a.TPM && !c.TPM {
		missing = append(missing, "a TPM")
	}
	if a.CPUTuned && !c.CPUTuning {
		missing = append(missing, "CPU model, topology or pinning")
	}
	if a.Devices > 0 && !c.Passthrough {
		missing = append(missing, "PCI/USB passthrough")
	}
	if strings.TrimSpace(a.NetworkMode) == metadata.NetworkBridge && !c.Bridge {
		missing = append(missing, "bridged networking")
	}
	if a.VLANID > 0 && !c.VLAN {
		missing = append(missing, "VLAN tagging")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("the %s backend does not support %s", c.Backend, strings.Join(missing, ", "))
}

// cpuTuned reports whether a CPU body departs from plain vCPUs.
func cpuTuned(b cpuShapeBody) bool {
	b = b.normalise()
	return b.Mode != "" || b.Topology != nil || b.CPUSet != ""
}

// networkModeOf is the mode a create request asks for: the network group
// wins over the legacy top-level field, as in networkBody.normalise.
func networkModeOf(req vmCreateRequest) string {
	if m := strings.TrimSpace(req.Network.Mode); m != "" {
		return m
	}
	return req.NetworkMode
}

// handleNetworkSwitch handles GET /network/switch: the switch linking this
// host's VMs, with each VM's port and traffic counters. It is 404 where there
// is no such switch (Linux, or a Mac set to vz_network: nat) and 503 when the
// switch is not answering.
func (s *Server) handleNetworkSwitch(w http.ResponseWriter, _ *http.Request) {
	sw, ok := s.vmManager.(hypervisor.Switcher)
	if !ok {
		http.Error(w, vswitch.ErrNoSwitch.Error(), http.StatusNotFound)
		return
	}
	rep, err := sw.SwitchStatus()
	switch {
	case errors.Is(err, vswitch.ErrNoSwitch):
		http.Error(w, err.Error(), http.StatusNotFound)
	case err != nil:
		http.Error(w, fmt.Sprintf("the network switch is not answering: %v", err), http.StatusServiceUnavailable)
	default:
		writeJSON(w, rep)
	}
}
