package vm

import (
	"encoding/xml"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	libvirt "github.com/digitalocean/go-libvirt"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

// unreachableURI names a socket that cannot exist, so dialing fails fast.
const unreachableURI = "qemu+unix:///system?socket=/nonexistent/nano-cloud-test-unreachable.sock"

// testURI is libvirt's in-memory fake driver; it needs no KVM.
const testURI = "test:///default"

// newTestManager connects to testURI, skipping the test if libvirt is unavailable.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := New(testURI)
	if err != nil {
		t.Skipf("libvirt not available (%v): skipping", err)
	}
	return m
}

func TestNew_ConnectsAndCloses(t *testing.T) {
	m := newTestManager(t)
	if err := m.Close(); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}
}

func TestList_ReturnsWithoutError(t *testing.T) {
	m := newTestManager(t)
	defer func() {
		if err := m.Close(); err != nil {
			t.Errorf("Close() returned error: %v", err)
		}
	}()

	vms, err := m.List()
	if err != nil {
		t.Fatalf("List() returned error: %v", err)
	}

	if vms == nil {
		t.Error("List() returned nil slice; want non-nil (possibly empty)")
	}

	t.Logf("List() returned %d domain(s)", len(vms))
	for _, v := range vms {
		t.Logf("  domain: id=%d name=%q state=%q vcpus=%d memory_kb=%d",
			v.ID, v.Name, v.State, v.VCPUs, v.MemoryKB)
	}
}

func TestList_VMFieldsPopulated(t *testing.T) {
	m := newTestManager(t)
	defer m.Close() //nolint:errcheck // best-effort cleanup in test

	vms, err := m.List()
	if err != nil {
		t.Fatalf("List() returned error: %v", err)
	}

	for _, v := range vms {
		if v.Name == "" {
			t.Errorf("domain id=%d has empty Name", v.ID)
		}
		validStates := map[string]bool{
			"running": true,
			"paused":  true,
			"stopped": true,
			"unknown": true,
		}
		if !validStates[v.State] {
			t.Errorf("domain %q has unexpected State %q", v.Name, v.State)
		}
	}
}

func TestClose_SafeToCall(t *testing.T) {
	m := newTestManager(t)
	if err := m.Close(); err != nil {
		t.Fatalf("first Close() returned error: %v", err)
	}
	// A second Close may return an error but must not panic.
	_ = m.Close()
}

func TestDomainStateString(t *testing.T) {
	tests := []struct {
		name  string
		input int32
		want  string
	}{
		{"running", 1, "running"},
		{"blocked", 2, "running"},
		{"paused", 3, "paused"},
		{"shutdown", 4, "stopped"},
		{"shutoff", 5, "stopped"},
		{"crashed", 6, "stopped"},
		{"pmsuspended", 7, "paused"},
		{"nostate", 0, "unknown"},
		{"future", 99, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domainStateString(libvirt.DomainState(tt.input))
			if got != tt.want {
				t.Errorf("domainStateString(%d) = %q; want %q", tt.input, got, tt.want)
			}
		})
	}
}

// baseEntry is a plain VM spec with no host caps filled in.
func baseEntry() metadata.VMEntry {
	return metadata.VMEntry{
		ID:          "test-vm",
		NetworkMode: "nat",
		VCPUs:       2,
		MemoryMB:    2048,
	}
}

// sampleDomainCapsXML is modelled on QEMU 8.x / libvirt 9.x output.
const sampleDomainCapsXML = `<domainCapabilities>
  <path>/usr/bin/qemu-system-x86_64</path>
  <domain>kvm</domain>
  <machine>q35</machine>
  <arch>x86_64</arch>
  <vcpu max='128'/>
  <os supported='yes'>
    <enum name='firmware'>
      <value>bios</value>
      <value>efi</value>
    </enum>
    <loader supported='yes'>
      <enum name='type'>
        <value>rom</value>
        <value>pflash</value>
      </enum>
    </loader>
  </os>
</domainCapabilities>`

// TestParseDomainCaps checks each field falls back to defaultHostCaps on its own.
func TestParseDomainCaps(t *testing.T) {
	aarch64 := defaultHostCaps
	aarch64.Emulator = "/usr/bin/qemu-system-aarch64"
	for _, tc := range []struct {
		name, xml string
		want      hostCaps
	}{
		{"well-formed", sampleDomainCapsXML, hostCaps{
			Emulator: "/usr/bin/qemu-system-x86_64", DomainType: "kvm", Machine: "q35", Arch: "x86_64",
		}},
		{"empty", "", defaultHostCaps},
		{"invalid", "not xml <<<", defaultHostCaps},
		{"only a path", "<domainCapabilities>\n  <path>/usr/bin/qemu-system-aarch64</path>\n</domainCapabilities>", aarch64},
	} {
		if got := parseDomainCaps(tc.xml); got != tc.want {
			t.Errorf("%s: parseDomainCaps = %+v; want %+v", tc.name, got, tc.want)
		}
	}
}

// xnode is a schema-free parse of the rendered XML. It shares no tags with
// DomainSpec, so a misspelt element or attribute name cannot round-trip.
type xnode struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Text     string     `xml:",chardata"`
	Children []xnode    `xml:",any"`
}

// find returns every node at a slash-separated path below n, in document order.
func (n xnode) find(path string) []xnode {
	cur := []xnode{n}
	for _, seg := range strings.Split(path, "/") {
		var next []xnode
		for _, c := range cur {
			for _, ch := range c.Children {
				if ch.XMLName.Local == seg {
					next = append(next, ch)
				}
			}
		}
		cur = next
	}
	return cur
}

func (n xnode) attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// parseDoc renders a spec and parses it back schema-free.
func parseDoc(t *testing.T, s DomainSpec) xnode {
	t.Helper()
	doc, err := BuildDomainXML(s)
	if err != nil {
		t.Fatalf("BuildDomainXML: %v", err)
	}
	var root xnode
	if err := xml.Unmarshal([]byte(doc), &root); err != nil {
		t.Fatalf("rendered document does not parse: %v\n%s", err, doc)
	}
	return root
}

// specWithCaps is a plain VM with host capabilities filled in, as Manager would.
func specWithCaps() DomainSpec {
	s := DomainSpecFromEntry(baseEntry(), "/pool/test-vm.qcow2", "/pool/test-vm-cidata.iso")
	s.Type, s.OS.Type.Arch, s.OS.Type.Machine = "kvm", "x86_64", "q35"
	s.Devices.Emulator = "/usr/bin/qemu-system-x86_64"
	return s
}

// TestBuildDomainXML_LibvirtContract pins the names libvirt reads. A round-trip
// through DomainSpec's tags cannot catch a misspelling, which libvirt ignores.
func TestBuildDomainXML_LibvirtContract(t *testing.T) {
	root := parseDoc(t, specWithCaps())

	if root.XMLName.Local != "domain" {
		t.Errorf("root element = %q, want \"domain\"", root.XMLName.Local)
	}

	for _, tc := range []struct {
		path string
		attr string // "" asserts the element's text
		want string
	}{
		{"", "type", "kvm"},
		{"name", "", "test-vm"},
		{"memory", "unit", "MiB"},
		{"memory", "", "2048"},
		{"vcpu", "", "2"},
		{"os/type", "arch", "x86_64"},
		{"os/type", "machine", "q35"},
		{"os/type", "", "hvm"},
		{"os/boot", "dev", "hd"},
		{"cpu", "mode", "host-model"},
		{"clock", "offset", "utc"},
		// A guest that reboots itself must come back, not be destroyed.
		{"on_reboot", "", "restart"},
		{"on_poweroff", "", "destroy"},
		{"on_crash", "", "destroy"},
		{"devices/emulator", "", "/usr/bin/qemu-system-x86_64"},
		// VNC stays on loopback; the console is reached through the daemon's proxy.
		{"devices/graphics", "type", "vnc"},
		{"devices/graphics", "listen", "127.0.0.1"},
		{"devices/graphics/listen", "address", "127.0.0.1"},
		{"devices/graphics/listen", "type", "address"},
		{"devices/video/model", "vram", "16384"},
		{"devices/serial", "type", "pty"},
		{"devices/console/target", "type", "serial"},
		// The guest agent channel lets PrimaryIP confirm a bridged guest's address.
		{"devices/channel", "type", "unix"},
		{"devices/channel/target", "type", "virtio"},
		{"devices/channel/target", "name", "org.qemu.guest_agent.0"},
	} {
		label := tc.path + "@" + tc.attr
		t.Run(label, func(t *testing.T) {
			nodes := []xnode{root}
			if tc.path != "" {
				nodes = root.find(tc.path)
			}
			if len(nodes) != 1 {
				t.Fatalf("found %d nodes at %q, want 1", len(nodes), tc.path)
			}
			got := strings.TrimSpace(nodes[0].Text)
			if tc.attr != "" {
				got = nodes[0].attr(tc.attr)
			}
			if got != tc.want {
				t.Errorf("%s = %q, want %q", label, got, tc.want)
			}
		})
	}

	// Without ACPI the guest never sees DomainShutdown, so graceful stop does nothing.
	for _, path := range []string{"features/acpi", "features/apic"} {
		if len(root.find(path)) != 1 {
			t.Errorf("%s missing — expected exactly one", path)
		}
	}

	if n := len(root.find("devices/interface")); n != 1 {
		t.Errorf("got %d interfaces, want exactly 1", n)
	}

	// The seed ISO is a SATA cdrom because q35 has no IDE controller.
	disks := root.find("devices/disk")
	if len(disks) != 2 {
		t.Fatalf("got %d disks, want 2 (boot overlay + seed ISO)", len(disks))
	}
	boot, seed := disks[0], disks[1]
	for _, c := range []struct{ label, got, want string }{
		{"boot device", boot.attr("device"), "disk"},
		{"boot driver type", boot.find("driver")[0].attr("type"), "qcow2"},
		{"boot target dev", boot.find("target")[0].attr("dev"), "vda"},
		{"boot target bus", boot.find("target")[0].attr("bus"), "virtio"},
		{"boot source", boot.find("source")[0].attr("file"), "/pool/test-vm.qcow2"},
		{"seed device", seed.attr("device"), "cdrom"},
		{"seed driver type", seed.find("driver")[0].attr("type"), "raw"},
		{"seed target bus", seed.find("target")[0].attr("bus"), "sata"},
		{"seed source", seed.find("source")[0].attr("file"), "/pool/test-vm-cidata.iso"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.label, c.got, c.want)
		}
	}
	if len(seed.find("readonly")) != 1 {
		t.Error("seed ISO is not <readonly/> — the guest could write to its own seed")
	}
}

// firmwareSpec builds a spec for an entry with the firmware fields set.
func firmwareSpec(t *testing.T, mutate func(*metadata.VMEntry)) xnode {
	t.Helper()
	e := baseEntry()
	mutate(&e)
	s := DomainSpecFromEntry(e, "/pool/test-vm.qcow2", "/pool/test-vm-cidata.iso")
	s.Type, s.OS.Type.Arch, s.OS.Type.Machine = "kvm", "x86_64", "q35"
	return parseDoc(t, s)
}

// TestBuildDomainXML_Firmware pins the firmware elements. Most mistakes here
// fail in the guest, not at define time.
func TestBuildDomainXML_Firmware(t *testing.T) {
	t.Run("BIOS is the default and adds nothing", func(t *testing.T) {
		root := firmwareSpec(t, func(*metadata.VMEntry) {})

		if got := root.find("os")[0].attr("firmware"); got != "" {
			t.Errorf("os@firmware = %q on a BIOS domain, want empty", got)
		}
		for _, path := range []string{"os/loader", "os/nvram", "features/smm", "devices/tpm"} {
			if n := len(root.find(path)); n != 0 {
				t.Errorf("%s present on a BIOS domain (%d found)", path, n)
			}
		}
	})

	t.Run("UEFI without secure boot", func(t *testing.T) {
		root := firmwareSpec(t, func(e *metadata.VMEntry) { e.Firmware = metadata.FirmwareUEFI })

		if got := root.find("os")[0].attr("firmware"); got != "efi" {
			t.Errorf("os@firmware = %q, want \"efi\"", got)
		}
		// A writable variable store, or the guest forgets its boot order.
		if n := len(root.find("os/nvram")); n != 1 {
			t.Errorf("got %d <nvram> elements, want exactly 1", n)
		}
		loaders := root.find("os/loader")
		if len(loaders) != 1 {
			t.Fatalf("got %d <loader> elements, want 1", len(loaders))
		}
		if got := loaders[0].attr("type"); got != "pflash" {
			t.Errorf("loader@type = %q, want \"pflash\"", got)
		}
		if got := loaders[0].attr("readonly"); got != "yes" {
			t.Errorf("loader@readonly = %q, want \"yes\"", got)
		}
		// A path here pins a filename that differs per distro.
		if got := strings.TrimSpace(loaders[0].Text); got != "" {
			t.Errorf("loader names a firmware path (%q) — autoselection must choose it", got)
		}
		// secure='yes' restricts autoselection to signed firmware the host may lack.
		if got := loaders[0].attr("secure"); got != "" {
			t.Errorf("loader@secure = %q without secure boot requested", got)
		}
		if n := len(root.find("features/smm")); n != 0 {
			t.Errorf("SMM enabled (%d) without secure boot — it is only needed to hold the secure-boot variable store", n)
		}
	})

	t.Run("secure boot adds the two things libvirt refuses the domain without", func(t *testing.T) {
		root := firmwareSpec(t, func(e *metadata.VMEntry) {
			e.Firmware, e.SecureBoot = metadata.FirmwareUEFI, true
		})

		if got := root.find("os/loader")[0].attr("secure"); got != "yes" {
			t.Errorf("loader@secure = %q, want \"yes\"", got)
		}
		smm := root.find("features/smm")
		if len(smm) != 1 {
			t.Fatalf("got %d <smm> elements, want 1 — libvirt refuses secure boot without it", len(smm))
		}
		if got := smm[0].attr("state"); got != "on" {
			t.Errorf("smm@state = %q, want \"on\"", got)
		}
	})

	// Only a hand-edited metadata.json can set this; it must render as plain BIOS.
	t.Run("secure boot without UEFI is inert", func(t *testing.T) {
		root := firmwareSpec(t, func(e *metadata.VMEntry) { e.SecureBoot = true })

		for _, path := range []string{"os/loader", "features/smm"} {
			if n := len(root.find(path)); n != 0 {
				t.Errorf("%s present on a BIOS domain (%d found)", path, n)
			}
		}
	})

	t.Run("TPM is independent of firmware", func(t *testing.T) {
		root := firmwareSpec(t, func(e *metadata.VMEntry) { e.TPM = true })

		tpms := root.find("devices/tpm")
		if len(tpms) != 1 {
			t.Fatalf("got %d <tpm> elements, want 1", len(tpms))
		}
		if got := tpms[0].attr("model"); got != "tpm-crb" {
			t.Errorf("tpm@model = %q, want \"tpm-crb\"", got)
		}
		backend := tpms[0].find("backend")
		if len(backend) != 1 {
			t.Fatalf("got %d <backend> elements, want 1", len(backend))
		}
		if got := backend[0].attr("type"); got != "emulator" {
			t.Errorf("backend@type = %q, want \"emulator\" (swtpm)", got)
		}
		// Windows 11 requires 2.0; a 1.2 device passes its check for nothing.
		if got := backend[0].attr("version"); got != "2.0" {
			t.Errorf("backend@version = %q, want \"2.0\"", got)
		}
		if got := root.find("os")[0].attr("firmware"); got != "" {
			t.Errorf("os@firmware = %q, want empty: TPM must not imply UEFI", got)
		}
	})
}

// TestApplyCaps_SecureBootPinsQ35 guards the q35 pin for secure boot: only q35
// has SMM on x86, and QEMU's x86 default is still i440fx.
func TestApplyCaps_SecureBootPinsQ35(t *testing.T) {
	secure := func() DomainSpec {
		e := baseEntry()
		e.Firmware, e.SecureBoot = metadata.FirmwareUEFI, true
		return DomainSpecFromEntry(e, "/d.qcow2", "/d.iso")
	}

	t.Run("overrides an i440fx host default", func(t *testing.T) {
		m := &Manager{caps: hostCaps{Machine: "pc-i440fx-8.2", Arch: "x86_64", DomainType: "kvm"}}
		s := secure()
		m.applyCaps(&s)

		if s.OS.Type.Machine != "q35" {
			t.Errorf("machine = %q, want \"q35\" — secure boot needs SMM", s.OS.Type.Machine)
		}
	})

	t.Run("keeps a versioned q35 the host already chose", func(t *testing.T) {
		m := &Manager{caps: hostCaps{Machine: "pc-q35-8.2", Arch: "x86_64", DomainType: "kvm"}}
		s := secure()
		m.applyCaps(&s)

		// pc-q35-* is q35, so the host's versioned machine is kept.
		if s.OS.Type.Machine != "pc-q35-8.2" {
			t.Errorf("machine = %q, want the host's own q35 machine kept", s.OS.Type.Machine)
		}
	})

	// q35 exists only on x86.
	t.Run("leaves a non-x86 host alone", func(t *testing.T) {
		m := &Manager{caps: hostCaps{Machine: "virt", Arch: "aarch64", DomainType: "kvm"}}
		s := secure()
		m.applyCaps(&s)

		if s.OS.Type.Machine != "virt" {
			t.Errorf("machine = %q, want \"virt\" kept on aarch64", s.OS.Type.Machine)
		}
	})

	t.Run("does not disturb a BIOS domain", func(t *testing.T) {
		m := &Manager{caps: hostCaps{Machine: "pc-i440fx-8.2", Arch: "x86_64", DomainType: "kvm"}}
		s := DomainSpecFromEntry(baseEntry(), "/d.qcow2", "/d.iso")
		m.applyCaps(&s)

		if s.OS.Type.Machine != "pc-i440fx-8.2" {
			t.Errorf("machine = %q, want the host default untouched", s.OS.Type.Machine)
		}
	})
}

// hostDevFacts flattens a rendered <hostdev> to the attributes libvirt reads.
type hostDevFacts struct {
	Type    string
	Managed string
	// Source is "domain:bus:slot.function" for PCI, "vendor:product" for USB.
	Source string
}

func hostDevFactsOf(root xnode) []hostDevFacts {
	out := []hostDevFacts{}
	for _, h := range root.find("devices/hostdev") {
		f := hostDevFacts{Type: h.attr("type"), Managed: h.attr("managed")}
		if a := h.find("source/address"); len(a) > 0 {
			f.Source = a[0].attr("domain") + ":" + a[0].attr("bus") + ":" + a[0].attr("slot") + "." + a[0].attr("function")
		}
		if v := h.find("source/vendor"); len(v) > 0 {
			p := h.find("source/product")
			f.Source = v[0].attr("id") + ":" + p[0].attr("id")
		}
		out = append(out, f)
	}
	return out
}

// TestBuildDomainXML_HostDevs pins the passthrough element. Without
// managed='yes' libvirt leaves the device bound to its host driver.
func TestBuildDomainXML_HostDevs(t *testing.T) {
	gpu := metadata.PCIDevice{Address: "0000:01:00.0", Vendor: "NVIDIA", Product: "GTX 1050 Ti"}
	yubikey := metadata.USBDevice{VendorID: "0x1050", ProductID: "0x0407"}

	tests := []struct {
		name string
		pci  []metadata.PCIDevice
		usb  []metadata.USBDevice
		want []hostDevFacts
	}{
		{
			name: "no devices renders no hostdev at all",
			want: []hostDevFacts{},
		},
		{
			// libvirt does not accept the "0000:01:00.0" form that sysfs and lspci use.
			name: "a PCI device is located by its split, hex-prefixed address",
			pci:  []metadata.PCIDevice{gpu},
			want: []hostDevFacts{{Type: "pci", Managed: "yes", Source: "0x0000:0x01:0x00.0x0"}},
		},
		{
			// bus:device would follow whatever lands on that port after a replug.
			name: "a USB device is matched on vendor:product",
			usb:  []metadata.USBDevice{yubikey},
			want: []hostDevFacts{{Type: "usb", Managed: "yes", Source: "0x1050:0x0407"}},
		},
		{
			name: "both kinds coexist, PCI first",
			pci:  []metadata.PCIDevice{gpu},
			usb:  []metadata.USBDevice{yubikey},
			want: []hostDevFacts{
				{Type: "pci", Managed: "yes", Source: "0x0000:0x01:0x00.0x0"},
				{Type: "usb", Managed: "yes", Source: "0x1050:0x0407"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := baseEntry()
			e.PCIDevices, e.USBDevices = tt.pci, tt.usb

			got := hostDevFactsOf(parseDoc(t, DomainSpecFromEntry(e, "/d.qcow2", "/d.iso")))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("hostdevs = %+v, want %+v", got, tt.want)
			}

			for _, h := range parseDoc(t, DomainSpecFromEntry(e, "/d.qcow2", "/d.iso")).find("devices/hostdev") {
				if h.attr("mode") != "subsystem" {
					t.Errorf("hostdev@mode = %q, want \"subsystem\"", h.attr("mode"))
				}
			}
		})
	}
}

// TestValidateHostDevices covers entries hand-edited into metadata.json. A
// malformed address must stop the define, not boot the VM without its device.
func TestValidateHostDevices(t *testing.T) {
	tests := []struct {
		name    string
		pci     []metadata.PCIDevice
		usb     []metadata.USBDevice
		wantErr error // nil means the entry is acceptable
	}{
		{name: "no devices"},
		{name: "canonical address", pci: []metadata.PCIDevice{{Address: "0000:01:00.0"}}},
		{name: "uppercase hex is still an address", pci: []metadata.PCIDevice{{Address: "0000:0A:00.1"}}},
		{name: "surrounding space is trimmed", pci: []metadata.PCIDevice{{Address: " 0000:01:00.0 "}}},
		{name: "domain omitted", pci: []metadata.PCIDevice{{Address: "01:00.0"}}, wantErr: ErrInvalidPCIAddress},
		{name: "function out of range", pci: []metadata.PCIDevice{{Address: "0000:01:00.9"}}, wantErr: ErrInvalidPCIAddress},
		{name: "not hex", pci: []metadata.PCIDevice{{Address: "zzzz:01:00.0"}}, wantErr: ErrInvalidPCIAddress},
		{
			// The address is interpolated into XML, so trailing junk must be rejected.
			name: "trailing junk", pci: []metadata.PCIDevice{{Address: "0000:01:00.0'/>"}},
			wantErr: ErrInvalidPCIAddress,
		},
		{name: "empty address", pci: []metadata.PCIDevice{{Address: ""}}, wantErr: ErrInvalidPCIAddress},
		{name: "canonical usb id", usb: []metadata.USBDevice{{VendorID: "0x1050", ProductID: "0x0407"}}},
		{name: "usb id without 0x", usb: []metadata.USBDevice{{VendorID: "1050", ProductID: "0x0407"}}, wantErr: ErrInvalidUSBID},
		{name: "usb id too short", usb: []metadata.USBDevice{{VendorID: "0x105", ProductID: "0x0407"}}, wantErr: ErrInvalidUSBID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := baseEntry()
			e.PCIDevices, e.USBDevices = tt.pci, tt.usb

			err := ValidateHostDevices(e)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateHostDevices: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateHostDevices = %v, want it to wrap %v", err, tt.wantErr)
			}
		})
	}
}

// TestParseDomainHostDevs reads XML as libvirt writes it back, not as we render it.
func TestParseDomainHostDevs(t *testing.T) {
	// libvirt writes "0x0" where we emit "0x00", and adds a guest-side
	// <address type='pci'> that must not be read as a second device.
	const live = `<domain type='kvm'>
  <name>gpu-vm</name>
  <devices>
    <hostdev mode='subsystem' type='pci' managed='yes'>
      <driver name='vfio'/>
      <source>
        <address domain='0x0000' bus='0x1' slot='0x0' function='0x0'/>
      </source>
      <alias name='hostdev0'/>
      <address type='pci' domain='0x0000' bus='0x05' slot='0x00' function='0x0'/>
    </hostdev>
    <hostdev mode='subsystem' type='usb' managed='yes'>
      <source>
        <vendor id='0x1050'/>
        <product id='0x0407'/>
        <address bus='2' device='2'/>
      </source>
      <alias name='hostdev1'/>
      <address type='usb' bus='0' port='1'/>
    </hostdev>
  </devices>
</domain>`

	got, err := ParseDomainHostDevs(live)
	if err != nil {
		t.Fatalf("ParseDomainHostDevs: %v", err)
	}

	// Sorted, so it compares with HostDevRefs regardless of listing order.
	want := []HostDevRef{"0000:01:00.0", "0x1050:0x0407"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("refs = %v, want %v", got, want)
	}

	// The spec's refs must match exactly, or every VM reads as restart-required.
	e := baseEntry()
	e.PCIDevices = []metadata.PCIDevice{{Address: "0000:01:00.0"}}
	e.USBDevices = []metadata.USBDevice{{VendorID: "0x1050", ProductID: "0x0407"}}
	if fromSpec := HostDevRefs(e); !reflect.DeepEqual(fromSpec, got) {
		t.Errorf("HostDevRefs = %v, want it to match the live domain's %v", fromSpec, got)
	}
}

// TestRedefineFromSpec_KeepsDomainIdentity checks a redefine keeps the UUID and
// MAC. Without the UUID libvirt refuses the define as a duplicate; without the
// MAC it succeeds and the VM comes back with a new DHCP lease and IP.
func TestRedefineFromSpec_KeepsDomainIdentity(t *testing.T) {
	m := newTestManager(t)
	defer m.Close() //nolint:errcheck // best-effort cleanup in test

	const name = "identity-probe"
	e := baseEntry()
	e.ID = name

	if err := m.Define(VMCreateParams{
		Name: name, DiskPath: "/d.qcow2", CloudInitISO: "/d.iso",
		VCPUs: e.VCPUs, MemoryMB: e.MemoryMB, NetworkMode: e.NetworkMode,
	}); err != nil {
		t.Fatalf("define: %v", err)
	}
	defer m.Delete(name) //nolint:errcheck // best-effort cleanup in test

	before := identityOf(t, m, name)
	if before.UUID == "" || before.MAC == "" {
		t.Fatalf("libvirt assigned no identity to work with: %+v", before)
	}

	e.USBDevices = []metadata.USBDevice{{VendorID: "0x1050", ProductID: "0x0407"}}
	if err := m.RedefineFromSpec(e, "/d.qcow2", "/d.iso"); err != nil {
		t.Fatalf("RedefineFromSpec: %v", err)
	}

	if after := identityOf(t, m, name); after != before {
		t.Errorf("identity changed across a redefine: %+v -> %+v", before, after)
	}

	// Check the change landed, or a redefine that did nothing would pass.
	hw, err := m.ActiveHardware(name)
	if err != nil {
		t.Fatalf("ActiveHardware: %v", err)
	}
	if len(hw.HostDevs) != 1 || hw.HostDevs[0] != HostDevRef("0x1050:0x0407") {
		t.Errorf("hostdevs = %v, want the newly attached USB device", hw.HostDevs)
	}
}

// TestDefineFromSpec_GivesAFreshIdentity is the clone case: a second machine
// must get its own UUID and MAC, or two VMs fight over one DHCP lease.
func TestDefineFromSpec_GivesAFreshIdentity(t *testing.T) {
	m := newTestManager(t)
	defer m.Close() //nolint:errcheck // best-effort cleanup in test

	const source, clone = "identity-source", "identity-clone"
	e := baseEntry()
	e.ID = source
	if err := m.DefineFromSpec(e, "/d.qcow2", "/d.iso"); err != nil {
		t.Fatalf("define source: %v", err)
	}
	defer m.Delete(source) //nolint:errcheck // best-effort cleanup in test

	e.ID = clone
	if err := m.DefineFromSpec(e, "/c.qcow2", "/c.iso"); err != nil {
		t.Fatalf("define clone: %v", err)
	}
	defer m.Delete(clone) //nolint:errcheck // best-effort cleanup in test

	before, after := identityOf(t, m, source), identityOf(t, m, clone)
	if after.MAC == "" || after.UUID == "" {
		t.Fatalf("libvirt assigned the clone no identity of its own: %+v", after)
	}
	if after.MAC == before.MAC {
		t.Errorf("clone shares the source's MAC %s — both would claim the same DHCP lease", after.MAC)
	}
	if after.UUID == before.UUID {
		t.Errorf("clone shares the source's UUID %s", after.UUID)
	}

	// baseEntry carries no identity, so the checks above pass for free. An entry
	// that does carry one must define exactly that identity.
	const rebuilt = "identity-rebuilt"
	e.ID, e.UUID, e.MAC = rebuilt, before.UUID, before.MAC
	if err := m.Delete(source); err != nil {
		t.Fatalf("undefine the source before reusing its identity: %v", err)
	}
	if err := m.DefineFromSpec(e, "/d.qcow2", "/d.iso"); err != nil {
		t.Fatalf("define from a spec carrying an identity: %v", err)
	}
	defer m.Delete(rebuilt) //nolint:errcheck // best-effort cleanup in test

	if got := identityOf(t, m, rebuilt); got != before {
		t.Errorf("rebuilt domain identity = %+v, want the recorded %+v", got, before)
	}
}

func identityOf(t *testing.T, m *Manager, name string) DomainIdentity {
	t.Helper()
	id, err := m.Identity(name)
	if err != nil {
		t.Fatalf("Identity(%q): %v", name, err)
	}
	return id
}

// TestBuildDomainXML_WellFormed checks that XML-unsafe characters are escaped.
func TestBuildDomainXML_WellFormed(t *testing.T) {
	s := DomainSpecFromEntry(baseEntry(), "/pool/d.qcow2", "/pool/d.iso")
	// vmNamePattern gates the VM name but not the paths around it.
	s.Devices.Disks[0].Source.File = `/pool/a&b/<x>.qcow2`

	doc, err := BuildDomainXML(s)
	if err != nil {
		t.Fatalf("BuildDomainXML: %v", err)
	}

	dec := xml.NewDecoder(strings.NewReader(doc))
	for {
		if _, err := dec.Token(); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("document is not well-formed: %v\n%s", err, doc)
		}
	}

	root := parseDoc(t, s)
	if got := root.find("devices/disk")[0].find("source")[0].attr("file"); got != `/pool/a&b/<x>.qcow2` {
		t.Errorf("path did not survive escaping: got %q", got)
	}
	if strings.Contains(doc, "pc-i440fx-2.9") {
		t.Errorf("machine type must come from host caps, not a literal:\n%s", doc)
	}
}

// TestApplyCaps: host caps fill empty fields; a value already on the spec wins.
func TestApplyCaps(t *testing.T) {
	caps := hostCaps{Emulator: "/usr/bin/qemu-system-aarch64", Machine: "virt", Arch: "aarch64", DomainType: "qemu"}

	t.Run("fills empty fields", func(t *testing.T) {
		m := &Manager{caps: caps}
		s := DomainSpecFromEntry(baseEntry(), "/d.qcow2", "/d.iso")
		m.applyCaps(&s)

		if s.Type != "qemu" || s.OS.Type.Arch != "aarch64" || s.OS.Type.Machine != "virt" || s.Devices.Emulator != caps.Emulator {
			t.Errorf("caps not applied: type=%q arch=%q machine=%q emulator=%q",
				s.Type, s.OS.Type.Arch, s.OS.Type.Machine, s.Devices.Emulator)
		}
	})

	t.Run("spec wins over host", func(t *testing.T) {
		m := &Manager{caps: caps}
		s := DomainSpecFromEntry(baseEntry(), "/d.qcow2", "/d.iso")
		s.OS.Type.Machine = "q35"
		m.applyCaps(&s)

		if s.OS.Type.Machine != "q35" {
			t.Errorf("machine = %q, want q35 — an explicit spec value must not be overwritten", s.OS.Type.Machine)
		}
	})
}

// TestDomainSpecFromEntry_Network checks that an unrecognised mode still
// produces a NIC: no network at all is worse than the wrong one.
func TestDomainSpecFromEntry_Network(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mode        string
		bridge      string
		vlan        int
		wantType    string
		wantNetwork string
		wantBridge  string
		wantTag     int
	}{
		{name: "nat", mode: "nat", wantType: "network", wantNetwork: "default"},
		{name: "bridge defaults to br0", mode: "bridge", wantType: "bridge", wantBridge: "br0"},
		{name: "bridge named", mode: "bridge", bridge: "vmbr0", wantType: "bridge", wantBridge: "vmbr0"},
		{name: "bridge tagged", mode: "bridge", bridge: "br-lan", vlan: 20, wantType: "bridge", wantBridge: "br-lan", wantTag: 20},
		// The API refuses this; the renderer must still emit no tag on NAT.
		{name: "nat ignores a tag", mode: "nat", vlan: 20, wantType: "network", wantNetwork: "default"},
		{name: "empty mode", mode: "", wantType: "network", wantNetwork: "default"},
		{name: "unrecognised mode", mode: "nonsense", wantType: "network", wantNetwork: "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := baseEntry()
			e.NetworkMode, e.Bridge, e.VLANID = tc.mode, tc.bridge, tc.vlan
			ifaces := DomainSpecFromEntry(e, "/d.qcow2", "/d.iso").Devices.Interfaces

			if len(ifaces) != 1 {
				t.Fatalf("got %d interfaces, want exactly 1 — a guest gets one NIC", len(ifaces))
			}
			got := ifaces[0]
			if got.Type != tc.wantType {
				t.Errorf("interface type = %q, want %q", got.Type, tc.wantType)
			}
			if got.Source.Network != tc.wantNetwork {
				t.Errorf("source network = %q, want %q", got.Source.Network, tc.wantNetwork)
			}
			if got.Source.Bridge != tc.wantBridge {
				t.Errorf("source bridge = %q, want %q", got.Source.Bridge, tc.wantBridge)
			}
			tag := 0
			if got.VLAN != nil {
				tag = got.VLAN.Tag.ID
			}
			if tag != tc.wantTag {
				t.Errorf("vlan tag = %d, want %d", tag, tc.wantTag)
			}
		})
	}
}

// mustRenderDomainXML marshals an entry's domain XML as a redefine would.
func mustRenderDomainXML(t *testing.T, e metadata.VMEntry) string {
	t.Helper()
	b, err := xml.Marshal(DomainSpecFromEntry(e, "/pool/web.qcow2", "/pool/web-seed.iso"))
	if err != nil {
		t.Fatalf("marshal domain: %v", err)
	}
	return string(b)
}

func TestDomainXML_ProvisionedVLANEmitsNoVlanElement(t *testing.T) {
	// The subinterface tags the frames. Emitting <vlan> too would double-tag.
	e := metadata.VMEntry{
		ID: "web", NetworkMode: metadata.NetworkBridge,
		Bridge: "nc-vlan20", VLANID: 20, VLANUplink: "eth0",
	}
	xml := mustRenderDomainXML(t, e)

	if strings.Contains(xml, "<vlan>") {
		t.Errorf("provisioned VLAN emitted a <vlan> element, which double-tags:\n%s", xml)
	}
	if !strings.Contains(xml, `<source bridge="nc-vlan20">`) &&
		!strings.Contains(xml, `<source bridge="nc-vlan20"></source>`) {
		t.Errorf("guest is not attached to the provisioned bridge:\n%s", xml)
	}
}

func TestDomainXML_LegacyVLANKeepsItsTag(t *testing.T) {
	// A legacy entry has no uplink; dropping its tag moves it to the untagged VLAN.
	e := metadata.VMEntry{
		ID: "web", NetworkMode: metadata.NetworkBridge,
		Bridge: "br0", VLANID: 20,
	}
	xml := mustRenderDomainXML(t, e)

	if !strings.Contains(xml, `<tag id="20">`) && !strings.Contains(xml, `<tag id="20"></tag>`) {
		t.Errorf("legacy entry lost its VLAN tag, moving the guest to the untagged VLAN:\n%s", xml)
	}
}

func TestParseVNCDisplay(t *testing.T) {
	tests := []struct {
		name    string
		display string
		want    string
		wantErr bool
	}{
		{name: "loopback shorthand", display: ":0", want: "127.0.0.1:5900"},
		{name: "loopback shorthand with offset", display: ":3", want: "127.0.0.1:5903"},
		{name: "explicit host", display: "127.0.0.1:1", want: "127.0.0.1:5901"},
		{name: "trims whitespace/newline", display: ":0\n", want: "127.0.0.1:5900"},
		{name: "no colon", display: "garbage", wantErr: true},
		{name: "non-numeric offset", display: ":abc", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVNCDisplay(tt.display)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseVNCDisplay(%q) = %q, nil; want error", tt.display, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseVNCDisplay(%q) unexpected error: %v", tt.display, err)
			}
			if got != tt.want {
				t.Errorf("parseVNCDisplay(%q) = %q, want %q", tt.display, got, tt.want)
			}
		})
	}
}

// A stopped or missing domain has no VNC port, so VNCAddress must error.
func TestVNCAddress_NotRunning(t *testing.T) {
	m := newTestManager(t)
	defer m.Close() //nolint:errcheck // best-effort cleanup in test

	if _, err := m.VNCAddress("does-not-exist-xyz"); err == nil {
		t.Error("VNCAddress() for a nonexistent domain returned nil error; want an error")
	}
}

// isUnsupportedError reports whether the driver lacks the operation.
// test:///default implements no snapshot RPCs, so those tests skip.
func isUnsupportedError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "unsupported") ||
		strings.Contains(msg, "not supported") ||
		strings.Contains(msg, "this function is not supported")
}

func TestListSnapshots_NoSnapshots(t *testing.T) {
	m := newTestManager(t)
	defer m.Close() //nolint:errcheck

	snaps, err := m.ListSnapshots("test")
	if err != nil {
		if isUnsupportedError(err) {
			t.Skipf("test:///default driver does not support snapshots: %v", err)
		}
		t.Fatalf("ListSnapshots() unexpected error: %v", err)
	}
	if snaps == nil {
		t.Error("ListSnapshots() returned nil; want non-nil empty slice")
	}
	t.Logf("ListSnapshots() returned %d snapshot(s)", len(snaps))
}

func TestCreateSnapshot_SkipIfUnsupported(t *testing.T) {
	m := newTestManager(t)
	defer m.Close() //nolint:errcheck

	err := m.CreateSnapshot("test", "test-snap-1", "test snapshot")
	if err != nil {
		if isUnsupportedError(err) {
			t.Skipf("test:///default driver does not support snapshot create: %v", err)
		}
		t.Fatalf("CreateSnapshot() unexpected error: %v", err)
	}
	t.Log("CreateSnapshot() succeeded")
}

// TestSnapshotXMLParsing: malformed XML must be an error, not a zero Snapshot.
func TestSnapshotXMLParsing(t *testing.T) {
	for _, tc := range []struct {
		name, xml string
		want      Snapshot
		wantErr   bool
	}{
		{name: "shut off", xml: `<domainsnapshot>
  <name>snap-before-upgrade</name>
  <description>pre-upgrade baseline</description>
  <creationTime>1700000000</creationTime>
  <state>shutoff</state>
</domainsnapshot>`, want: Snapshot{Name: "snap-before-upgrade", Description: "pre-upgrade baseline",
			CreatedAt: time.Unix(1700000000, 0).UTC(), State: "shutoff"}},
		{name: "running", xml: `<domainsnapshot>
  <name>live-snap</name>
  <description></description>
  <creationTime>1234567890</creationTime>
  <state>running</state>
</domainsnapshot>`, want: Snapshot{Name: "live-snap", CreatedAt: time.Unix(1234567890, 0).UTC(), State: "running"}},
		{name: "invalid", xml: "not xml at all <<<", wantErr: true},
	} {
		got, err := ParseSnapshotXML(tc.xml)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: ParseSnapshotXML() error = %v; want error %v", tc.name, err, tc.wantErr)
			continue
		}
		if got.Name != tc.want.Name || got.Description != tc.want.Description ||
			!got.CreatedAt.Equal(tc.want.CreatedAt) || got.State != tc.want.State {
			t.Errorf("%s: ParseSnapshotXML() = %+v; want %+v", tc.name, got, tc.want)
		}
	}
}

// TestRestoreSnapshot_MissingVM fails at DomainLookupByName, which
// test:///default implements, so it runs even without snapshot RPCs.
func TestRestoreSnapshot_MissingVM(t *testing.T) {
	m := newTestManager(t)
	defer m.Close() //nolint:errcheck

	err := m.RestoreSnapshot("does-not-exist", "some-snap")
	if err == nil {
		t.Fatal("RestoreSnapshot() on missing VM: want error, got nil")
	}
	if !strings.Contains(err.Error(), "restore snapshot") {
		t.Errorf("RestoreSnapshot() error = %q; want it to mention %q", err.Error(), "restore snapshot")
	}
	t.Logf("RestoreSnapshot() on missing VM correctly errored: %v", err)
}

// Either "unsupported" or "not found" is an acceptable error here.
func TestRestoreSnapshot_MissingSnapshot(t *testing.T) {
	m := newTestManager(t)
	defer m.Close() //nolint:errcheck

	err := m.RestoreSnapshot("test", "definitely-not-a-real-snapshot")
	if err == nil {
		t.Fatal("RestoreSnapshot() with missing snapshot: want error, got nil")
	}
	t.Logf("RestoreSnapshot() with missing snapshot correctly errored: %v", err)
}

// TestRestoreSnapshot_LeavesDomainStopped checks a revert always lands stopped.
// It skips on test:///default, which has no snapshot RPCs.
func TestRestoreSnapshot_LeavesDomainStopped(t *testing.T) {
	m := newTestManager(t)
	defer m.Close() //nolint:errcheck

	const snapName = "restore-test-snap"
	if err := m.CreateSnapshot("test", snapName, "restore contract test"); err != nil {
		if isUnsupportedError(err) {
			t.Skipf("test:///default driver does not support snapshot create: %v", err)
		}
		t.Fatalf("CreateSnapshot() unexpected error: %v", err)
	}
	defer func() {
		_ = m.DeleteSnapshot("test", snapName) // best-effort cleanup
	}()

	if err := m.RestoreSnapshot("test", snapName); err != nil {
		if isUnsupportedError(err) {
			t.Skipf("test:///default driver does not support snapshot revert: %v", err)
		}
		t.Fatalf("RestoreSnapshot() unexpected error: %v", err)
	}

	got, err := m.Get("test")
	if err != nil {
		t.Fatalf("Get() after restore unexpected error: %v", err)
	}
	if got.State != "stopped" {
		t.Errorf("domain state after RestoreSnapshot() = %q; want %q (VM must always land stopped)", got.State, "stopped")
	}
}

// TestExists_ReportsDefinedDomains: a missing domain must be (false, nil), not
// an error. POST /vms relies on Exists to avoid truncating a live VM's disk.
func TestExists_ReportsDefinedDomains(t *testing.T) {
	m := newTestManager(t)
	defer m.Close() //nolint:errcheck // best-effort cleanup in test

	// test:///default pre-populates a domain named "test".
	vms, err := m.List()
	if err != nil {
		t.Fatalf("List() returned error: %v", err)
	}
	if len(vms) == 0 {
		t.Skip("test driver defined no domains; nothing to assert existence against")
	}

	existing := vms[0].Name
	got, err := m.Exists(existing)
	if err != nil {
		t.Fatalf("Exists(%q) returned error: %v", existing, err)
	}
	if !got {
		t.Errorf("Exists(%q) = false, want true", existing)
	}

	got, err = m.Exists("definitely-not-a-domain-xyz")
	if err != nil {
		t.Fatalf("Exists on a missing domain returned error %v; want (false, nil)", err)
	}
	if got {
		t.Error("Exists on a missing domain = true, want false")
	}
}

// TestDelete_UsesSnapshotMetadataFlag pins Delete's undefine flags;
// test:///default cannot create the snapshot that would exercise them.
func TestDelete_UsesSnapshotMetadataFlag(t *testing.T) {
	if undefineFlags&libvirt.DomainUndefineSnapshotsMetadata == 0 {
		t.Error("undefineFlags is missing DomainUndefineSnapshotsMetadata — " +
			"a VM with snapshots cannot be deleted without it")
	}
	if undefineFlags&libvirt.DomainUndefineManagedSave == 0 {
		t.Error("undefineFlags is missing DomainUndefineManagedSave")
	}
	// Drivers reject unsupported flags rather than ignore them, so NVRAM is added
	// per domain by undefineFlagsFor, never here.
	if undefineFlags&libvirt.DomainUndefineNvram != 0 {
		t.Error("undefineFlags sets DomainUndefineNvram unconditionally — " +
			"drivers without NVRAM reject the whole undefine call, so it belongs in undefineFlagsFor")
	}
}

// TestDelete_DomainAlreadyGoneIsNoOp pins Delete as "ensure this domain does not
// exist", so a VM whose domain is already gone can still be deleted.
func TestDelete_DomainAlreadyGoneIsNoOp(t *testing.T) {
	m := newTestManager(t)

	if err := m.Delete("definitely-not-a-domain-xyz"); err != nil {
		t.Errorf("Delete of a domain libvirt does not have = %v, want nil — "+
			"a VM whose domain is gone can never be cleaned up otherwise", err)
	}
}

// TestUndefineFlagsFor: NVRAM must be set for a UEFI domain or it cannot be
// undefined, and unset for BIOS or the driver rejects the call.
func TestUndefineFlagsFor(t *testing.T) {
	uefi := specWithCaps()
	uefi.OS.Firmware, uefi.OS.NVRAM = "efi", &nvram{}
	uefiXML, err := BuildDomainXML(uefi)
	if err != nil {
		t.Fatalf("BuildDomainXML: %v", err)
	}
	biosXML, err := BuildDomainXML(specWithCaps())
	if err != nil {
		t.Fatalf("BuildDomainXML: %v", err)
	}

	for _, tc := range []struct {
		name    string
		xml     string
		wantNVR bool
	}{
		{"UEFI domain", uefiXML, true},
		{"BIOS domain", biosXML, false},
		// Delete carries on when DomainGetXMLDesc fails, so empty XML is a real input.
		{"unreadable domain XML", "", false},
		{"malformed domain XML", "<domain><os><nvram", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := undefineFlagsFor(tc.xml)

			if hasNVR := got&libvirt.DomainUndefineNvram != 0; hasNVR != tc.wantNVR {
				t.Errorf("DomainUndefineNvram set = %v, want %v", hasNVR, tc.wantNVR)
			}
			// A snapshotted UEFI VM needs the base flags too.
			if got&undefineFlags != undefineFlags {
				t.Errorf("flags %#x dropped part of the base set %#x", got, undefineFlags)
			}
		})
	}
}

// NewLazy never dials, so it is safe at startup with an unreachable URI.
func TestNewLazy_DoesNotDialOrError(t *testing.T) {
	m := NewLazy(unreachableURI)
	if m == nil {
		t.Fatal("NewLazy returned nil")
	}
	if m.Connected() {
		t.Error("Connected() = true immediately after NewLazy, before any Connect call")
	}
}

// An unreachable URI surfaces as ErrNotConnected, which internal/api maps to 503.
func TestUnconnectedManager_MethodsReturnErrNotConnected(t *testing.T) {
	m := NewLazy(unreachableURI)

	if _, err := m.List(); !errors.Is(err, ErrNotConnected) {
		t.Errorf("List(): err = %v, want errors.Is(err, ErrNotConnected)", err)
	}
	if _, err := m.Exists("anything"); !errors.Is(err, ErrNotConnected) {
		t.Errorf("Exists(): err = %v, want errors.Is(err, ErrNotConnected)", err)
	}
	if err := m.Start("anything"); !errors.Is(err, ErrNotConnected) {
		t.Errorf("Start(): err = %v, want errors.Is(err, ErrNotConnected)", err)
	}
	if err := m.Create(VMCreateParams{Name: "anything"}); !errors.Is(err, ErrNotConnected) {
		t.Errorf("Create(): err = %v, want errors.Is(err, ErrNotConnected)", err)
	}
	// The stats sampler never dials; it relies on this to skip its VM tick.
	if _, err := m.SampleAll(); !errors.Is(err, ErrNotConnected) {
		t.Errorf("SampleAll(): err = %v, want errors.Is(err, ErrNotConnected)", err)
	}
	if _, err := m.ListNodeDeviceXML(); !errors.Is(err, ErrNotConnected) {
		t.Errorf("ListNodeDeviceXML(): err = %v, want errors.Is(err, ErrNotConnected)", err)
	}
	// Identity backfill relies on this: an unreachable libvirt means "retry",
	// never an empty identity written over a real one.
	if _, err := m.Identity("anything"); !errors.Is(err, ErrNotConnected) {
		t.Errorf("Identity(): err = %v, want errors.Is(err, ErrNotConnected)", err)
	}
}

// TestListNodeDeviceXML_FiltersToPCIAndUSB guards the capability filter. It
// catches a dropped filter, not a wrong mask.
func TestListNodeDeviceXML_FiltersToPCIAndUSB(t *testing.T) {
	m := newTestManager(t)
	defer m.Close() //nolint:errcheck // best-effort cleanup in test

	xmls, err := m.ListNodeDeviceXML()
	if err != nil {
		t.Fatalf("ListNodeDeviceXML() error: %v", err)
	}
	for i, x := range xmls {
		var doc struct {
			XMLName xml.Name `xml:"device"`
			Name    string   `xml:"name"`
			Caps    []struct {
				Type string `xml:"type,attr"`
			} `xml:"capability"`
		}
		if err := xml.Unmarshal([]byte(x), &doc); err != nil {
			t.Errorf("document %d is not valid XML: %v\n%s", i, err, x)
			continue
		}
		if doc.Name == "" {
			t.Errorf("document %d has no <name>:\n%s", i, x)
		}
		for _, c := range doc.Caps {
			if c.Type != "pci" && c.Type != "usb_device" {
				t.Errorf("device %q has capability %q; the filter should have excluded it", doc.Name, c.Type)
			}
		}
	}
}

// SampleAll must report the guest's used memory, not DomainGetInfo's balloon
// allocation, which pins every memory bar at 100%.
func TestSampleAll_ReportsGuestUsedMemoryNotBalloonAllocation(t *testing.T) {
	m := newTestManager(t)
	defer m.Close() //nolint:errcheck // test cleanup

	samples, err := m.SampleAll()
	if err != nil {
		t.Fatalf("SampleAll() returned error: %v", err)
	}

	lv, err := m.conn()
	if err != nil {
		t.Fatalf("conn() returned error: %v", err)
	}

	var found bool
	for _, s := range samples {
		if s.State != libvirt.DomainRunning {
			continue
		}
		found = true

		d, err := lv.DomainLookupByName(s.Name)
		if err != nil {
			t.Fatalf("lookup %q: %v", s.Name, err)
		}
		_, _, balloonKB, _, _, err := lv.DomainGetInfo(d)
		if err != nil {
			t.Fatalf("DomainGetInfo %q: %v", s.Name, err)
		}
		if s.MemUsedKB == balloonKB {
			t.Errorf("%s: MemUsedKB = %d, the balloon allocation — want the guest figure from DomainMemoryStats", s.Name, s.MemUsedKB)
		}
		if s.MemUsedKB == 0 {
			t.Errorf("%s: MemUsedKB = 0, want a real figure", s.Name)
		}
		if s.NrVCPU == 0 {
			t.Errorf("%s: NrVCPU = 0, want the domain's vcpu count — CPU%% divides by it", s.Name)
		}
	}
	if !found {
		t.Skip("test:///default exposed no running domain")
	}
}

func TestClose_NeverConnected_DoesNotPanic(t *testing.T) {
	m := NewLazy(unreachableURI)
	if err := m.Close(); err != nil {
		t.Errorf("Close() on a never-connected Manager returned %v, want nil", err)
	}
	if err := m.Close(); err != nil {
		t.Errorf("second Close() on a never-connected Manager returned %v, want nil", err)
	}
}

func TestNewLazy_ThenConnect_MethodsWork(t *testing.T) {
	m := NewLazy(testURI)
	if err := m.Connect(); err != nil {
		t.Skipf("libvirt not available (%v): skipping", err)
	}
	defer m.Close() //nolint:errcheck // best-effort cleanup in test

	if !m.Connected() {
		t.Error("Connected() = false after a successful Connect()")
	}
	if _, err := m.List(); err != nil {
		t.Errorf("List() after Connect(): %v", err)
	}
}

// TestConn_ConcurrentCallersDoNotRace sends many goroutines into conn()'s first
// dial at once. Run with -race.
func TestConn_ConcurrentCallersDoNotRace(t *testing.T) {
	m := NewLazy(testURI)

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if _, err := m.List(); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	if err, ok := <-errs; ok {
		t.Skipf("libvirt not available (%v): skipping", err)
	}
	if !m.Connected() {
		t.Error("Connected() = false after concurrent List() calls succeeded")
	}
	_ = m.Close()
}

// TestConnect_ReusesLiveConnection guards the double-check in connect(). Without
// it, callers reconnecting after a libvirtd restart disconnect each other.
func TestConnect_ReusesLiveConnection(t *testing.T) {
	m := NewLazy(testURI)
	if err := m.Connect(); err != nil {
		t.Skipf("libvirt not available (%v): skipping", err)
	}
	defer func() { _ = m.Close() }()

	first := m.lv
	if err := m.Connect(); err != nil {
		t.Fatalf("second Connect() returned error: %v", err)
	}

	if m.lv != first {
		t.Error("second Connect() replaced a live handle; want the existing one reused")
	}
	if !first.IsConnected() {
		t.Error("second Connect() disconnected the handle installed by the first")
	}
}

// TestPrimaryIPFrom: a DHCP lease outlives the guest that held it, so a lease
// names an address but never confirms the guest is up. A live source ends the
// search, keeping the healthy case at one query.
func TestPrimaryIPFrom(t *testing.T) {
	const (
		leaseIP = "192.168.122.50"
		liveIP  = "192.168.122.77"
	)

	for _, tc := range []struct {
		name          string
		have          map[libvirt.DomainInterfaceAddressesSource][]string
		wantIP        string
		wantConfirmed bool
		wantQueries   int
	}{
		{
			// A hung guest: in the lease file, answering nothing.
			name:        "a lease alone names an address but confirms nothing",
			have:        map[libvirt.DomainInterfaceAddressesSource][]string{libvirt.DomainInterfaceAddressesSrcLease: {leaseIP}},
			wantIP:      leaseIP,
			wantQueries: 3,
		},
		{
			name:          "an ARP entry means the guest answered",
			have:          map[libvirt.DomainInterfaceAddressesSource][]string{libvirt.DomainInterfaceAddressesSrcArp: {liveIP}},
			wantIP:        liveIP,
			wantConfirmed: true,
			wantQueries:   1,
		},
		{
			name:          "the guest agent also confirms",
			have:          map[libvirt.DomainInterfaceAddressesSource][]string{libvirt.DomainInterfaceAddressesSrcAgent: {liveIP}},
			wantIP:        liveIP,
			wantConfirmed: true,
			wantQueries:   2,
		},
		{
			// The healthy steady state, and the one that must stay cheap.
			name: "a live source wins the address over the lease, and short-circuits",
			have: map[libvirt.DomainInterfaceAddressesSource][]string{
				libvirt.DomainInterfaceAddressesSrcLease: {leaseIP},
				libvirt.DomainInterfaceAddressesSrcArp:   {liveIP},
			},
			wantIP:        liveIP,
			wantConfirmed: true,
			wantQueries:   1,
		},
		{
			name:        "nothing anywhere",
			have:        nil,
			wantIP:      "",
			wantQueries: 3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queries := 0
			ip, confirmed := primaryIPFrom(func(src libvirt.DomainInterfaceAddressesSource) []string {
				queries++
				return tc.have[src]
			})

			if ip != tc.wantIP || confirmed != tc.wantConfirmed {
				t.Errorf("primaryIPFrom() = (%q, %v), want (%q, %v)", ip, confirmed, tc.wantIP, tc.wantConfirmed)
			}
			if queries != tc.wantQueries {
				t.Errorf("made %d source queries, want %d", queries, tc.wantQueries)
			}
		})
	}
}

// TestIPv4sOf keeps PrimaryIP from handing vmPhase an address it cannot use.
func TestIPv4sOf(t *testing.T) {
	got := ipv4sOf([]libvirt.DomainInterface{{
		Name: "lo",
		Addrs: []libvirt.DomainIPAddr{
			{Type: int32(libvirt.IPAddrTypeIpv4), Addr: "127.0.0.1"},
			{Type: int32(libvirt.IPAddrTypeIpv6), Addr: "fe80::1"},
			{Type: int32(libvirt.IPAddrTypeIpv4), Addr: ""},
			{Type: int32(libvirt.IPAddrTypeIpv4), Addr: "192.168.122.9"},
		},
	}})

	if len(got) != 1 || got[0] != "192.168.122.9" {
		t.Errorf("ipv4sOf() = %v, want [192.168.122.9] — loopback, IPv6 and empty must be dropped", got)
	}
}

func agentIface(name, mac string, ipv4s ...string) libvirt.DomainInterface {
	iface := libvirt.DomainInterface{Name: name, Hwaddr: libvirt.OptString{mac}}
	for _, ip := range ipv4s {
		iface.Addrs = append(iface.Addrs, libvirt.DomainIPAddr{Type: int32(libvirt.IPAddrTypeIpv4), Addr: ip, Prefix: 24})
	}
	return iface
}

// TestAgentIPv4s: the agent lists every guest interface, and on a k3s or Docker
// host the bridges come first, so the NIC is picked by MAC.
func TestAgentIPv4s(t *testing.T) {
	const nicMAC = "52:54:00:75:fa:8b"
	reply := []libvirt.DomainInterface{
		agentIface("lo", "00:00:00:00:00:00", "127.0.0.1"),
		agentIface("docker0", "02:42:ac:11:00:01", "172.17.0.1"),
		agentIface("cni0", "6e:1f:2a:00:00:01", "10.42.0.1"),
		agentIface("flannel.1", "aa:bb:cc:00:00:01", "10.42.0.0"),
		agentIface("eth0", "52:54:00:75:FA:8B", "192.168.5.16"),
	}

	for _, tc := range []struct {
		name  string
		reply []libvirt.DomainInterface
		mac   string
		want  []string
	}{
		{"the NIC's MAC wins over the runtime-made links", reply, nicMAC, []string{"192.168.5.16"}},
		{
			// No name filter knows a VPN link; only the MAC tells them apart.
			"the NIC's MAC, compared case-insensitively, wins over a link no filter names",
			[]libvirt.DomainInterface{agentIface("wg0", "", "10.8.0.2"), agentIface("eth0", "52:54:00:75:FA:8B", "192.168.5.16")},
			nicMAC, []string{"192.168.5.16"},
		},
		{"no readable MAC skips the runtime-made links", reply, "", []string{"192.168.5.16"}},
		{"an unmatched MAC falls back the same way", reply, "52:54:00:00:00:99", []string{"192.168.5.16"}},
		{
			"the NIC without an IPv4 yet means no address, not docker0's",
			[]libvirt.DomainInterface{agentIface("docker0", "02:42:ac:11:00:01", "172.17.0.1"), agentIface("eth0", nicMAC)},
			nicMAC, nil,
		},
		{
			"link-local is never the guest's address",
			[]libvirt.DomainInterface{agentIface("ens3", "52:54:00:00:00:01", "169.254.10.1", "10.0.0.5")},
			"", []string{"10.0.0.5"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentIPv4s(tc.reply, tc.mac); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("agentIPv4s() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDefineFromSpec_RejectsIncompleteSpec: an entry without VCPUs or MemoryMB
// fails in libvirt with an unrelated <numa> error. The check runs before
// conn(), so it needs no libvirt.
func TestDefineFromSpec_RejectsIncompleteSpec(t *testing.T) {
	m := NewLazy(unreachableURI)

	for _, tc := range []struct {
		name  string
		entry metadata.VMEntry
	}{
		{"legacy entry, no hardware recorded", metadata.VMEntry{ID: "old-vm"}},
		{"no vcpus", metadata.VMEntry{ID: "x", MemoryMB: 2048}},
		{"no memory", metadata.VMEntry{ID: "x", VCPUs: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := m.DefineFromSpec(tc.entry, "/d.qcow2", "/d.iso")
			if !errors.Is(err, ErrIncompleteSpec) {
				t.Errorf("DefineFromSpec = %v, want ErrIncompleteSpec", err)
			}
		})
	}

	// A complete spec passes validation and fails on the connection instead.
	err := m.DefineFromSpec(baseEntry(), "/d.qcow2", "/d.iso")
	if !errors.Is(err, ErrNotConnected) {
		t.Errorf("complete spec: DefineFromSpec = %v, want ErrNotConnected", err)
	}
}

// runningAgentDomainXML is a running domain's guest-agent channel as libvirt
// reports it, with details we never render.
const runningAgentDomainXML = `<domain type='kvm' id='3'>
  <name>agent-probe</name>
  <vcpu placement='static'>2</vcpu>
  <cpu mode='custom' match='exact' check='full'><model fallback='forbid'>Skylake-Client-IBRS</model></cpu>
  <devices>
    <disk type='file' device='disk'><source file='/pool/agent-probe.qcow2'/><target dev='vda' bus='virtio'/></disk>
    <disk type='file' device='cdrom'><source file='/pool/agent-probe-cidata.iso'/><target dev='sda' bus='sata'/><readonly/></disk>
    <controller type='virtio-serial' index='0'><alias name='virtio-serial0'/></controller>
    <interface type='network'>
      <mac address='52:54:00:12:34:56'/>
      <source network='default' portid='ff01' bridge='virbr0'/>
      <model type='virtio'/>
    </interface>
    <channel type='unix'>
      <source mode='bind' path='/var/lib/libvirt/qemu/channel/target/domain-3-agent-probe/org.qemu.guest_agent.0'/>
      <target type='virtio' name='org.qemu.guest_agent.0' state='connected'/>
      <alias name='channel0'/>
      <address type='virtio-serial' controller='0' bus='0' port='1'/>
    </channel>
  </devices>
</domain>`

// TestParseActiveHardware_IgnoresGuestAgentChannel: the channel is not
// hardware. An existing VM gains it on redefine but lacks it until restart, so
// counting it would mark every VM restart-required.
func TestParseActiveHardware_IgnoresGuestAgentChannel(t *testing.T) {
	withChannel, err := parseActiveHardware(runningAgentDomainXML)
	if err != nil {
		t.Fatalf("parse with channel: %v", err)
	}

	start := strings.Index(runningAgentDomainXML, "<controller")
	end := strings.Index(runningAgentDomainXML, "</channel>") + len("</channel>")
	before := runningAgentDomainXML[:start] + runningAgentDomainXML[end:]
	without, err := parseActiveHardware(before)
	if err != nil {
		t.Fatalf("parse without channel: %v", err)
	}

	if !reflect.DeepEqual(withChannel, without) {
		t.Errorf("the guest-agent channel changed the active hardware:\n with:    %+v\n without: %+v", withChannel, without)
	}

	e := metadata.VMEntry{ID: "agent-probe", VCPUs: 2, NetworkMode: metadata.NetworkNAT}
	if CPUShapeDrifted(CPUShapeOf(e), withChannel.CPU) ||
		withChannel.Network != NetworkShapeOf(e) ||
		!reflect.DeepEqual(withChannel.DiskTargets, DiskTargets(e)) ||
		len(withChannel.HostDevs) != 0 {
		t.Errorf("a domain matching its spec reads as drifted: %+v", withChannel)
	}
}
