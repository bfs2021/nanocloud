package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/nanocloud-io/nanocloud/internal/hostdev"
	"github.com/nanocloud-io/nanocloud/internal/hypervisor"
	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/network"
	"github.com/nanocloud-io/nanocloud/internal/storage"
	"github.com/nanocloud-io/nanocloud/internal/vm"
)

// specResponse is the body of both GET and PATCH /vms/{id}/spec.
type specResponse struct {
	// PCIDevices and USBDevices are the resolved set, which is not always the
	// set that was asked for: picking a GPU brings its IOMMU-group siblings.
	PCIDevices []metadata.PCIDevice `json:"pci_devices"`
	USBDevices []metadata.USBDevice `json:"usb_devices"`

	// CPU reports the effective shape: no recorded mode reads as host-model.
	CPU cpuShapeBody `json:"cpu"`

	// Network reports the effective networking, with stored defaults resolved.
	Network networkBody `json:"network"`

	// Disks lists the boot overlay first, then the attached disks.
	Disks []diskJSON `json:"disks"`

	// RestartRequired means the running domain differs from the spec.
	RestartRequired bool `json:"restart_required"`

	// Warnings are caveats about the current selection, recomputed on each read.
	Warnings []string `json:"warnings"`
}

// specPatch is the PATCH body. Its fields are pointers so that "leave this
// alone" and "make this empty" are different requests.
type specPatch struct {
	PCIDevices *[]string `json:"pci_devices"`
	USBDevices *[]string `json:"usb_devices"`
	// CPU replaces the whole shape at once: its three fields are one property,
	// and "no topology" is a value within it.
	CPU *cpuShapeBody `json:"cpu"`
	// Network is one group for the same reason.
	Network *networkBody `json:"network"`
}

// cpuShapeBody is a VM's CPU shape in POST /vms and PATCH /vms/{id}/spec.
type cpuShapeBody struct {
	// Mode is metadata.CPUHostModel or metadata.CPUHostPassthrough; empty
	// means host-model.
	Mode string `json:"mode"`
	// Topology is null when the guest's layout is left to libvirt.
	Topology *metadata.CPUTopology `json:"topology"`
	// CPUSet is libvirt's cpuset syntax ("4-7", "0,2,4", "0-7,^4"). Empty is
	// unpinned.
	CPUSet string `json:"cpu_set"`
}

// normalise resolves a body to what metadata.json stores. host-model is
// stored as empty, so only non-default VMs carry the key.
func (b cpuShapeBody) normalise() cpuShapeBody {
	b.Mode = strings.TrimSpace(b.Mode)
	if b.Mode == metadata.CPUHostModel {
		b.Mode = ""
	}
	b.CPUSet = strings.TrimSpace(b.CPUSet)
	return b
}

// applyTo writes a normalised shape onto an entry.
func (b cpuShapeBody) applyTo(e *metadata.VMEntry) {
	e.CPUMode, e.CPUTopology, e.CPUSet = b.Mode, b.Topology, b.CPUSet
}

// cpuShapeOf reads an entry's shape back out, resolving the stored default.
func cpuShapeOf(e metadata.VMEntry) cpuShapeBody {
	mode := e.CPUMode
	if mode == "" {
		mode = metadata.CPUHostModel
	}
	return cpuShapeBody{Mode: mode, Topology: e.CPUTopology, CPUSet: e.CPUSet}
}

// networkBody is a VM's networking in POST /vms and PATCH /vms/{id}/spec.
type networkBody struct {
	// Mode is metadata.NetworkNAT or metadata.NetworkBridge; empty means NAT.
	Mode   string `json:"mode"`
	Bridge string `json:"bridge"`
	VLANID int    `json:"vlan_id"`
	// VLANUplink is chosen by the server and only ever reported.
	VLANUplink string `json:"vlan_uplink,omitempty"`
}

// normalise resolves a body to what metadata.json stores; the default bridge
// is stored as empty. VLANUplink is always cleared, because it decodes from
// client JSON but only the server may set it.
func (b networkBody) normalise() networkBody {
	b.Mode = strings.TrimSpace(b.Mode)
	if b.Mode == "" {
		b.Mode = metadata.NetworkNAT
	}
	b.Bridge = strings.TrimSpace(b.Bridge)
	if b.Bridge == metadata.DefaultBridge {
		b.Bridge = ""
	}
	b.VLANUplink = ""
	return b
}

// VLANProvisioner is the part of *hostnet.Manager the API calls, declared
// here so tests need no host network.
type VLANProvisioner interface {
	Ensure(uplink string, vid int) (string, error)
	DefaultRouteInterface() (string, error)
}

// provisionVLAN fills in the bridge and uplink for a tagged request, creating
// the VLAN link if needed. It is a no-op for NAT and untagged bridges. Call it
// after normalise and before applyTo.
//
// It runs before validateNetwork and before any file is written, so a refused
// request may leave the VLAN link and bridge behind; both are additive. The
// range check keeps an impossible tag a 400 rather than Ensure's 422.
func (s *Server) provisionVLAN(netw *networkBody) (int, error) {
	if netw.Mode != metadata.NetworkBridge || netw.VLANID <= 0 {
		return 0, nil
	}
	if netw.VLANID > metadata.MaxVLANID {
		return http.StatusBadRequest, fmt.Errorf(
			"VLAN %d is not a valid 802.1Q tag — it must be 1-%d, or 0 for untagged", netw.VLANID, metadata.MaxVLANID)
	}
	if s.hostNet == nil {
		return http.StatusServiceUnavailable, errors.New(
			"VLAN provisioning is unavailable: this daemon has no host network manager configured")
	}
	uplink, err := s.hostNet.DefaultRouteInterface()
	if err != nil {
		return http.StatusUnprocessableEntity, err
	}
	bridge, err := s.hostNet.Ensure(uplink, netw.VLANID)
	if err != nil {
		return http.StatusUnprocessableEntity, err
	}
	netw.Bridge, netw.VLANUplink = bridge, uplink
	return 0, nil
}

// applyTo writes a normalised body onto an entry.
func (b networkBody) applyTo(e *metadata.VMEntry) {
	e.NetworkMode, e.Bridge, e.VLANID = b.Mode, b.Bridge, b.VLANID
	e.VLANUplink = b.VLANUplink
}

// networkOf reads an entry's networking back out, resolving stored defaults.
// A NAT VM reports no bridge: libvirt owns virbr0.
func networkOf(e metadata.VMEntry) networkBody {
	if e.NetworkMode != metadata.NetworkBridge {
		return networkBody{Mode: metadata.NetworkNAT}
	}
	return networkBody{
		Mode: metadata.NetworkBridge, Bridge: e.BridgeOf(),
		VLANID: e.VLANID, VLANUplink: e.VLANUplink,
	}
}

// validateNetwork checks a candidate entry's networking: a request no host
// could satisfy is a 400, one only this host cannot is a 422. A nil bridges
// slice means the host could not be read and skips the host checks; an empty
// one is a real answer.
func validateNetwork(e metadata.VMEntry, bridges []network.Bridge) (int, error) {
	switch e.NetworkMode {
	case "", metadata.NetworkNAT:
		if e.Bridge != "" || e.VLANID != 0 {
			return http.StatusBadRequest, fmt.Errorf(
				"a bridge or VLAN cannot be set on NAT: virbr0 is host-local behind a NAT, so a tag on it is invisible to everything outside this host")
		}
		return 0, nil
	case metadata.NetworkBridge:
	default:
		return http.StatusBadRequest, fmt.Errorf("network mode %q is not %s or %s", e.NetworkMode, metadata.NetworkNAT, metadata.NetworkBridge)
	}

	if e.VLANID < 0 || e.VLANID > metadata.MaxVLANID {
		return http.StatusBadRequest, fmt.Errorf("VLAN %d is not a valid 802.1Q tag — it must be 1-%d, or 0 for untagged", e.VLANID, metadata.MaxVLANID)
	}

	if bridges == nil {
		return 0, nil
	}

	// A tagged VM attaches to the nc-vlan<id> bridge Ensure creates, so the
	// host check is on its uplink.
	if e.VLANID > 0 {
		if e.VLANUplink == "" {
			return http.StatusUnprocessableEntity, fmt.Errorf(
				"VLAN %d cannot be provisioned: this host has no default route, so there is no interface to put it on", e.VLANID)
		}
		return 0, nil
	}

	for _, b := range bridges {
		if b.Name == e.BridgeOf() {
			return 0, nil
		}
	}
	return http.StatusUnprocessableEntity, fmt.Errorf(
		"this host has no bridge named %q with a physical port — a bridge with no way off this host cannot carry a VM to your network", e.BridgeOf())
}

// hostBridges lists the host's bridges for validateNetwork, or nil if it cannot.
func (s *Server) hostBridges() []network.Bridge {
	if s.netManager == nil {
		return nil
	}
	bridges, err := s.netManager.Bridges()
	if err != nil {
		return nil
	}
	return network.WithUplink(bridges)
}

// validateCPUShape checks a candidate entry's CPU: a shape wrong on its own
// terms is a 400, one wrong only on this host (a cpuset naming CPUs it lacks)
// is a 422.
func validateCPUShape(e metadata.VMEntry, hostCPUs int) (int, error) {
	if err := vm.ValidateCPU(e, 0); err != nil {
		return http.StatusBadRequest, err
	}
	if err := vm.ValidateCPU(e, hostCPUs); err != nil {
		return http.StatusUnprocessableEntity, err
	}
	return 0, nil
}

// handleVMSpec handles GET /vms/{id}/spec.
func (s *Server) handleVMSpec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	entry, ok := s.specEntry(w, id)
	if !ok {
		return
	}
	writeJSON(w, s.specResponseFor(entry))
}

// specResponseFor renders an entry as every spec and disk endpoint answers.
// Warnings are derived on each read, since the host can change under a spec.
func (s *Server) specResponseFor(e metadata.VMEntry) specResponse {
	warnings := []string{}
	if report, err := s.deviceReport(); err == nil {
		if res, resErr := hostdev.Resolve(report, selectionOf(e)); resErr == nil {
			warnings = res.Warnings
		} else {
			warnings = append(warnings, resErr.Error())
		}
	}

	return specResponse{
		PCIDevices:      nonNil(e.PCIDevices),
		USBDevices:      nonNil(e.USBDevices),
		CPU:             cpuShapeOf(e),
		Network:         networkOf(e),
		Disks:           disksOf(e),
		RestartRequired: s.restartRequired(e),
		Warnings:        warnings,
	}
}

// handleVMSpecPatch handles PATCH /vms/{id}/spec, the write path for
// configuration that changes no files. Power state is not patchable: start
// and stop own it.
func (s *Server) handleVMSpecPatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var patch specPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ask := featureAsk{}
	if patch.CPU != nil {
		ask.CPUTuned = cpuTuned(*patch.CPU)
	}
	if patch.PCIDevices != nil {
		ask.Devices += len(*patch.PCIDevices)
	}
	if patch.USBDevices != nil {
		ask.Devices += len(*patch.USBDevices)
	}
	if patch.Network != nil {
		ask.NetworkMode, ask.VLANID = patch.Network.Mode, patch.Network.VLANID
	}
	if err := s.unsupportedFeature(ask); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	entry, ok := s.specEntry(w, id)
	if !ok {
		return
	}
	before := entry

	// cpu stays nil when the patch omits it, so a device change cannot rewrite
	// the stored CPU.
	var cpu *cpuShapeBody
	if patch.CPU != nil {
		shape := patch.CPU.normalise()
		shape.applyTo(&entry)
		if status, cpuErr := validateCPUShape(entry, s.vmManager.HostCPUs()); cpuErr != nil {
			http.Error(w, cpuErr.Error(), status)
			return
		}
		cpu = &shape
	}

	var net *networkBody
	if patch.Network != nil {
		n := patch.Network.normalise()
		if status, provErr := s.provisionVLAN(&n); provErr != nil {
			http.Error(w, provErr.Error(), status)
			return
		}
		n.applyTo(&entry)
		if status, netErr := validateNetwork(entry, s.hostBridges()); netErr != nil {
			http.Error(w, netErr.Error(), status)
			return
		}
		net = &n
	}

	sel := selectionOf(entry)
	if patch.PCIDevices != nil {
		sel.PCI = *patch.PCIDevices
	}
	if patch.USBDevices != nil {
		sel.USB = *patch.USBDevices
	}

	report, err := s.deviceReport()
	if err != nil {
		writeVMManagerError(w, "list host devices", err)
		return
	}
	resolved, err := hostdev.Resolve(report, sel)
	if err != nil {
		// 422, not 400: the request is fine, but this host cannot satisfy it.
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	pci, usb := specDevicesOf(resolved)
	entry.PCIDevices, entry.USBDevices = pci, usb

	// copyPatched writes only the groups this patch names, so neither the
	// write nor its rollback can disturb a field the request never mentioned.
	copyPatched := func(src metadata.VMEntry) func(*metadata.VMEntry) {
		return func(e *metadata.VMEntry) {
			e.PCIDevices, e.USBDevices = src.PCIDevices, src.USBDevices
			if cpu != nil {
				e.CPUMode, e.CPUTopology, e.CPUSet = src.CPUMode, src.CPUTopology, src.CPUSet
			}
			if net != nil {
				e.NetworkMode, e.Bridge, e.VLANID, e.VLANUplink = src.NetworkMode, src.Bridge, src.VLANID, src.VLANUplink
			}
		}
	}

	// Spec before action, as on every lifecycle path: the reverse order can
	// leave a domain holding a device nothing recorded.
	if err := s.writeSpec(id, copyPatched(entry)); err != nil {
		http.Error(w, fmt.Sprintf("update %q: could not record the change: %v", id, err), http.StatusInternalServerError)
		return
	}

	if err := s.redefine(entry); err != nil {
		// Nothing retries a failed define, so a kept spec would report
		// restart_required that no restart can clear.
		if rbErr := s.writeSpec(id, copyPatched(before)); rbErr != nil {
			err = fmt.Errorf("%w; the spec could not be rolled back either, so it now describes a machine libvirt does not have: %v", err, rbErr)
		}
		writeVMManagerError(w, fmt.Sprintf("apply hardware change to %q", id), err)
		return
	}

	writeJSON(w, s.specResponseFor(entry))
}

// specEntry loads the VM's spec, writing the error response itself when it
// cannot. A VM with no entry, such as one defined with virsh, is a 404.
func (s *Server) specEntry(w http.ResponseWriter, id string) (metadata.VMEntry, bool) {
	if s.metaStore == nil {
		http.Error(w, "this daemon has no metadata store, so VM specs cannot be read or changed", http.StatusServiceUnavailable)
		return metadata.VMEntry{}, false
	}
	entry, err := s.metaStore.Get(id)
	if errors.Is(err, metadata.ErrNotFound) {
		http.Error(w, fmt.Sprintf("no spec recorded for %q — nano-cloud did not create this VM", id), http.StatusNotFound)
		return metadata.VMEntry{}, false
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("read spec for %q: %v", id, err), http.StatusInternalServerError)
		return metadata.VMEntry{}, false
	}
	return entry, true
}

// redefine rebuilds the domain from the spec, keeping the identity libvirt
// gave it. An entry with no pool is refused rather than pointed at a relative
// path; the existing domain still names the right files.
func (s *Server) redefine(e metadata.VMEntry) error {
	disk, iso, ok := storage.PathsFor(e.PoolPath, e.ID)
	if !ok {
		return fmt.Errorf("%q has no storage pool recorded, so its domain cannot be rebuilt", e.ID)
	}
	if err := s.ensureVLAN(e); err != nil {
		return err
	}
	return s.vmManager.RedefineFromSpec(e, disk, iso)
}

// ensureVLAN creates the VLAN bridge an entry names before a domain is defined
// against it. Every define path needs this: libvirt accepts a missing bridge,
// and the VM then fails at its next boot. An entry with no recorded uplink is
// skipped, as the reconcile loop skips it.
func (s *Server) ensureVLAN(e metadata.VMEntry) error {
	if s.hostNet == nil || e.VLANUplink == "" || e.VLANID <= 0 {
		return nil
	}
	_, err := s.hostNet.Ensure(e.VLANUplink, e.VLANID)
	return err
}

// restartRequired reports whether the running domain differs from the spec.
// A stopped VM never does. An unreachable libvirt reports false rather than
// sending the user to reboot a VM that may already be correct.
func (s *Server) restartRequired(e metadata.VMEntry) bool {
	live, err := s.vmManager.ActiveHardware(e.ID)
	if err != nil {
		return false
	}
	return !reflect.DeepEqual(live.HostDevs, vm.HostDevRefs(e)) ||
		vm.CPUShapeDrifted(vm.CPUShapeOf(e), live.CPU) ||
		live.Network != vm.NetworkShapeOf(e) ||
		!reflect.DeepEqual(live.DiskTargets, vm.DiskTargets(e))
}

// deviceReport builds the host inventory the same way GET /system/devices does.
func (s *Server) deviceReport() (hostdev.Report, error) {
	lister, ok := s.vmManager.(hypervisor.HostDeviceLister)
	if !ok {
		return noPassthroughReport(s.capabilities().Backend), nil
	}
	xmls, err := lister.ListNodeDeviceXML()
	if err != nil {
		return hostdev.Report{}, err
	}
	return hostdev.Build(xmls, ""), nil
}

// selectionOf expresses an entry's current devices as a selection, so a patch
// that names only one list leaves the other exactly as it was.
func selectionOf(e metadata.VMEntry) hostdev.Selection {
	sel := hostdev.Selection{PCI: make([]string, 0, len(e.PCIDevices)), USB: make([]string, 0, len(e.USBDevices))}
	for _, d := range e.PCIDevices {
		sel.PCI = append(sel.PCI, d.Address)
	}
	for _, d := range e.USBDevices {
		sel.USB = append(sel.USB, d.VendorID+":"+d.ProductID)
	}
	return sel
}

// specDevicesOf narrows resolved devices to what the spec records, taking the
// display names from the host inventory rather than the request.
func specDevicesOf(r hostdev.Resolved) ([]metadata.PCIDevice, []metadata.USBDevice) {
	pci := make([]metadata.PCIDevice, 0, len(r.PCI))
	for _, d := range r.PCI {
		pci = append(pci, metadata.PCIDevice{Address: d.Address, Vendor: d.Vendor, Product: d.Product})
	}
	usb := make([]metadata.USBDevice, 0, len(r.USB))
	for _, d := range r.USB {
		usb = append(usb, metadata.USBDevice{VendorID: d.VendorID, ProductID: d.ProductID, Description: d.Description})
	}
	return pci, usb
}

// nonNil keeps a JSON array empty rather than null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, http.StatusOK, v)
}

// writeJSONStatus is writeJSON with a status. Content-Type must be set before
// WriteHeader sends the headers.
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck // headers are already sent; a write failure is the client's disconnect
}
