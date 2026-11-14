package metadata

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

// CurrentSchemaVersion is the on-disk format this binary writes. Bump it only
// for an incompatible restructure; new VMEntry fields survive older binaries
// because unknown keys are round-tripped.
const CurrentSchemaVersion = 1

// Restart policies decide what the reconcile loop does when a VM that should be
// running is found stopped:
//
//	on-failure — restart after a crash or external destroy, but honour an
//	             in-guest poweroff.
//	always     — restart regardless.
//	never      — never restart automatically.
const (
	RestartOnFailure = "on-failure"
	RestartAlways    = "always"
	RestartNever     = "never"
)

// DefaultRestartPolicy is applied when an entry does not name one.
const DefaultRestartPolicy = RestartOnFailure

// Guest networking. A VM has exactly one NIC, on one of these two modes.
const (
	NetworkNAT    = "nat"
	NetworkBridge = "bridge"
)

// DefaultBridge is the host bridge a bridge-mode VM attaches to when its entry
// names none. Older entries rely on an empty value meaning br0.
const DefaultBridge = "br0"

// MaxVLANID is the largest 802.1Q tag. 4095 is reserved by the standard.
const MaxVLANID = 4094

// Guest boot firmware. An empty Firmware means BIOS, which older entries and
// images with an unsigned bootloader (Alpine) need.
const (
	FirmwareBIOS = "bios"
	FirmwareUEFI = "uefi"
)

// Guest CPU presentation. An empty CPUMode means host-model; host-passthrough
// exposes the exact host CPU, which nested virtualisation needs.
const (
	CPUHostModel       = "host-model"
	CPUHostPassthrough = "host-passthrough"
)

var (
	// ErrNotFound is returned by Get and Update for an unknown VM ID. Update
	// does not create, so a mistyped ID cannot write a phantom entry; use
	// Upsert when creation is intended.
	ErrNotFound = errors.New("metadata: no entry for id")

	// ErrSchemaTooNew is returned when the file was written by a newer binary.
	// Callers must treat it as fatal: the next write would drop every field
	// this binary does not know.
	ErrSchemaTooNew = errors.New("metadata: file schema is newer than this binary understands")
)

// VMEntry is the per-VM spec: what the VM is meant to be, not what libvirt is
// currently doing. It lives outside libvirt XML so it survives libvirt losing
// the domain.
type VMEntry struct {
	// ID is the VM identifier and must match the libvirt domain name.
	ID string `json:"id"`
	// PoolPath is the absolute path of the storage pool holding the VM disk.
	PoolPath string `json:"pool_path"`
	// NetworkMode is NetworkNAT or NetworkBridge.
	NetworkMode string `json:"network_mode"`
	// Bridge is the host bridge a bridge-mode VM attaches to; empty is
	// DefaultBridge. Ignored on NAT.
	Bridge string `json:"bridge,omitempty"`
	// VLANID tags the guest's frames with an 802.1Q VLAN; 0 is untagged.
	// Meaningful only on NetworkBridge.
	VLANID int `json:"vlan_id,omitempty"`
	// VLANUplink is the host interface under a VLAN nano-cloud provisioned;
	// when empty, the domain XML does the tagging. It is recorded, not
	// re-derived, so a moved default route cannot silently move the VM.
	VLANUplink string `json:"vlan_uplink,omitempty"`
	// CloudInitUser is the username injected via cloud-init on first boot.
	CloudInitUser string `json:"cloud_init_user"`
	// Hostname is the guest hostname supplied to cloud-init.
	Hostname string `json:"hostname"`
	// ImageID is the catalog ID of the base image the VM was created from.
	ImageID string `json:"image_id"`
	// OS is the distro tag of the base image, used by the frontend for icons.
	OS string `json:"os"`
	// UUID and MAC are the values libvirt chose at define time, recorded so a
	// domain that is gone can be rebuilt as the same machine at the same
	// address. Empty means not yet read; the reconcile loop backfills them.
	UUID string `json:"uuid,omitempty"`
	MAC  string `json:"mac,omitempty"`

	// StartedAtUnix is when the domain was last started; zero means not
	// tracked as running. It is an observation for uptime, not desired state,
	// and is cleared only on a forced stop.
	StartedAtUnix int64 `json:"started_at_unix,omitempty"`

	// Running is the desired power state. nil means "never determined" and
	// must not read as false, or a legacy fleet would stay off after a reboot;
	// the reconcile loop fills it in from observed libvirt state.
	Running *bool `json:"running,omitempty"`
	// VCPUs is the desired virtual CPU count; zero for legacy entries.
	VCPUs int `json:"vcpus,omitempty"`
	// MemoryMB is the desired RAM in mebibytes; zero for legacy entries.
	MemoryMB int `json:"memory_mb,omitempty"`
	// DiskGB is the boot disk size requested at create time. Record-only: the
	// reconcile loop never acts on it.
	DiskGB int `json:"disk_gb,omitempty"`
	// RestartPolicy is one of the Restart* constants. Empty means
	// DefaultRestartPolicy.
	RestartPolicy string `json:"restart_policy,omitempty"`

	// Firmware is FirmwareUEFI, or empty for BIOS.
	Firmware string `json:"firmware,omitempty"`
	// SecureBoot is only meaningful with FirmwareUEFI; it also requires the
	// q35 machine type and SMM.
	SecureBoot bool `json:"secure_boot,omitempty"`
	// TPM adds an emulated TPM 2.0 device, which needs swtpm on the host.
	TPM bool `json:"tpm,omitempty"`

	// CPUMode is CPUHostPassthrough, or empty for host-model.
	CPUMode string `json:"cpu_mode,omitempty"`
	// CPUTopology fixes the guest's socket/core/thread layout. It is a pointer
	// so that absent ("derive it from VCPUs") differs from zero.
	CPUTopology *CPUTopology `json:"cpu_topology,omitempty"`
	// CPUSet pins every vCPU to a set of host CPUs, in libvirt's cpuset syntax
	// ("4-7", "0,2,4"). Empty means unpinned.
	CPUSet string `json:"cpu_set,omitempty"`

	// PCIDevices and USBDevices are host devices passed through to the guest.
	PCIDevices []PCIDevice `json:"pci_devices,omitempty"`
	USBDevices []USBDevice `json:"usb_devices,omitempty"`

	// Disks holds the disks attached in addition to the boot overlay, whose
	// path derives from PoolPath and ID and so is not repeated here.
	Disks []Disk `json:"disks,omitempty"`

	// CloudInit carries the provisioning extras rendered into the seed ISO at
	// create time, beyond CloudInitUser and Hostname.
	CloudInit *CloudInitSpec `json:"cloud_init,omitempty"`

	// Service names the service this VM was created for, if any. It is a label
	// only; nothing about how the VM runs depends on it.
	Service string `json:"service,omitempty"`

	// unknown holds keys this binary does not recognise, so writing the entry
	// back keeps fields a newer release added.
	unknown map[string]json.RawMessage
}

// CPUTopology is the guest's CPU layout. Sockets×Cores×Threads must equal the
// entry's VCPUs or libvirt refuses to define the domain.
type CPUTopology struct {
	Sockets int `json:"sockets"`
	Cores   int `json:"cores"`
	Threads int `json:"threads"`
}

// PCIDevice is one host PCI function passed through to a guest.
type PCIDevice struct {
	// Address is the host PCI address in domain:bus:slot.function form, e.g.
	// "0000:01:00.0". It is the identity; Vendor and Product are display text.
	Address string `json:"address"`
	Vendor  string `json:"vendor,omitempty"`
	Product string `json:"product,omitempty"`
}

// USBDevice is one host USB device passed through to a guest. It is matched on
// vendor:product because bus and device numbers change on replug.
type USBDevice struct {
	VendorID    string `json:"vendor_id"`
	ProductID   string `json:"product_id"`
	Description string `json:"description,omitempty"`
}

// Disk is one disk attached to a VM beyond its boot overlay.
type Disk struct {
	// Target is the guest-side device name ("vdb") and the disk's identity in
	// the API, so it must stay stable for the life of the VM.
	Target string `json:"target"`
	// Path is the absolute host path of the image file.
	Path string `json:"path"`
	// SizeGB is the virtual size.
	SizeGB int `json:"size_gb"`
	// Format and Bus mean qcow2 and virtio when empty.
	Format string `json:"format,omitempty"`
	Bus    string `json:"bus,omitempty"`
}

// CloudInitSpec is the provisioning payload rendered into the seed ISO. It is
// kept for display; cloud-init runs it on first boot only.
type CloudInitSpec struct {
	Packages   []string    `json:"packages,omitempty"`
	RunCmd     []string    `json:"runcmd,omitempty"`
	WriteFiles []WriteFile `json:"write_files,omitempty"`
	// SSHKeys are the authorized keys as resolved at create time, including
	// any fetched by the daemon from a GitHub username.
	SSHKeys []string `json:"ssh_keys,omitempty"`
}

// WriteFile is one file cloud-init writes into the guest on first boot.
type WriteFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// Permissions is an octal string in the form cloud-init expects, e.g. "0644".
	Permissions string `json:"permissions,omitempty"`
	Owner       string `json:"owner,omitempty"`
}

// vmEntryKeys is the set of JSON keys VMEntry declares, derived from the struct
// tags. A hand-written list could miss a field, which would then be kept in
// unknown as well and resurface whenever omitempty dropped the real value.
var vmEntryKeys = func() map[string]struct{} {
	t := reflect.TypeOf(VMEntry{})
	keys := make(map[string]struct{}, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			keys[name] = struct{}{}
		}
	}
	return keys
}()

// vmEntryJSON has VMEntry's layout but none of its methods, so the custom
// marshaller below can encode the struct's own fields without recursing.
type vmEntryJSON VMEntry

// UnmarshalJSON decodes an entry and retains every key this binary does not
// recognise, so running an older binary does not strip a newer release's
// fields from every VM. Unknown keys in the document envelope are still
// dropped, so non-VM state belongs in its own file.
func (e *VMEntry) UnmarshalJSON(data []byte) error {
	var known vmEntryJSON
	if err := json.Unmarshal(data, &known); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	for name := range vmEntryKeys {
		delete(all, name)
	}

	*e = VMEntry(known)
	if len(all) > 0 {
		e.unknown = all
	}
	return nil
}

// MarshalJSON writes the entry's own fields plus any retained unknown keys.
// It has a value receiver because encoding/json cannot take the address of a
// map element, so a pointer method would be skipped for map[string]VMEntry.
func (e VMEntry) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(vmEntryJSON(e))
	if err != nil {
		return nil, err
	}
	if len(e.unknown) == 0 {
		return data, nil
	}

	var merged map[string]json.RawMessage
	if err := json.Unmarshal(data, &merged); err != nil {
		return nil, err
	}
	for k, v := range e.unknown {
		// A key this binary owns always comes from the struct, even when
		// omitempty left it out.
		if _, ours := vmEntryKeys[k]; !ours {
			merged[k] = v
		}
	}
	return json.Marshal(merged)
}

// SetRunning records the desired power state.
func (e *VMEntry) SetRunning(running bool) {
	e.Running = &running
}

// DesiredRunning reports the desired power state and whether it is known.
// A false ok means the entry has not been adopted yet; callers must not treat
// that as "should be off".
func (e VMEntry) DesiredRunning() (running, ok bool) {
	if e.Running == nil {
		return false, false
	}
	return *e.Running, true
}

// BridgeOf returns the bridge a bridge-mode entry attaches to, resolving the
// empty default. The renderer and the API both use it so they agree on which
// bridge an older entry means.
func (e VMEntry) BridgeOf() string {
	if e.Bridge == "" {
		return DefaultBridge
	}
	return e.Bridge
}

// Policy returns the effective restart policy, resolving the empty default.
func (e VMEntry) Policy() string {
	if e.RestartPolicy == "" {
		return DefaultRestartPolicy
	}
	return e.RestartPolicy
}
