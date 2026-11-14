package hostdev

import (
	"encoding/xml"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Device is one parsed libvirt node-device document. At most one of PCI and
// USB is set; both are nil for capability types the inventory ignores.
type Device struct {
	Name string
	PCI  *PCIDevice
	USB  *USBDevice

	// Group is every address in this device's <iommuGroup>, its own included.
	// Empty when IOMMU is off.
	Group []string

	// Blocks explains why this device disqualifies its whole IOMMU group, or
	// is empty when it disqualifies nothing.
	Blocks string

	// Self explains why this device is not a candidate itself, or is empty.
	// A bridge sets Self but not Blocks, so the card behind it stays usable.
	Self string
}

// nodeDeviceXML mirrors libvirt's <device> document.
type nodeDeviceXML struct {
	Name       string          `xml:"name"`
	DriverName string          `xml:"driver>name"`
	Caps       []capabilityXML `xml:"capability"`
}

// capabilityXML is a <capability> element. It nests inside itself: a bridge
// carries an empty <capability type='pci-bridge'/> within its pci capability.
type capabilityXML struct {
	Type string `xml:"type,attr"`
	// Class is the PCI class code as a hex string, e.g. "0x030000".
	Class string `xml:"class"`
	// Domain..Function are decimal here, unlike the hex attributes in
	// <iommuGroup>.
	Domain     int             `xml:"domain"`
	Bus        int             `xml:"bus"`
	Slot       int             `xml:"slot"`
	Function   int             `xml:"function"`
	Device     int             `xml:"device"`
	Product    idNameXML       `xml:"product"`
	Vendor     idNameXML       `xml:"vendor"`
	IOMMUGroup *iommuGroupXML  `xml:"iommuGroup"`
	Nested     []capabilityXML `xml:"capability"`
}

type idNameXML struct {
	ID   string `xml:"id,attr"`
	Name string `xml:",chardata"`
}

type iommuGroupXML struct {
	Number    int          `xml:"number,attr"`
	Addresses []addressXML `xml:"address"`
}

type addressXML struct {
	Domain   string `xml:"domain,attr"`
	Bus      string `xml:"bus,attr"`
	Slot     string `xml:"slot,attr"`
	Function string `xml:"function,attr"`
}

// ParseNodeDeviceXML parses one libvirt <device> document. The group verdict
// and sysfs-derived fields are left to Build.
func ParseNodeDeviceXML(xmlStr string) (Device, error) {
	var nd nodeDeviceXML
	if err := xml.Unmarshal([]byte(xmlStr), &nd); err != nil {
		return Device{}, fmt.Errorf("hostdev: parse node device XML: %w", err)
	}

	d := Device{Name: nd.Name, Group: make([]string, 0)}
	for _, c := range nd.Caps {
		switch c.Type {
		case "pci":
			d.PCI = &PCIDevice{
				Address:    pciAddress(c.Domain, c.Bus, c.Slot, c.Function),
				Vendor:     strings.TrimSpace(c.Vendor.Name),
				Product:    strings.TrimSpace(c.Product.Name),
				Class:      pciClass(c),
				Driver:     nd.DriverName,
				IOMMUGroup: -1,
			}
			if c.IOMMUGroup != nil {
				d.PCI.IOMMUGroup = c.IOMMUGroup.Number
				d.Group = groupAddresses(*c.IOMMUGroup)
			}
			d.Blocks = blockedBy(c)
			d.Self = notACandidate(c)
			d.PCI.Bridge = isBridge(c)
		case "usb_device":
			// Identity is vendor:product. Bus and device numbers change on
			// replug, so they are for display only.
			d.USB = &USBDevice{
				VendorID:    c.Vendor.ID,
				ProductID:   c.Product.ID,
				Description: strings.TrimSpace(c.Vendor.Name + " " + c.Product.Name),
				BusSeen:     fmt.Sprintf("bus %d device %d", c.Bus, c.Device),
			}
		}
	}
	return d, nil
}

func pciAddress(domain, bus, slot, function int) string {
	return fmt.Sprintf("%04x:%02x:%02x.%x", domain, bus, slot, function)
}

// groupAddresses renders an <iommuGroup>'s members in PCIDevice.Address form,
// sorted. A member whose attributes do not parse is dropped.
func groupAddresses(g iommuGroupXML) []string {
	out := make([]string, 0, len(g.Addresses))
	for _, a := range g.Addresses {
		dom, ok1 := parseHex(a.Domain)
		bus, ok2 := parseHex(a.Bus)
		slot, ok3 := parseHex(a.Slot)
		fn, ok4 := parseHex(a.Function)
		if !ok1 || !ok2 || !ok3 || !ok4 {
			continue
		}
		out = append(out, pciAddress(int(dom), int(bus), int(slot), int(fn)))
	}
	sort.Strings(out)
	return out
}

func parseHex(s string) (uint64, bool) {
	v, err := strconv.ParseUint(strings.TrimSpace(s), 0, 32)
	return v, err == nil
}

const (
	// PCI base classes; subclassHostBridge is base+subclass.
	classStorage    = 0x01
	classBridge     = 0x06
	classSystem     = 0x08
	classSignal     = 0x11
	classUnassigned = 0xff

	subclassHostBridge = 0x0600
)

// Reasons a device disqualifies its group, composed by Build into
// "<class>, which <reason>".
const (
	whyHostBridge = "cannot be released by the host"
	whyStorage    = "probably carries the host's own disks"
)

const (
	whyBridge = "this is a bridge the host uses to reach the devices behind it, not an endpoint — pass through the device on the far side of it instead"

	whyPlumbing = "this is part of the host's own chipset and CPU plumbing, not a device a guest can use"
)

// isBridge reports whether c is a bridge of any kind, host bridges included,
// falling back to the nested pci-bridge capability when there is no class.
func isBridge(c capabilityXML) bool {
	if code, ok := parseHex(c.Class); ok {
		return code>>16 == classBridge
	}
	for _, n := range c.Nested {
		if n.Type == "pci-bridge" {
			return true
		}
	}
	return false
}

// notACandidate explains why this device cannot be passed through on its own,
// or returns "" when it can. Host bridges are left to blockedBy. Other bridges
// are excluded here rather than blocking their group, which would refuse every
// GPU behind a root port.
//
// Classes 0x08, 0x11 and 0xff are chipset and CPU-internal functions (uncore
// monitoring, the VT-d unit itself) and make up most of a server's PCI list.
// Class 0x08 also covers SD host controllers (0x0805), which could be allowed
// if someone needs one.
func notACandidate(c capabilityXML) string {
	code, ok := parseHex(c.Class)
	if ok && code>>8 == subclassHostBridge {
		return ""
	}
	if isBridge(c) {
		return whyBridge
	}
	if ok {
		switch code >> 16 {
		case classSystem, classSignal, classUnassigned:
			return whyPlumbing
		}
	}
	return ""
}

// pciClass names the device's class. <class> is optional in libvirt's schema,
// so a bridge may be named only by its nested capability.
func pciClass(c capabilityXML) string {
	if name := className(c.Class); name != "" {
		return name
	}
	for _, n := range c.Nested {
		if n.Type == "pci-bridge" {
			return "PCI bridge"
		}
	}
	return ""
}

// blockedBy explains why this device disqualifies its whole IOMMU group, or
// returns "" when it disqualifies nothing.
//
// Only host bridges block, not other bridges: vfio's group-viability check
// exempts PCI-to-PCI and CardBus bridges (header type 1 or 2), which stay on
// pcieport while the endpoint behind them is passed through.
func blockedBy(c capabilityXML) string {
	code, ok := parseHex(c.Class)
	if !ok {
		return ""
	}
	if code>>8 == subclassHostBridge {
		return whyHostBridge
	}
	if code>>16 == classStorage {
		// A risk, not a certainty: a spare NVMe looks the same here as the
		// one holding the root filesystem.
		return whyStorage
	}
	return ""
}

// pciSubclassNames and pciBaseClassNames name a class code at two widths
// (0x0300 is VGA, 0x03 any display controller). A subclass match wins.
var pciSubclassNames = map[uint64]string{
	0x0106: "SATA controller",
	0x0108: "NVMe controller",
	0x0200: "Ethernet controller",
	0x0300: "VGA compatible controller",
	0x0403: "Audio device",
	0x0600: "Host bridge",
	0x0604: "PCI bridge",
	0x0805: "SD host controller",
	0x0806: "IOMMU",
	0x0880: "System peripheral",
	0x0c03: "USB controller",
}

var pciBaseClassNames = map[uint64]string{
	0x01: "Mass storage controller",
	0x02: "Network controller",
	0x03: "Display controller",
	0x04: "Multimedia controller",
	0x06: "Bridge",
	0x08: "System peripheral",
	0x0c: "Serial bus controller",
	0x11: "Signal processing controller",
	0xff: "Unassigned class",
}

// className turns libvirt's <class> code into a readable label.
func className(code string) string {
	v, ok := parseHex(code)
	if !ok {
		return ""
	}
	if n, ok := pciSubclassNames[v>>8]; ok {
		return n
	}
	if n, ok := pciBaseClassNames[v>>16]; ok {
		return n
	}
	return fmt.Sprintf("PCI class 0x%06x", v)
}
