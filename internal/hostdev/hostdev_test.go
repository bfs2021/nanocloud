package hostdev_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/hostdev"
)

// sysfsRoot is a fixture tree with two IOMMU groups and a boot_vga file for
// 0000:01:00.0 ("1") and 0000:01:00.1 ("0").
const sysfsRoot = "testdata/sysfs"

// whyBridge is the refusal a bridge earns, spelled out rather than imported so
// a rewording of the user-facing text fails the test.
const whyBridge = "this is a bridge the host uses to reach the devices behind it, not an endpoint — pass through the device on the far side of it instead"

// fixtures reads the named node-device documents from testdata/nodedev.
func fixtures(t *testing.T, names ...string) []string {
	t.Helper()
	out := make([]string, 0, len(names))
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join("testdata", "nodedev", n+".xml"))
		if err != nil {
			t.Fatalf("read fixture %s: %v", n, err)
		}
		out = append(out, string(b))
	}
	return out
}

func TestParseNodeDeviceXML(t *testing.T) {
	tests := []struct {
		fixture string
		want    hostdev.Device
	}{
		{
			fixture: "gpu",
			want: hostdev.Device{
				Name: "pci_0000_01_00_0",
				PCI: &hostdev.PCIDevice{
					Address:    "0000:01:00.0",
					Vendor:     "NVIDIA Corporation",
					Product:    "GP107 [GeForce GTX 1050 Ti]",
					Class:      "VGA compatible controller",
					Driver:     "vfio-pci",
					IOMMUGroup: 13,
				},
				Group: []string{"0000:01:00.0", "0000:01:00.1"},
			},
		},
		{
			fixture: "gpu-audio",
			want: hostdev.Device{
				Name: "pci_0000_01_00_1",
				PCI: &hostdev.PCIDevice{
					Address:    "0000:01:00.1",
					Vendor:     "NVIDIA Corporation",
					Product:    "GP107GL High Definition Audio Controller",
					Class:      "Audio device",
					Driver:     "snd_hda_intel",
					IOMMUGroup: 13,
				},
				Group: []string{"0000:01:00.0", "0000:01:00.1"},
			},
		},
		{
			// Slot 28 decimal must render as 1c hex. A PCI-to-PCI bridge does
			// not disqualify its group.
			fixture: "bridge",
			want: hostdev.Device{
				Name: "pci_0000_00_1c_0",
				PCI: &hostdev.PCIDevice{
					Address:    "0000:00:1c.0",
					Vendor:     "Intel Corporation",
					Product:    "100 Series PCI Express Root Port #1",
					Class:      "PCI bridge",
					Bridge:     true,
					Driver:     "pcieport",
					IOMMUGroup: 7,
				},
				Group: []string{"0000:00:1c.0", "0000:03:00.0"},
				Self:  whyBridge,
			},
		},
		{
			fixture: "nic",
			want: hostdev.Device{
				Name: "pci_0000_03_00_0",
				PCI: &hostdev.PCIDevice{
					Address:    "0000:03:00.0",
					Vendor:     "Realtek Semiconductor Co., Ltd.",
					Product:    "RTL8111/8168/8411 PCI Express Gigabit Ethernet Controller",
					Class:      "Ethernet controller",
					Driver:     "r8169",
					IOMMUGroup: 7,
				},
				Group: []string{"0000:00:1c.0", "0000:03:00.0"},
			},
		},
		{
			// <class> is optional; the nested pci-bridge capability must
			// still name the device.
			fixture: "bridge-no-class",
			want: hostdev.Device{
				Name: "pci_0000_00_1d_0",
				PCI: &hostdev.PCIDevice{
					Address:    "0000:00:1d.0",
					Vendor:     "Intel Corporation",
					Product:    "100 Series PCI Express Root Port #9",
					Class:      "PCI bridge",
					Bridge:     true,
					Driver:     "pcieport",
					IOMMUGroup: 21,
				},
				Group: []string{"0000:00:1d.0"},
				Self:  whyBridge,
			},
		},
		{
			// No <iommuGroup>: group -1 and no siblings.
			fixture: "gpu-no-iommu",
			want: hostdev.Device{
				Name: "pci_0000_02_00_0",
				PCI: &hostdev.PCIDevice{
					Address:    "0000:02:00.0",
					Vendor:     "Advanced Micro Devices, Inc. [AMD/ATI]",
					Product:    "Navi 10 [Radeon RX 5600 XT]",
					Class:      "VGA compatible controller",
					Driver:     "amdgpu",
					IOMMUGroup: -1,
				},
				Group: []string{},
			},
		},
		{
			fixture: "usb",
			want: hostdev.Device{
				Name: "usb_1_4",
				USB: &hostdev.USBDevice{
					VendorID:    "0x1050",
					ProductID:   "0x0407",
					Description: "Yubico.com YubiKey OTP+FIDO+CCID",
					BusSeen:     "bus 1 device 4",
				},
				Group: []string{},
			},
		},
		{
			// An unmodelled capability type yields no device and no error.
			fixture: "system",
			want:    hostdev.Device{Name: "computer", Group: []string{}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			got, err := hostdev.ParseNodeDeviceXML(fixtures(t, tt.fixture)[0])
			if err != nil {
				t.Fatalf("ParseNodeDeviceXML() error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseNodeDeviceXML()\n got = %+v\nwant = %+v", got, tt.want)
				if got.PCI != nil && tt.want.PCI != nil {
					t.Errorf("  pci got = %+v\n  pci want = %+v", *got.PCI, *tt.want.PCI)
				}
			}
		})
	}
}

func TestParseNodeDeviceXML_Invalid(t *testing.T) {
	if _, err := hostdev.ParseNodeDeviceXML("not xml at all <<<"); err == nil {
		t.Error("ParseNodeDeviceXML() with invalid XML: want error, got nil")
	}
}

// pciFacts is what Build adds on top of a parsed document. Copied-through
// fields are covered by TestParseNodeDeviceXML.
type pciFacts struct {
	Address  string
	Group    int
	BootVGA  bool
	Siblings []string
	Usable   bool
	Reason   string
}

func pciFactsOf(devs []hostdev.PCIDevice) []pciFacts {
	out := make([]pciFacts, 0, len(devs))
	for _, d := range devs {
		out = append(out, pciFacts{
			Address: d.Address, Group: d.IOMMUGroup, BootVGA: d.BootVGA,
			Siblings: d.Siblings, Usable: d.Usable, Reason: d.Reason,
		})
	}
	return out
}

// usbKeysOf reduces the USB list to the identity Build must preserve.
func usbKeysOf(devs []hostdev.USBDevice) []string {
	out := make([]string, 0, len(devs))
	for _, d := range devs {
		out = append(out, d.VendorID+":"+d.ProductID)
	}
	return out
}

func TestBuild(t *testing.T) {
	// noSysfs stands in for a host with IOMMU off.
	noSysfs := t.TempDir()

	tests := []struct {
		name             string
		fixtures         []string
		sysRoot          string
		wantIOMMUEnabled bool
		wantGroups       int
		wantPCI          []pciFacts
		wantUSB          []string
	}{
		{
			// boot_vga is "1" for 01:00.0 and "0" for 01:00.1, so the file's
			// content is read, not just its presence.
			name:             "gpu and its audio function are siblings and both usable",
			fixtures:         []string{"gpu", "gpu-audio"},
			sysRoot:          sysfsRoot,
			wantIOMMUEnabled: true,
			wantGroups:       2,
			wantPCI: []pciFacts{
				{Address: "0000:01:00.0", Group: 13, BootVGA: true, Siblings: []string{"0000:01:00.1"}, Usable: true},
				{Address: "0000:01:00.1", Group: 13, BootVGA: false, Siblings: []string{"0000:01:00.0"}, Usable: true},
			},
			wantUSB: []string{},
		},
		{
			// The chipset's disk controller and onboard audio share a group
			// on many Intel boards.
			name:             "a storage controller warns off its whole group",
			fixtures:         []string{"sata", "onboard-audio"},
			sysRoot:          sysfsRoot,
			wantIOMMUEnabled: true,
			wantGroups:       2,
			wantPCI: []pciFacts{
				{
					Address: "0000:00:17.0", Group: 9, Siblings: []string{"0000:00:1f.3"},
					Reason: "this device is a SATA controller, which probably carries the host's own disks",
				},
				{
					Address: "0000:00:1f.3", Group: 9, Siblings: []string{"0000:00:17.0"},
					Reason: "IOMMU group 9 also contains 0000:00:17.0 (SATA controller, which probably carries the host's own disks); a group is passed through as one unit",
				},
			},
			wantUSB: []string{},
		},
		{
			// A host bridge (class 0x0600) disqualifies its group; a
			// PCI-to-PCI bridge (nested capability, no <class>) is refused
			// only for itself.
			name:             "both kinds of bridge are refused, for different reasons",
			fixtures:         []string{"bridge-no-class", "host-bridge"},
			sysRoot:          sysfsRoot,
			wantIOMMUEnabled: true,
			wantGroups:       2,
			wantPCI: []pciFacts{
				{
					Address: "0000:00:00.0", Group: 0, Siblings: []string{},
					Reason: "this device is a Host bridge, which cannot be released by the host",
				},
				{Address: "0000:00:1d.0", Group: 21, Siblings: []string{}, Reason: whyBridge},
			},
			wantUSB: []string{},
		},
		{
			// The subject is the device behind the bridge, not the bridge.
			name:             "a PCI-to-PCI bridge does not block the endpoint behind it",
			fixtures:         []string{"bridge", "nic"},
			sysRoot:          sysfsRoot,
			wantIOMMUEnabled: true,
			wantGroups:       2,
			wantPCI: []pciFacts{
				{
					Address: "0000:00:1c.0", Group: 7,
					Siblings: []string{"0000:03:00.0"}, Reason: whyBridge,
				},
				{
					Address: "0000:03:00.0", Group: 7,
					Siblings: []string{"0000:00:1c.0"}, Usable: true,
				},
			},
			wantUSB: []string{},
		},
		{
			// The bridge fixture is withheld, so the NIC's sibling cannot be
			// classified and must not block.
			name:             "an unclassifiable sibling leaves the device usable",
			fixtures:         []string{"nic"},
			sysRoot:          sysfsRoot,
			wantIOMMUEnabled: true,
			wantGroups:       2,
			wantPCI: []pciFacts{
				{Address: "0000:03:00.0", Group: 7, Siblings: []string{"0000:00:1c.0"}, Usable: true},
			},
			wantUSB: []string{},
		},
		{
			name:             "iommu off still reports the device",
			fixtures:         []string{"gpu-no-iommu"},
			sysRoot:          noSysfs,
			wantIOMMUEnabled: false,
			wantPCI: []pciFacts{
				{Address: "0000:02:00.0", Group: -1, Siblings: []string{}, Usable: true},
			},
			wantUSB: []string{},
		},
		{
			name:             "usb and ignored capability types",
			fixtures:         []string{"usb", "system"},
			sysRoot:          noSysfs,
			wantIOMMUEnabled: false,
			wantPCI:          []pciFacts{},
			wantUSB:          []string{"0x1050:0x0407"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hostdev.Build(fixtures(t, tt.fixtures...), tt.sysRoot)

			if got.IOMMU.Enabled != tt.wantIOMMUEnabled {
				t.Errorf("IOMMU.Enabled = %v, want %v", got.IOMMU.Enabled, tt.wantIOMMUEnabled)
			}
			if got.IOMMU.GroupsSeen != tt.wantGroups {
				t.Errorf("IOMMU.GroupsSeen = %d, want %d", got.IOMMU.GroupsSeen, tt.wantGroups)
			}
			if got.IOMMU.Message == "" {
				t.Error("IOMMU.Message is empty; the readout must always say something")
			}
			// The remedy is shown only while IOMMU is off.
			if hasRemedy := got.IOMMU.Remedy != ""; hasRemedy == tt.wantIOMMUEnabled {
				t.Errorf("IOMMU.Remedy = %q with Enabled = %v", got.IOMMU.Remedy, got.IOMMU.Enabled)
			}
			if diff := pciFactsOf(got.PCI); !reflect.DeepEqual(diff, tt.wantPCI) {
				t.Errorf("PCI\n got = %+v\nwant = %+v", diff, tt.wantPCI)
			}
			if keys := usbKeysOf(got.USB); !reflect.DeepEqual(keys, tt.wantUSB) {
				t.Errorf("USB\n got = %v\nwant = %v", keys, tt.wantUSB)
			}
		})
	}
}

// TestBuild_SkipsUnparsableDocument guards that one bad document does not
// blank the inventory.
func TestBuild_SkipsUnparsableDocument(t *testing.T) {
	xmls := append(fixtures(t, "gpu"), "<<< not a device")
	if got := hostdev.Build(xmls, sysfsRoot); len(got.PCI) != 1 {
		t.Errorf("len(PCI) = %d, want 1", len(got.PCI))
	}
}

// TestReport_MarshalsEmptyListsAsArrays guards that every empty list encodes
// as [] rather than null, which the frontend cannot iterate.
func TestReport_MarshalsEmptyListsAsArrays(t *testing.T) {
	empty, err := json.Marshal(hostdev.Build(nil, t.TempDir()))
	if err != nil {
		t.Fatalf("marshal empty report: %v", err)
	}
	for _, want := range []string{`"pci":[]`, `"usb":[]`} {
		if !strings.Contains(string(empty), want) {
			t.Errorf("empty report %s, want it to contain %s", empty, want)
		}
	}

	lone, err := json.Marshal(hostdev.Build(fixtures(t, "gpu-no-iommu"), t.TempDir()))
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if !strings.Contains(string(lone), `"siblings":[]`) {
		t.Errorf("report %s, want it to contain \"siblings\":[]", lone)
	}
}

// TestBuild_NamesDevices covers the three ways a PCI device gets a name,
// including libvirt reporting empty PCI vendor and product names.
func TestBuild_NamesDevices(t *testing.T) {
	// IDs readable, no pci.ids anywhere.
	noDB := t.TempDir()
	for name, content := range map[string]string{"vendor": "0x10de\n", "device": "0x1c82\n"} {
		dir := filepath.Join(noDB, "sys", "bus", "pci", "devices", "0000:01:00.0")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("fixture dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("fixture %s: %v", name, err)
		}
	}

	tests := []struct {
		name                     string
		fixture, sysRoot         string
		wantVendor, wantProduct  string
		wantVendorID, wantProdID string
	}{
		{
			// libvirt's names win when present.
			name: "libvirt's own names are kept", fixture: "gpu", sysRoot: sysfsRoot,
			wantVendor: "NVIDIA Corporation", wantProduct: "GP107 [GeForce GTX 1050 Ti]",
			wantVendorID: "10de", wantProdID: "1c82",
		},
		{
			// libvirt reports <vendor id='0x10de'/> with no text.
			name: "nameless devices are resolved from pci.ids", fixture: "gpu-unnamed", sysRoot: sysfsRoot,
			wantVendor: "NVIDIA Corporation", wantProduct: "GP107 [GeForce GTX 1050 Ti]",
			wantVendorID: "10de", wantProdID: "1c82",
		},
		{
			// No database is not an error; the IDs remain.
			name: "without a database the numeric IDs survive", fixture: "gpu-unnamed", sysRoot: noDB,
			wantVendor: "", wantProduct: "",
			wantVendorID: "10de", wantProdID: "1c82",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := hostdev.Build(fixtures(t, tt.fixture), tt.sysRoot)
			if len(rep.PCI) != 1 {
				t.Fatalf("len(PCI) = %d, want 1", len(rep.PCI))
			}
			got := rep.PCI[0]
			for _, f := range []struct{ field, got, want string }{
				{"Vendor", got.Vendor, tt.wantVendor},
				{"Product", got.Product, tt.wantProduct},
				{"VendorID", got.VendorID, tt.wantVendorID},
				{"ProductID", got.ProductID, tt.wantProdID},
			} {
				if f.got != f.want {
					t.Errorf("%s = %q, want %q", f.field, f.got, f.want)
				}
			}
		})
	}
}

// whyPlumbing is the refusal a chipset/CPU-internal function earns.
const whyPlumbing = "this is part of the host's own chipset and CPU plumbing, not a device a guest can use"

// TestBuild_CandidateByClass covers the candidate decision for the class
// codes a real Xeon E5 host reports.
func TestBuild_CandidateByClass(t *testing.T) {
	tests := []struct {
		name, class string
		wantUsable  bool
		wantReason  string
	}{
		{"VGA controller", "0x030000", true, ""},
		{"ethernet controller", "0x020000", true, ""},
		{"audio device", "0x040300", true, ""},
		{"USB controller", "0x0c0330", true, ""},
		{"PCI bridge", "0x060400", false, whyBridge},
		// blockedBy owns this one.
		{"host bridge", "0x060000", false, "this device is a Host bridge, which cannot be released by the host"},
		{"SATA controller", "0x010600", false, "this device is a SATA controller, which probably carries the host's own disks"},
		// 00:05.x, the ff: uncore functions, and the chipset SPSR.
		{"system peripheral", "0x088000", false, whyPlumbing},
		{"performance counters", "0x110100", false, whyPlumbing},
		{"unassigned class", "0xff0000", false, whyPlumbing},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := hostdev.Build([]string{deviceXML(tt.class)}, sysfsRoot)
			if len(rep.PCI) != 1 {
				t.Fatalf("len(PCI) = %d, want 1", len(rep.PCI))
			}
			got := rep.PCI[0]
			if got.Usable != tt.wantUsable {
				t.Errorf("Usable = %v, want %v (reason %q)", got.Usable, tt.wantUsable, got.Reason)
			}
			if got.Reason != tt.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tt.wantReason)
			}
		})
	}
}

// TestClassNaming guards that no class an ordinary host reports falls through
// to the raw-hex fallback.
func TestClassNaming(t *testing.T) {
	for class, want := range map[string]string{
		"0x088000": "System peripheral",
		"0x110100": "Signal processing controller",
		"0xff0000": "Unassigned class",
		"0x0c0330": "USB controller",
		"0x020000": "Ethernet controller",
	} {
		rep := hostdev.Build([]string{deviceXML(class)}, sysfsRoot)
		if got := rep.PCI[0].Class; got != want {
			t.Errorf("class %s = %q, want %q", class, got, want)
		}
	}
}

// deviceXML is a minimal node-device document with the given class, at an
// address absent from the sysfs fixture.
func deviceXML(class string) string {
	return `<device>
  <name>pci_0000_07_00_0</name>
  <capability type='pci'>
    <class>` + class + `</class>
    <domain>0</domain><bus>7</bus><slot>0</slot><function>0</function>
    <product id='0x0001'>Test Device</product>
    <vendor id='0x0002'>Test Vendor</vendor>
    <iommuGroup number='40'>
      <address domain='0x0000' bus='0x07' slot='0x00' function='0x0'/>
    </iommuGroup>
  </capability>
</device>`
}
