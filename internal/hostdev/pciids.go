package hostdev

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// pciIDsPaths are where distros install the PCI ID database. Debian and Ubuntu
// ship it with pciutils at /usr/share/misc, the RHEL family with hwdata.
var pciIDsPaths = []string{
	"usr/share/hwdata/pci.ids",
	"usr/share/misc/pci.ids",
	"usr/share/pci.ids",
}

// readPCIIDs reads a device's numeric IDs from sysfs without the "0x" prefix.
// Either is empty when unreadable, which is normal off Linux.
func readPCIIDs(sysRoot, address string) (vendor, product string) {
	read := func(file string) string {
		b, err := os.ReadFile(sysPath(sysRoot, "sys", "bus", "pci", "devices", address, file))
		if err != nil {
			return ""
		}
		return strings.TrimPrefix(strings.TrimSpace(string(b)), "0x")
	}
	return read("vendor"), read("device")
}

// pciNames resolves numeric IDs to names from the host's pci.ids in one pass,
// filling want in place and leaving unlisted pairs as found.
func pciNames(sysRoot string, want []*pciName) {
	if len(want) == 0 {
		return
	}
	f, err := openPCIIDs(sysRoot)
	if err != nil {
		// A minimal host may not ship the database; callers fall back to the
		// numeric IDs.
		return
	}
	defer f.Close() //nolint:errcheck // read-only database; close error is not actionable

	byVendor := make(map[string][]*pciName, len(want))
	for _, n := range want {
		byVendor[n.vendorID] = append(byVendor[n.vendorID], n)
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var pending []*pciName
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// The class section uses the same indentation as vendors, so stop
		// before class codes are read as vendor IDs.
		if strings.HasPrefix(line, "C ") {
			break
		}

		switch {
		case !strings.HasPrefix(line, "\t"):
			id, name, ok := splitIDName(line)
			if !ok {
				continue
			}
			pending = byVendor[id]
			for _, n := range pending {
				n.vendor = name
			}
		case !strings.HasPrefix(line, "\t\t") && len(pending) > 0:
			id, name, ok := splitIDName(strings.TrimPrefix(line, "\t"))
			if !ok {
				continue
			}
			for _, n := range pending {
				if n.productID == id {
					n.product = name
				}
			}
		}
		// Two-tab lines are subsystem entries; the inventory does not use them.
	}
}

// pciName holds one device's IDs and the names resolved for them.
type pciName struct {
	vendorID, productID string
	vendor, product     string
}

func openPCIIDs(sysRoot string) (*os.File, error) {
	for _, p := range pciIDsPaths {
		if f, err := os.Open(sysPath(sysRoot, p)); err == nil {
			return f, nil
		}
	}
	return nil, fmt.Errorf("hostdev: no pci.ids found in %v", pciIDsPaths)
}

// splitIDName parses one "<hex id>  <name>" line.
func splitIDName(line string) (id, name string, ok bool) {
	id, name, found := strings.Cut(strings.TrimRight(line, "\r"), " ")
	if !found {
		return "", "", false
	}
	name = strings.TrimSpace(name)
	if id == "" || name == "" {
		return "", "", false
	}
	return strings.ToLower(id), name, true
}
