package api_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/api"
	"github.com/nanocloud-io/nanocloud/internal/config"
	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/template"
	"github.com/nanocloud-io/nanocloud/internal/vm"
)

// specFixture is a server wired to a test-driver libvirt and on-disk metadata
// and template stores.
type specFixture struct {
	srv       *api.Server
	store     *metadata.Store
	templates *template.Store
	mgr       *vm.Manager
	poolDir   string
}

// newSpecFixture defines a domain named vmName with no passthrough devices and
// records a matching spec entry. prov is needed only by tests that move a VM
// onto a tagged VLAN.
func newSpecFixture(t *testing.T, vmName string, prov ...api.VLANProvisioner) specFixture {
	t.Helper()

	var hostNet api.VLANProvisioner
	if len(prov) > 0 {
		hostNet = prov[0]
	}

	m := libvirtManager(t)

	poolDir := t.TempDir()
	if err := m.Define(vm.VMCreateParams{
		Name: vmName, DiskPath: filepath.Join(poolDir, vmName+".qcow2"),
		CloudInitISO: filepath.Join(poolDir, vmName+"-cidata.iso"),
		VCPUs:        1, MemoryMB: 512, NetworkMode: "nat",
	}); err != nil {
		t.Fatalf("define %s: %v", vmName, err)
	}
	t.Cleanup(func() { m.Delete(vmName) }) //nolint:errcheck // best-effort cleanup in test

	store := metadata.New(filepath.Join(t.TempDir(), "metadata.json"))
	if err := store.Upsert(vmName, func(e *metadata.VMEntry) {
		e.ID, e.PoolPath, e.VCPUs, e.MemoryMB, e.NetworkMode = vmName, poolDir, 1, 512, "nat"
		e.SetRunning(false)
	}); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	templates := template.New(filepath.Join(t.TempDir(), "templates.json"))

	return specFixture{
		srv: api.New(api.Deps{
			WebFS: minimalWebFS(), Cfg: &config.Config{}, VMManager: m, MetaStore: store,
			Templates: templates, HostNet: hostNet,
		}),
		store:     store,
		templates: templates,
		mgr:       m,
		poolDir:   poolDir,
	}
}

// do issues a request and decodes a spec response, failing on an unexpected status.
func (f specFixture) do(t *testing.T, method, path, body string, wantStatus int) specBody {
	t.Helper()

	rec := serve(f.srv, method, path, body)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s: status = %d, want %d (body: %s)", method, path, rec.Code, wantStatus, rec.Body)
	}
	if wantStatus != http.StatusOK {
		return specBody{raw: rec.Body.String()}
	}

	var got specBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s %s: %v (body: %s)", method, path, err, rec.Body)
	}
	got.raw = rec.Body.String()
	return got
}

type specBody struct {
	PCIDevices []metadata.PCIDevice `json:"pci_devices"`
	USBDevices []metadata.USBDevice `json:"usb_devices"`
	CPU        specCPU              `json:"cpu"`
	Network    specNetwork          `json:"network"`
	Disks      []specDisk           `json:"disks"`
	Restart    bool                 `json:"restart_required"`
	Warnings   []string             `json:"warnings"`
	raw        string
}

type specNetwork struct {
	Mode   string `json:"mode"`
	Bridge string `json:"bridge"`
	VLANID int    `json:"vlan_id"`
}

type specDisk struct {
	Target string `json:"target"`
	Path   string `json:"path"`
	SizeGB int    `json:"size_gb"`
	Boot   bool   `json:"boot"`
}

type specCPU struct {
	Mode     string                `json:"mode"`
	Topology *metadata.CPUTopology `json:"topology"`
	CPUSet   string                `json:"cpu_set"`
}

// TestHandleVMSpec_RestartRequiredTracksTheLiveDomain checks that a spec
// claiming a GPU the domain lacks raises restart_required.
func TestHandleVMSpec_RestartRequiredTracksTheLiveDomain(t *testing.T) {
	f := newSpecFixture(t, "spec-restart-probe")

	if got := f.do(t, http.MethodGet, "/vms/spec-restart-probe/spec", "", http.StatusOK); got.Restart {
		t.Errorf("restart_required = true for a domain that matches its spec (body: %s)", got.raw)
	}

	// Written straight to the store: the test driver has no PCI devices, so
	// PATCH would refuse this one.
	if err := f.store.Update("spec-restart-probe", func(e *metadata.VMEntry) {
		e.PCIDevices = []metadata.PCIDevice{{Address: "0000:01:00.0", Vendor: "NVIDIA"}}
	}); err != nil {
		t.Fatalf("update spec: %v", err)
	}

	got := f.do(t, http.MethodGet, "/vms/spec-restart-probe/spec", "", http.StatusOK)
	if !got.Restart {
		t.Errorf("restart_required = false while the spec claims a device the domain lacks (body: %s)", got.raw)
	}
	if len(got.PCIDevices) != 1 || got.PCIDevices[0].Address != "0000:01:00.0" {
		t.Errorf("pci_devices = %+v, want the spec's device echoed back", got.PCIDevices)
	}
}

func TestHandleVMSpec_EmptyListsAreArrays(t *testing.T) {
	f := newSpecFixture(t, "spec-arrays-probe")

	got := f.do(t, http.MethodGet, "/vms/spec-arrays-probe/spec", "", http.StatusOK)
	for _, field := range []string{`"pci_devices":[]`, `"usb_devices":[]`, `"warnings":[]`} {
		if !strings.Contains(got.raw, field) {
			t.Errorf("body is missing %s: %s", field, got.raw)
		}
	}
}

func TestHandleVMSpecPatch_DetachRedefinesTheDomain(t *testing.T) {
	const name = "spec-detach-probe"
	f := newSpecFixture(t, name)

	if err := f.store.Update(name, func(e *metadata.VMEntry) {
		e.PCIDevices = []metadata.PCIDevice{{Address: "0000:01:00.0"}}
		e.USBDevices = []metadata.USBDevice{{VendorID: "0x1050", ProductID: "0x0407"}}
	}); err != nil {
		t.Fatalf("seed devices: %v", err)
	}

	got := f.do(t, http.MethodPatch, "/vms/"+name+"/spec", `{"pci_devices":[],"usb_devices":[]}`, http.StatusOK)
	if len(got.PCIDevices) != 0 || len(got.USBDevices) != 0 {
		t.Errorf("response still lists devices: %+v / %+v", got.PCIDevices, got.USBDevices)
	}
	if got.Restart {
		t.Errorf("restart_required = true after detaching from a domain that never had the devices (body: %s)", got.raw)
	}

	entry, err := f.store.Get(name)
	if err != nil {
		t.Fatalf("read back spec: %v", err)
	}
	if len(entry.PCIDevices) != 0 || len(entry.USBDevices) != 0 {
		t.Errorf("metadata.json still records devices: %+v / %+v", entry.PCIDevices, entry.USBDevices)
	}

	hw, err := f.mgr.ActiveHardware(name)
	if err != nil {
		t.Fatalf("read live hardware: %v", err)
	}
	if len(hw.HostDevs) != 0 {
		t.Errorf("domain still has hostdevs %v after a detach", hw.HostDevs)
	}
}

func TestHandleVMSpecPatch_RefusesWithAReason(t *testing.T) {
	const name = "spec-refuse-probe"
	f := newSpecFixture(t, name)

	// USB, not PCI, so the result does not depend on the host's IOMMU. The
	// test driver reports no USB devices, as for an unplugged one.
	got := f.do(t, http.MethodPatch, "/vms/"+name+"/spec",
		`{"usb_devices":["0xdead:0xbeef"]}`, http.StatusUnprocessableEntity)
	if !strings.Contains(got.raw, "0xdead:0xbeef") {
		t.Errorf("refusal does not name the device: %s", got.raw)
	}

	entry, err := f.store.Get(name)
	if err != nil {
		t.Fatalf("read back spec: %v", err)
	}
	if len(entry.USBDevices) != 0 {
		t.Errorf("a refused patch still wrote %+v to the spec", entry.USBDevices)
	}
}

// TestHandleVMSpecPatch_IgnoresPowerState checks that a spec patch never
// changes Running, which only start and stop own.
func TestHandleVMSpecPatch_IgnoresPowerState(t *testing.T) {
	const name = "spec-power-probe"
	f := newSpecFixture(t, name)

	if err := f.store.Update(name, func(e *metadata.VMEntry) { e.SetRunning(true) }); err != nil {
		t.Fatalf("seed running: %v", err)
	}

	f.do(t, http.MethodPatch, "/vms/"+name+"/spec", `{"running":false,"vcpus":64,"usb_devices":[]}`, http.StatusOK)

	entry, err := f.store.Get(name)
	if err != nil {
		t.Fatalf("read back spec: %v", err)
	}
	if entry.Running == nil || !*entry.Running {
		t.Errorf("running = %v, want it left true — PATCH /spec must not change power state", entry.Running)
	}
	if entry.VCPUs != 1 {
		t.Errorf("vcpus = %d, want 1 — PATCH /spec accepts device lists and the CPU shape, nothing else", entry.VCPUs)
	}
}

// TestHandleVMSpec_ReportsTheEffectiveCPUMode checks that a VM with no
// cpu_mode recorded reports host-model.
func TestHandleVMSpec_ReportsTheEffectiveCPUMode(t *testing.T) {
	f := newSpecFixture(t, "spec-cpu-default-probe")

	got := f.do(t, http.MethodGet, "/vms/spec-cpu-default-probe/spec", "", http.StatusOK)
	if got.CPU.Mode != metadata.CPUHostModel {
		t.Errorf("cpu.mode = %q, want %q for an entry with no mode recorded", got.CPU.Mode, metadata.CPUHostModel)
	}
	if got.CPU.Topology != nil || got.CPU.CPUSet != "" {
		t.Errorf("cpu = %+v, want an underived, unpinned default", got.CPU)
	}
}

func TestHandleVMSpecPatch_CPUShapeIsRecordedAndApplied(t *testing.T) {
	const name = "spec-cpu-patch-probe"
	f := newSpecFixture(t, name)

	// The fixture VM has one vCPU, so 1×1×1 is the only topology that defines.
	got := f.do(t, http.MethodPatch, "/vms/"+name+"/spec",
		`{"cpu":{"mode":"host-passthrough","topology":{"sockets":1,"cores":1,"threads":1},"cpu_set":"0-3"}}`,
		http.StatusOK)
	if got.CPU.Mode != metadata.CPUHostPassthrough || got.CPU.CPUSet != "0-3" || got.CPU.Topology == nil {
		t.Fatalf("cpu = %+v, want the patched shape echoed back", got.CPU)
	}

	entry, err := f.store.Get(name)
	if err != nil {
		t.Fatalf("read back spec: %v", err)
	}
	if entry.CPUMode != metadata.CPUHostPassthrough || entry.CPUSet != "0-3" || entry.CPUTopology == nil {
		t.Errorf("metadata.json holds %+v, want the patched shape", specCPU{entry.CPUMode, entry.CPUTopology, entry.CPUSet})
	}

	hw, err := f.mgr.ActiveHardware(name)
	if err != nil {
		t.Fatalf("read live hardware: %v", err)
	}
	if want := (vm.CPUShape{Passthrough: true, Sockets: 1, Cores: 1, Threads: 1, CPUSet: "0-3"}); hw.CPU != want {
		t.Errorf("the defined domain has %+v, want %+v — the spec was written but not applied", hw.CPU, want)
	}
}

// TestHandleVMSpecPatch_RefusesAnImpossibleCPU checks the status of each
// refusal: 400 for a topology that cannot multiply out, 422 for a cpuset this
// host is too small for.
func TestHandleVMSpecPatch_RefusesAnImpossibleCPU(t *testing.T) {
	const name = "spec-cpu-refuse-probe"

	tests := []struct {
		name, body string
		wantStatus int
		wantIn     string
	}{
		{
			name:       "a topology that does not multiply out to the vcpu count",
			body:       `{"cpu":{"topology":{"sockets":2,"cores":2,"threads":1}}}`,
			wantStatus: http.StatusBadRequest,
			wantIn:     "4 vCPUs",
		},
		{
			name:       "an invented mode",
			body:       `{"cpu":{"mode":"host-ish"}}`,
			wantStatus: http.StatusBadRequest,
			wantIn:     "host-ish",
		},
		{
			// The test driver reports a 16-CPU host.
			name:       "a cpuset naming CPUs this host does not have",
			body:       `{"cpu":{"cpu_set":"0-31"}}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantIn:     "16 CPUs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSpecFixture(t, name)

			got := f.do(t, http.MethodPatch, "/vms/"+name+"/spec", tt.body, tt.wantStatus)
			if !strings.Contains(got.raw, tt.wantIn) {
				t.Errorf("refusal does not say %q: %s", tt.wantIn, got.raw)
			}

			entry, err := f.store.Get(name)
			if err != nil {
				t.Fatalf("read back spec: %v", err)
			}
			if entry.CPUMode != "" || entry.CPUTopology != nil || entry.CPUSet != "" {
				t.Errorf("a refused patch still wrote %+v to the spec", specCPU{entry.CPUMode, entry.CPUTopology, entry.CPUSet})
			}
		})
	}
}

// TestHandleVMSpecPatch_LeavesAnUnmentionedCPUAlone checks that a device-only
// patch does not write the effective CPU mode over the stored blank.
func TestHandleVMSpecPatch_LeavesAnUnmentionedCPUAlone(t *testing.T) {
	const name = "spec-cpu-untouched-probe"
	f := newSpecFixture(t, name)

	f.do(t, http.MethodPatch, "/vms/"+name+"/spec", `{"usb_devices":[]}`, http.StatusOK)

	entry, err := f.store.Get(name)
	if err != nil {
		t.Fatalf("read back spec: %v", err)
	}
	if entry.CPUMode != "" {
		t.Errorf("cpu_mode = %q, want it left unwritten by a patch that never mentioned the CPU", entry.CPUMode)
	}
}

func TestHandleVMSpec_RestartRequiredCoversTheCPU(t *testing.T) {
	const name = "spec-cpu-restart-probe"
	f := newSpecFixture(t, name)

	if got := f.do(t, http.MethodGet, "/vms/"+name+"/spec", "", http.StatusOK); got.Restart {
		t.Errorf("restart_required = true for a domain that matches its spec (body: %s)", got.raw)
	}

	// Written straight to the store, since PATCH would redefine the domain.
	if err := f.store.Update(name, func(e *metadata.VMEntry) {
		e.CPUTopology = &metadata.CPUTopology{Sockets: 1, Cores: 1, Threads: 1}
	}); err != nil {
		t.Fatalf("update spec: %v", err)
	}

	if got := f.do(t, http.MethodGet, "/vms/"+name+"/spec", "", http.StatusOK); !got.Restart {
		t.Errorf("restart_required = false while the spec claims a topology the domain lacks (body: %s)", got.raw)
	}
}

// TestHandleVMSpec_UnknownVMIsNotFound covers a VM defined by hand with virsh,
// which has no spec.
func TestHandleVMSpec_UnknownVMIsNotFound(t *testing.T) {
	f := newSpecFixture(t, "spec-404-probe")

	for _, tc := range []struct {
		method, body string
	}{
		{http.MethodGet, ""},
		{http.MethodPatch, `{"pci_devices":[]}`},
	} {
		f.do(t, tc.method, "/vms/no-such-vm/spec", tc.body, http.StatusNotFound)
	}
}

// TestHandleVMSpecPatch_NetworkIsRecordedAndApplied walks a VLAN from the patch
// body to the defined domain. The client's bridge is replaced by the one
// Ensure created, and with no network manager the host checks are skipped.
func TestHandleVMSpecPatch_NetworkIsRecordedAndApplied(t *testing.T) {
	const name = "spec-network-patch-probe"
	prov := &fakeProvisioner{uplink: "eth0"}
	f := newSpecFixture(t, name, prov)

	got := f.do(t, http.MethodPatch, "/vms/"+name+"/spec",
		`{"network":{"mode":"bridge","bridge":"br-lan","vlan_id":20}}`, http.StatusOK)
	if want := (specNetwork{Mode: "bridge", Bridge: "nc-vlan20", VLANID: 20}); got.Network != want {
		t.Fatalf("network = %+v, want %+v", got.Network, want)
	}

	entry, err := f.store.Get(name)
	if err != nil {
		t.Fatalf("read back spec: %v", err)
	}
	if entry.NetworkMode != metadata.NetworkBridge || entry.Bridge != "nc-vlan20" || entry.VLANID != 20 {
		t.Errorf("metadata.json holds mode=%q bridge=%q vlan=%d, want the patched network",
			entry.NetworkMode, entry.Bridge, entry.VLANID)
	}
	if entry.VLANUplink != "eth0" {
		t.Errorf("VLANUplink = %q, want eth0", entry.VLANUplink)
	}

	hw, err := f.mgr.ActiveHardware(name)
	if err != nil {
		t.Fatalf("read live hardware: %v", err)
	}
	// VLANTag is 0, not 20: nc-vlan20 already tags at the host, so a
	// guest-side tag would double-tag.
	if want := (vm.NetworkShape{Mode: metadata.NetworkBridge, Bridge: "nc-vlan20"}); hw.Network != want {
		t.Errorf("the defined domain has %+v, want %+v — the spec was written but not applied", hw.Network, want)
	}
}

func TestHandleVMSpecPatch_LeavesAnUnmentionedNetworkAlone(t *testing.T) {
	const name = "spec-network-untouched-probe"
	f := newSpecFixture(t, name)

	f.do(t, http.MethodPatch, "/vms/"+name+"/spec", `{"cpu":{"mode":"host-passthrough"}}`, http.StatusOK)

	entry, err := f.store.Get(name)
	if err != nil {
		t.Fatalf("read back spec: %v", err)
	}
	if entry.NetworkMode != "nat" || entry.Bridge != "" || entry.VLANID != 0 {
		t.Errorf("a CPU-only patch rewrote the network to mode=%q bridge=%q vlan=%d",
			entry.NetworkMode, entry.Bridge, entry.VLANID)
	}
}
