package vm

import (
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

// ErrInvalidPCIAddress and ErrInvalidUSBID mean a spec names a device in a form
// no host could match, as opposed to a device this host does not have.
var (
	ErrInvalidPCIAddress = errors.New("vm: invalid PCI address")
	ErrInvalidUSBID      = errors.New("vm: invalid USB id")
)

// The domain XML is built from typed structs rather than a text template, so a
// stray brace or quote cannot yield a domain that defines but will not boot. The
// element types are unexported to keep DomainSpecFromEntry the only mapping.

// DomainSpec is a complete machine description, marshalled for DomainDefineXML.
type DomainSpec struct {
	XMLName xml.Name `xml:"domain"`
	// Type is the libvirt domain type ("kvm", or "qemu" without acceleration).
	Type string `xml:"type,attr"`
	Name string `xml:"name"`
	// UUID is empty for a new domain, where libvirt generates one. Redefining an
	// existing domain without it fails with "domain 'x' already exists with uuid".
	UUID       string   `xml:"uuid,omitempty"`
	Memory     memory   `xml:"memory"`
	VCPU       vcpuSpec `xml:"vcpu"`
	OS         osSpec   `xml:"os"`
	Features   features `xml:"features"`
	CPU        cpuSpec  `xml:"cpu"`
	Clock      clock    `xml:"clock"`
	OnPoweroff string   `xml:"on_poweroff"`
	OnReboot   string   `xml:"on_reboot"`
	OnCrash    string   `xml:"on_crash"`
	Devices    devices  `xml:"devices"`
}

type memory struct {
	Unit  string `xml:"unit,attr"`
	Value int    `xml:",chardata"`
}

// vcpuSpec carries the vCPU count and an optional cpuset. Pinning is one cpuset
// for the whole domain, so it lives here rather than in <cputune>.
type vcpuSpec struct {
	Placement string `xml:"placement,attr,omitempty"`
	CPUSet    string `xml:"cpuset,attr,omitempty"`
	Value     int    `xml:",chardata"`
}

// Child order follows libvirt's RNG schema so that virt-xml-validate passes.
type osSpec struct {
	// Firmware turns on libvirt's firmware autoselection ("efi"); empty means
	// BIOS. Autoselection picks a build matching Loader, so no OVMF path is pinned.
	Firmware string  `xml:"firmware,attr,omitempty"`
	Type     osType  `xml:"type"`
	Loader   *loader `xml:"loader"`
	NVRAM    *nvram  `xml:"nvram"`
	Boot     boot    `xml:"boot"`
}

// loader constrains what autoselection may pick. It names no path because
// firmware filenames differ per distro and release.
type loader struct {
	Secure   string `xml:"secure,attr,omitempty"`
	ReadOnly string `xml:"readonly,attr,omitempty"`
	Type     string `xml:"type,attr,omitempty"`
}

// nvram is presence-only: libvirt derives the per-domain variable store path
// from the loader it selected.
type nvram struct{}

type osType struct {
	Arch    string `xml:"arch,attr"`
	Machine string `xml:"machine,attr"`
	Value   string `xml:",chardata"`
}

type boot struct {
	Dev string `xml:"dev,attr"`
}

// features holds presence-only elements, so nil omits one. Without ACPI the
// guest never sees the request DomainShutdown sends.
type features struct {
	ACPI *struct{} `xml:"acpi"`
	APIC *struct{} `xml:"apic"`
	// SMM is required for <loader secure='yes'>: secure boot keeps its variable
	// store in SMRAM.
	SMM *smm `xml:"smm"`
}

type smm struct {
	State string `xml:"state,attr"`
}

type cpuSpec struct {
	Mode string `xml:"mode,attr"`
	// Topology is nil unless the spec fixes one; QEMU otherwise presents a flat
	// list of sockets.
	Topology *cpuTopology `xml:"topology"`
}

type cpuTopology struct {
	Sockets int `xml:"sockets,attr"`
	Cores   int `xml:"cores,attr"`
	Threads int `xml:"threads,attr"`
}

type clock struct {
	Offset string `xml:"offset,attr"`
}

type devices struct {
	Emulator   string      `xml:"emulator"`
	Disks      []disk      `xml:"disk"`
	Interfaces []iface     `xml:"interface"`
	HostDevs   []hostdev   `xml:"hostdev"`
	Graphics   graphics    `xml:"graphics"`
	Video      video       `xml:"video"`
	Serial     serialPort  `xml:"serial"`
	Console    consolePort `xml:"console"`
	Channels   []channel   `xml:"channel"`
	TPM        *tpm        `xml:"tpm"`
}

// channel is a virtio-serial port, rendered only for the QEMU guest agent; with
// no source path libvirt derives the socket. The agent lets PrimaryIP confirm a
// bridged or VLAN guest's address where ARP and the NAT lease file cannot.
type channel struct {
	Type   string     `xml:"type,attr"`
	Target channelTgt `xml:"target"`
}

type channelTgt struct {
	Type string `xml:"type,attr"`
	Name string `xml:"name,attr"`
}

// guestAgentChannel is the port name qemu-guest-agent opens by default.
const guestAgentChannel = "org.qemu.guest_agent.0"

// hostdev is one host device handed whole to the guest. With managed='yes'
// libvirt binds it to vfio-pci for the life of the domain and returns it to
// its host driver afterwards.
type hostdev struct {
	Mode    string     `xml:"mode,attr"`
	Type    string     `xml:"type,attr"`
	Managed string     `xml:"managed,attr"`
	Source  hostdevSrc `xml:"source"`
}

// hostdevSrc identifies a PCI device by address and a USB device by
// vendor:product. A live USB hostdev carries an address too, so readers must
// go by the hostdev's type.
type hostdevSrc struct {
	Address *pciAddress `xml:"address"`
	Vendor  *usbID      `xml:"vendor"`
	Product *usbID      `xml:"product"`
}

type pciAddress struct {
	Domain   string `xml:"domain,attr"`
	Bus      string `xml:"bus,attr"`
	Slot     string `xml:"slot,attr"`
	Function string `xml:"function,attr"`
}

type usbID struct {
	ID string `xml:"id,attr"`
}

// tpm is an emulated TPM 2.0, backed by swtpm on the host.
type tpm struct {
	Model   string     `xml:"model,attr"`
	Backend tpmBackend `xml:"backend"`
}

type tpmBackend struct {
	Type    string `xml:"type,attr"`
	Version string `xml:"version,attr"`
}

type disk struct {
	Type     string    `xml:"type,attr"`
	Device   string    `xml:"device,attr"`
	Driver   diskDrv   `xml:"driver"`
	Source   diskSrc   `xml:"source"`
	Target   diskTgt   `xml:"target"`
	ReadOnly *struct{} `xml:"readonly"`
}

type diskDrv struct {
	Name string `xml:"name,attr"`
	Type string `xml:"type,attr"`
}

type diskSrc struct {
	File string `xml:"file,attr"`
}

type diskTgt struct {
	Dev string `xml:"dev,attr"`
	Bus string `xml:"bus,attr"`
}

// iface is a guest NIC. A guest always gets exactly one, since cloud images
// only bring up their first interface.
type iface struct {
	Type string `xml:"type,attr"`
	// MAC is nil for a new domain, so libvirt generates one. A rebuild must keep
	// it, or the guest gets a new DHCP lease and comes back at a different IP.
	MAC    *ifaceMAC  `xml:"mac"`
	Source ifaceSrc   `xml:"source"`
	VLAN   *ifaceVLAN `xml:"vlan"`
	Model  ifaceModel `xml:"model"`
}

// ifaceVLAN is 802.1Q tagging on the port libvirt creates for this NIC. A
// pointer, so an untagged VM's document is unchanged.
type ifaceVLAN struct {
	Tag ifaceVLANTag `xml:"tag"`
}

type ifaceVLANTag struct {
	ID int `xml:"id,attr"`
}

type ifaceMAC struct {
	Address string `xml:"address,attr"`
}

// ifaceSrc sets network or bridge, never both: libvirt rejects that.
type ifaceSrc struct {
	Network string `xml:"network,attr,omitempty"`
	Bridge  string `xml:"bridge,attr,omitempty"`
}

type ifaceModel struct {
	Type string `xml:"type,attr"`
}

type graphics struct {
	Type     string       `xml:"type,attr"`
	Port     int          `xml:"port,attr"`
	AutoPort string       `xml:"autoport,attr"`
	Listen   string       `xml:"listen,attr"`
	ListenEl graphicsLstn `xml:"listen"`
}

type graphicsLstn struct {
	Type    string `xml:"type,attr"`
	Address string `xml:"address,attr"`
}

type video struct {
	Model videoModel `xml:"model"`
}

type videoModel struct {
	Type string `xml:"type,attr"`
	VRAM int    `xml:"vram,attr"`
}

type serialPort struct {
	Type   string    `xml:"type,attr"`
	Target serialTgt `xml:"target"`
}

type serialTgt struct {
	Port int `xml:"port,attr"`
}

type consolePort struct {
	Type   string     `xml:"type,attr"`
	Target consoleTgt `xml:"target"`
}

type consoleTgt struct {
	Type string `xml:"type,attr"`
	Port int    `xml:"port,attr"`
}

const (
	// bootDiskTarget is derived here; metadata.VMEntry.Disks excludes it.
	bootDiskTarget = "vda"
	// seedISOTarget is on the SATA bus because q35 has no IDE.
	seedISOTarget = "sda"
	// vncListenAddr keeps VNC off the network; the console goes through the
	// daemon's authenticated WebSocket proxy.
	vncListenAddr = "127.0.0.1"
	// emulatedVRAMKiB is QEMU's framebuffer size, not host VRAM.
	emulatedVRAMKiB = 16384
)

// DomainSpecFromEntry is the one mapping from a VM's spec to a machine
// description. Host capabilities (Type, Arch, Machine, Emulator) are left empty
// for Manager.applyCaps. UUID and MAC are applied only when the entry records
// them, so a fresh create or a clone gets a generated pair.
func DomainSpecFromEntry(e metadata.VMEntry, diskPath, isoPath string) DomainSpec {
	present := &struct{}{}

	// A VLAN tag is only rendered on a bridge; on NAT it would be invisible.
	// It is also left out when nano-cloud provisioned the VLAN (VLANUplink
	// set): the guest then attaches untagged to nc-vlan<id>, and a tag here
	// would double-tag. An entry with a VLAN but no uplink predates that.
	src, vlan := ifaceSrc{Network: "default"}, (*ifaceVLAN)(nil)
	if e.NetworkMode == metadata.NetworkBridge {
		src = ifaceSrc{Bridge: e.BridgeOf()}
		if e.VLANID > 0 && e.VLANUplink == "" {
			vlan = &ifaceVLAN{Tag: ifaceVLANTag{ID: e.VLANID}}
		}
	}

	s := DomainSpec{
		Name:       e.ID,
		Memory:     memory{Unit: "MiB", Value: e.MemoryMB},
		VCPU:       vcpuSpec{Value: e.VCPUs},
		OS:         osSpec{Type: osType{Value: "hvm"}, Boot: boot{Dev: "hd"}},
		Features:   features{ACPI: present, APIC: present},
		CPU:        cpuSpec{Mode: metadata.CPUHostModel},
		Clock:      clock{Offset: "utc"},
		OnPoweroff: "destroy",
		// restart, so a guest that reboots itself comes back on its own.
		OnReboot: "restart",
		OnCrash:  "destroy",
		Devices: devices{
			Disks: []disk{
				{
					Type: "file", Device: "disk",
					Driver: diskDrv{Name: "qemu", Type: "qcow2"},
					Source: diskSrc{File: diskPath},
					Target: diskTgt{Dev: bootDiskTarget, Bus: "virtio"},
				},
				{
					Type: "file", Device: "cdrom",
					Driver:   diskDrv{Name: "qemu", Type: "raw"},
					Source:   diskSrc{File: isoPath},
					Target:   diskTgt{Dev: seedISOTarget, Bus: "sata"},
					ReadOnly: present,
				},
			},
			Interfaces: []iface{{Type: ifaceTypeFor(e.NetworkMode), Source: src, VLAN: vlan, Model: ifaceModel{Type: "virtio"}}},
			Graphics: graphics{
				Type: "vnc", Port: -1, AutoPort: "yes", Listen: vncListenAddr,
				ListenEl: graphicsLstn{Type: "address", Address: vncListenAddr},
			},
			Video:    video{Model: videoModel{Type: "vga", VRAM: emulatedVRAMKiB}},
			Serial:   serialPort{Type: "pty"},
			Console:  consolePort{Type: "pty", Target: consoleTgt{Type: "serial"}},
			Channels: []channel{{Type: "unix", Target: channelTgt{Type: "virtio", Name: guestAgentChannel}}},
		},
	}

	applyFirmware(&s, e)
	applyCPU(&s, e)
	// Extra disks go after the seed cdrom so the first two keep fixed positions.
	s.Devices.Disks = append(s.Devices.Disks, extraDisks(e)...)
	s.Devices.HostDevs = hostDevsFor(e)
	if e.TPM {
		// tpm-crb is the TPM 2.0 interface.
		s.Devices.TPM = &tpm{Model: "tpm-crb", Backend: tpmBackend{Type: "emulator", Version: "2.0"}}
	}
	// Last, so the interface exists for the MAC.
	applyIdentity(&s, DomainIdentity{UUID: e.UUID, MAC: e.MAC})
	return s
}

// applyFirmware maps the boot-firmware part of the spec. BIOS stays the default
// because Alpine ships no signed shim.
func applyFirmware(s *DomainSpec, e metadata.VMEntry) {
	if e.Firmware != metadata.FirmwareUEFI {
		return
	}
	s.OS.Firmware = "efi"
	s.OS.NVRAM = &nvram{}
	s.OS.Loader = &loader{ReadOnly: "yes", Type: "pflash"}

	// Only honoured with UEFI, so secure boot on BIOS yields a plain BIOS domain.
	if e.SecureBoot {
		s.OS.Loader.Secure = "yes"
		s.Features.SMM = &smm{State: "on"}
	}
}

// applyCPU maps the CPU part of the spec. host-model stays the default: it
// exposes nearly everything the host can do while keeping the domain
// migratable. An unknown mode falls back to it; ValidateCPU refuses one first.
func applyCPU(s *DomainSpec, e metadata.VMEntry) {
	if e.CPUMode == metadata.CPUHostPassthrough {
		s.CPU.Mode = metadata.CPUHostPassthrough
	}
	if t := e.CPUTopology; t != nil {
		s.CPU.Topology = &cpuTopology{Sockets: t.Sockets, Cores: t.Cores, Threads: t.Threads}
	}
	if set := strings.TrimSpace(e.CPUSet); set != "" {
		s.VCPU.Placement = "static"
		s.VCPU.CPUSet = set
	}
}

// hostDevsFor maps the passthrough part of the spec. An unparsable address is
// skipped; ValidateHostDevices refuses such an entry before rendering.
func hostDevsFor(e metadata.VMEntry) []hostdev {
	if len(e.PCIDevices) == 0 && len(e.USBDevices) == 0 {
		return nil
	}

	out := make([]hostdev, 0, len(e.PCIDevices)+len(e.USBDevices))
	for _, d := range e.PCIDevices {
		addr, err := parsePCIAddress(d.Address)
		if err != nil {
			continue
		}
		out = append(out, hostdev{
			Mode: "subsystem", Type: "pci", Managed: "yes",
			Source: hostdevSrc{Address: &addr},
		})
	}
	for _, d := range e.USBDevices {
		out = append(out, hostdev{
			Mode: "subsystem", Type: "usb", Managed: "yes",
			Source: hostdevSrc{
				Vendor:  &usbID{ID: d.VendorID},
				Product: &usbID{ID: d.ProductID},
			},
		})
	}
	return out
}

// pciAddressPattern is the domain:bus:slot.function form sysfs and libvirt
// use. Anchored, so an address with trailing junk is rejected.
var pciAddressPattern = regexp.MustCompile(`^([0-9a-fA-F]{4}):([0-9a-fA-F]{2}):([0-9a-fA-F]{2})\.([0-7])$`)

// parsePCIAddress splits "0000:01:00.0" into libvirt's 0x-prefixed parts. It
// does not normalise: the value must reach libvirt as the inventory reported it.
func parsePCIAddress(addr string) (pciAddress, error) {
	m := pciAddressPattern.FindStringSubmatch(strings.TrimSpace(addr))
	if m == nil {
		return pciAddress{}, fmt.Errorf("%w: %q is not a domain:bus:slot.function address like 0000:01:00.0", ErrInvalidPCIAddress, addr)
	}
	return pciAddress{
		Domain:   "0x" + m[1],
		Bus:      "0x" + m[2],
		Slot:     "0x" + m[3],
		Function: "0x" + m[4],
	}, nil
}

// ValidateHostDevices refuses a spec whose passthrough devices cannot be
// rendered. Write paths already check the host inventory; this catches a
// hand-edited metadata.json, which would otherwise define without the device.
func ValidateHostDevices(e metadata.VMEntry) error {
	for _, d := range e.PCIDevices {
		if _, err := parsePCIAddress(d.Address); err != nil {
			return fmt.Errorf("vm: %q: %w", e.ID, err)
		}
	}
	for _, d := range e.USBDevices {
		if !usbIDPattern.MatchString(d.VendorID) || !usbIDPattern.MatchString(d.ProductID) {
			return fmt.Errorf("vm: %q: %w: %s:%s is not a 0x-prefixed vendor:product pair",
				e.ID, ErrInvalidUSBID, d.VendorID, d.ProductID)
		}
	}
	return nil
}

// usbIDPattern matches libvirt's 0xNNNN ids. USB devices are matched on
// vendor:product, never on bus:device, which change on every replug.
var usbIDPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{4}$`)

// ifaceTypeFor maps a network mode to libvirt's interface type. An unknown mode
// gets NAT rather than no network.
func ifaceTypeFor(mode string) string {
	if mode == metadata.NetworkBridge {
		return "bridge"
	}
	return "network"
}

// DomainIdentity is what libvirt rather than the spec decides. It is recorded
// on the entry so a vanished domain can be rebuilt as the same machine.
type DomainIdentity struct {
	UUID string
	MAC  string
}

// ParseDomainIdentity reads a domain's identity from libvirt's XML. A missing
// field comes back empty: a domain with no NIC has no MAC.
func ParseDomainIdentity(xmlStr string) (DomainIdentity, error) {
	var doc struct {
		UUID    string `xml:"uuid"`
		Devices struct {
			Interfaces []struct {
				MAC struct {
					Address string `xml:"address,attr"`
				} `xml:"mac"`
			} `xml:"interface"`
		} `xml:"devices"`
	}
	if err := xml.Unmarshal([]byte(xmlStr), &doc); err != nil {
		return DomainIdentity{}, fmt.Errorf("vm: parse domain identity: %w", err)
	}

	id := DomainIdentity{UUID: strings.TrimSpace(doc.UUID)}
	if len(doc.Devices.Interfaces) > 0 {
		id.MAC = strings.TrimSpace(doc.Devices.Interfaces[0].MAC.Address)
	}
	return id, nil
}

// applyIdentity pins a rebuilt domain to the identity it already had.
func applyIdentity(s *DomainSpec, id DomainIdentity) {
	if id.UUID != "" {
		s.UUID = id.UUID
	}
	if id.MAC != "" && len(s.Devices.Interfaces) > 0 {
		s.Devices.Interfaces[0].MAC = &ifaceMAC{Address: id.MAC}
	}
}

// HostDevRef is an assigned device as a PCI address or a USB "vendor:product"
// pair, so the live domain and the spec can be compared.
type HostDevRef string

// HostDevRefs reduces a spec's passthrough devices to comparable identities.
func HostDevRefs(e metadata.VMEntry) []HostDevRef {
	refs := make([]HostDevRef, 0, len(e.PCIDevices)+len(e.USBDevices))
	for _, d := range e.PCIDevices {
		refs = append(refs, HostDevRef(strings.ToLower(strings.TrimSpace(d.Address))))
	}
	for _, d := range e.USBDevices {
		refs = append(refs, HostDevRef(strings.ToLower(d.VendorID+":"+d.ProductID)))
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i] < refs[j] })
	return refs
}

// ParseDomainHostDevs reads the devices assigned to a running domain from its
// live XML, which lags the persistent config until a restart. Addresses are
// normalised to HostDevRefs' form, since libvirt re-renders them.
func ParseDomainHostDevs(xmlStr string) ([]HostDevRef, error) {
	var doc struct {
		Devices struct {
			HostDevs []hostdev `xml:"hostdev"`
		} `xml:"devices"`
	}
	if err := xml.Unmarshal([]byte(xmlStr), &doc); err != nil {
		return nil, fmt.Errorf("vm: parse domain hostdevs: %w", err)
	}

	refs := make([]HostDevRef, 0, len(doc.Devices.HostDevs))
	for _, h := range doc.Devices.HostDevs {
		// Dispatch on the declared type, not on which elements are present:
		// a running USB hostdev also carries the address libvirt resolved.
		switch h.Type {
		case "pci":
			if a := h.Source.Address; a != nil {
				refs = append(refs, HostDevRef(strings.ToLower(fmt.Sprintf("%s:%s:%s.%s",
					trimHex(a.Domain, 4), trimHex(a.Bus, 2), trimHex(a.Slot, 2), trimHex(a.Function, 1)))))
			}
		case "usb":
			if h.Source.Vendor != nil && h.Source.Product != nil {
				refs = append(refs, HostDevRef(strings.ToLower(h.Source.Vendor.ID+":"+h.Source.Product.ID)))
			}
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i] < refs[j] })
	return refs, nil
}

// trimHex turns libvirt's "0x01" into the zero-padded "01" of a sysfs address.
// libvirt may echo different padding, so the width is imposed here.
func trimHex(v string, width int) string {
	s := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(v), "0x"), "0X")
	for len(s) < width {
		s = "0" + s
	}
	return s
}

// BuildDomainXML marshals a machine description into libvirt's domain XML.
func BuildDomainXML(s DomainSpec) (string, error) {
	out, err := xml.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", fmt.Errorf("vm: render domain XML for %q: %w", s.Name, err)
	}
	return string(out), nil
}
