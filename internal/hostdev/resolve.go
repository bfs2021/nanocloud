package hostdev

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Selection names the devices a caller wants attached to a guest, in the form
// the API receives them: PCI by address, USB by "vendor:product".
type Selection struct {
	PCI []string
	USB []string
}

// Resolved is a selection checked against the host and expanded with each
// device's IOMMU group siblings, so picking a GPU also attaches its audio
// function.
type Resolved struct {
	PCI []PCIDevice
	USB []USBDevice
	// Warnings are things the user should see but that do not stop the attach.
	Warnings []string
}

// ErrUnavailable means the host cannot satisfy the selection. Every refusal
// carries a reason in its message.
var ErrUnavailable = errors.New("hostdev: device unavailable")

// Resolve checks a selection against the inventory and expands it. It refuses
// rather than silently dropping a device the user picked.
func Resolve(r Report, sel Selection) (Resolved, error) {
	out := Resolved{PCI: []PCIDevice{}, USB: []USBDevice{}, Warnings: []string{}}

	if len(sel.PCI) > 0 && !r.IOMMU.Enabled {
		return out, fmt.Errorf("%w: %s. %s", ErrUnavailable, r.IOMMU.Message, r.IOMMU.Remedy)
	}

	byAddr := make(map[string]PCIDevice, len(r.PCI))
	for _, d := range r.PCI {
		byAddr[d.Address] = d
	}

	// Keyed by address so two picks from one group yield one entry each.
	picked := make(map[string]PCIDevice)
	for _, want := range sel.PCI {
		addr := strings.ToLower(strings.TrimSpace(want))
		d, ok := byAddr[addr]
		if !ok {
			return out, fmt.Errorf("%w: this host has no PCI device at %s", ErrUnavailable, addr)
		}
		if !d.Usable {
			return out, fmt.Errorf("%w: %s cannot be passed through — %s", ErrUnavailable, addr, d.Reason)
		}
		if d.BootVGA {
			out.Warnings = append(out.Warnings, fmt.Sprintf(
				"%s (%s) is the card driving this host's console. Passing it to a guest takes the console with it: the host display goes dark until the VM stops.",
				addr, describePCI(d)))
		}

		picked[addr] = d
		// The group moves as a unit, so siblings are added automatically.
		for _, s := range d.Siblings {
			sib, known := byAddr[s]
			if !known {
				out.Warnings = append(out.Warnings, fmt.Sprintf(
					"%s shares IOMMU group %d with %s, which libvirt did not report as a device. The guest may fail to start until that device is accounted for.",
					addr, d.IOMMUGroup, s))
				continue
			}
			// A bridge stays bound to pcieport (vfio exempts it from the
			// viability check); naming it as a hostdev would fail the start.
			if sib.Bridge {
				continue
			}
			picked[s] = sib
		}
	}

	for _, d := range picked {
		out.PCI = append(out.PCI, d)
	}
	sort.Slice(out.PCI, func(i, j int) bool { return out.PCI[i].Address < out.PCI[j].Address })

	byID := make(map[string]USBDevice, len(r.USB))
	for _, d := range r.USB {
		byID[strings.ToLower(d.VendorID+":"+d.ProductID)] = d
	}
	seen := make(map[string]bool, len(sel.USB))
	for _, want := range sel.USB {
		id := strings.ToLower(strings.TrimSpace(want))
		d, ok := byID[id]
		if !ok {
			return out, fmt.Errorf("%w: no USB device %s is plugged into this host", ErrUnavailable, id)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out.USB = append(out.USB, d)
	}

	sort.Strings(out.Warnings)
	return out, nil
}

// describePCI is the human name for a device. Without a resolved name it
// prefers the numeric IDs over the class, since they tell two cards apart.
func describePCI(d PCIDevice) string {
	parts := make([]string, 0, 2)
	for _, s := range []string{d.Vendor, d.Product} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, " ")
	}
	if d.VendorID != "" && d.ProductID != "" {
		if d.Class != "" {
			return fmt.Sprintf("%s [%s:%s]", d.Class, d.VendorID, d.ProductID)
		}
		return fmt.Sprintf("[%s:%s]", d.VendorID, d.ProductID)
	}
	if d.Class != "" {
		return d.Class
	}
	return "unknown device"
}
