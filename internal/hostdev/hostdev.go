// Package hostdev builds the host's passthrough-candidate device inventory
// from libvirt node-device XML plus sysfs. Callers supply the XML, so the
// package makes no libvirt calls and can be tested against fixtures.
package hostdev

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Report is the whole inventory, and the JSON shape of GET /system/devices.
type Report struct {
	IOMMU IOMMUStatus `json:"iommu"`
	PCI   []PCIDevice `json:"pci"`
	USB   []USBDevice `json:"usb"`
}

// IOMMUStatus says whether this host can do PCI passthrough at all, and how
// to fix it when it cannot.
type IOMMUStatus struct {
	Enabled    bool   `json:"enabled"`
	GroupsSeen int    `json:"groups_seen"`
	Message    string `json:"message"`
	Remedy     string `json:"remedy,omitempty"`
}

// PCIDevice is one PCI function. Address, Vendor, Product, Class, Driver and
// IOMMUGroup come from libvirt; BootVGA, Siblings, Usable and Reason are
// resolved by Build.
type PCIDevice struct {
	Address string `json:"address"`
	Vendor  string `json:"vendor"`
	Product string `json:"product"`
	// VendorID and ProductID are the numeric IDs ("10de", "13ba") from sysfs.
	// libvirt leaves some devices unnamed, and then these are what tell two
	// identical-class cards apart.
	VendorID  string `json:"vendor_id,omitempty"`
	ProductID string `json:"product_id,omitempty"`
	Class     string `json:"class"`
	// Bridge marks a path to other devices rather than an endpoint. A bridge
	// stays behind when the rest of its group is passed through.
	Bridge bool `json:"bridge,omitempty"`
	// Driver is the host driver currently bound, "" when none is.
	Driver string `json:"driver"`
	// IOMMUGroup is -1 when libvirt reported no group, i.e. IOMMU is off.
	IOMMUGroup int `json:"iommu_group"`
	// BootVGA marks the card driving the host console.
	BootVGA bool `json:"boot_vga"`
	// Siblings are the other addresses in this device's IOMMU group, which
	// are passed through together or not at all.
	Siblings []string `json:"siblings"`
	Usable   bool     `json:"usable"`
	// Reason explains a false Usable and is empty otherwise.
	Reason string `json:"reason,omitempty"`
}

// USBDevice is one USB device. Its identity is VendorID:ProductID; BusSeen is
// informational, never an address to match on.
type USBDevice struct {
	VendorID    string `json:"vendor_id"`
	ProductID   string `json:"product_id"`
	Description string `json:"description"`
	BusSeen     string `json:"bus_seen"`
}

// iommuRemedy names both vendors' kernel arguments because the report cannot
// tell which CPU it is running on.
const iommuRemedy = "Add intel_iommu=on (Intel) or amd_iommu=on iommu=pt (AMD) to GRUB_CMDLINE_LINUX in /etc/default/grub, then run: sudo update-grub && sudo reboot"

// Build assembles the report from libvirt node-device XML plus sysfs rooted at
// sysRoot ("" means the real /). It cannot fail: an unreadable sysfs is
// reported as IOMMU off.
func Build(xmls []string, sysRoot string) Report {
	rep := Report{
		IOMMU: readIOMMU(sysRoot),
		PCI:   make([]PCIDevice, 0, len(xmls)),
		USB:   make([]USBDevice, 0),
	}

	devs := make([]Device, 0, len(xmls))
	blockers := make(map[string]string)
	for _, x := range xmls {
		d, err := ParseNodeDeviceXML(x)
		if err != nil {
			// One unreadable document must not blank the whole inventory.
			continue
		}
		devs = append(devs, d)
		if d.PCI != nil && d.Blocks != "" {
			blockers[d.PCI.Address] = d.PCI.Class + ", which " + d.Blocks
		}
	}

	for _, d := range devs {
		switch {
		case d.USB != nil:
			rep.USB = append(rep.USB, *d.USB)
		case d.PCI != nil:
			p := *d.PCI
			p.BootVGA = readBootVGA(sysRoot, p.Address)
			p.VendorID, p.ProductID = readPCIIDs(sysRoot, p.Address)
			p.Siblings = without(d.Group, p.Address)
			p.Usable, p.Reason = verdict(p, d.Self, blockers)
			rep.PCI = append(rep.PCI, p)
		}
	}

	nameUnnamed(rep.PCI, sysRoot)

	sort.Slice(rep.PCI, func(i, j int) bool { return rep.PCI[i].Address < rep.PCI[j].Address })
	sort.Slice(rep.USB, func(i, j int) bool { return usbKey(rep.USB[i]) < usbKey(rep.USB[j]) })
	return rep
}

// nameUnnamed fills in names libvirt did not report, from the host's pci.ids.
// Names libvirt did report are left alone.
func nameUnnamed(pci []PCIDevice, sysRoot string) {
	want := make([]*pciName, 0)
	at := make([]int, 0)
	for i, p := range pci {
		if (p.Vendor != "" && p.Product != "") || p.VendorID == "" {
			continue
		}
		want = append(want, &pciName{vendorID: p.VendorID, productID: p.ProductID})
		at = append(at, i)
	}

	pciNames(sysRoot, want)

	for k, n := range want {
		p := &pci[at[k]]
		if p.Vendor == "" {
			p.Vendor = n.vendor
		}
		if p.Product == "" {
			p.Product = n.product
		}
	}
}

// verdict decides whether p can be handed to a guest. One blocking member
// disqualifies the whole IOMMU group; a member libvirt never reported cannot
// be classified and does not block.
func verdict(p PCIDevice, self string, blockers map[string]string) (bool, string) {
	if b, ok := blockers[p.Address]; ok {
		return false, "this device is a " + b
	}
	if self != "" {
		return false, self
	}
	for _, s := range p.Siblings {
		if b, ok := blockers[s]; ok {
			return false, fmt.Sprintf("IOMMU group %d also contains %s (%s); a group is passed through as one unit",
				p.IOMMUGroup, s, b)
		}
	}
	return true, ""
}

// without returns addrs minus self. It is never nil, so the field marshals as
// [] rather than null.
func without(addrs []string, self string) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a != self {
			out = append(out, a)
		}
	}
	return out
}

func usbKey(u USBDevice) string { return u.VendorID + ":" + u.ProductID + " " + u.BusSeen }

// sysPath joins parts beneath sysRoot, where "" is the real filesystem root.
func sysPath(sysRoot string, parts ...string) string {
	if sysRoot == "" {
		sysRoot = "/"
	}
	return filepath.Join(append([]string{sysRoot}, parts...)...)
}

// readIOMMU counts the kernel's IOMMU groups. A missing or unreadable
// directory reports IOMMU as off.
func readIOMMU(sysRoot string) IOMMUStatus {
	entries, err := os.ReadDir(sysPath(sysRoot, "sys", "kernel", "iommu_groups"))
	if err != nil || len(entries) == 0 {
		return IOMMUStatus{
			Message: "IOMMU is off — PCI passthrough is unavailable on this host",
			Remedy:  iommuRemedy,
		}
	}
	return IOMMUStatus{
		Enabled:    true,
		GroupsSeen: len(entries),
		Message:    fmt.Sprintf("IOMMU is enabled; the kernel reports %d device groups", len(entries)),
	}
}

// readBootVGA reports whether address is the card driving the host console.
// The file exists only on display devices, so its absence is not an error.
func readBootVGA(sysRoot, address string) bool {
	b, err := os.ReadFile(sysPath(sysRoot, "sys", "bus", "pci", "devices", address, "boot_vga"))
	return err == nil && strings.TrimSpace(string(b)) == "1"
}
