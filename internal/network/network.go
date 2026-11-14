// Package network inspects host network interfaces and bridges and reads DHCP
// leases.
package network

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultLeasesPath is the production path to the libvirt dnsmasq leases file
// written for the default NAT network (virbr0).
const DefaultLeasesPath = "/var/lib/libvirt/dnsmasq/virbr0.status"

// Bridge is one Linux bridge on the host, as sysfs describes it. VLANFiltering
// is informational only: nano-cloud never writes it, since flipping it on a
// bridge carrying the host's LAN can drop the operator's session.
type Bridge struct {
	Name          string `json:"name"`
	VLANFiltering bool   `json:"vlan_filtering"`
	// Uplink is the bridge's first physical NIC port, or empty when it has
	// none. It separates a bridge that reaches the LAN from a host-local one
	// such as virbr0 or docker0.
	Uplink string `json:"uplink"`
}

// ListBridges returns the host's Linux bridges. sysRoot is "" for the real
// filesystem or a fixture tree in tests; a host without sysfs reports none.
// A bridge is any interface with a /sys/class/net/<name>/bridge directory,
// whatever its name.
func ListBridges(sysRoot string) ([]Bridge, error) {
	if sysRoot == "" {
		sysRoot = "/"
	}
	root := filepath.Join(sysRoot, "sys", "class", "net")
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Bridge{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", root, err)
	}

	out := []Bridge{}
	for _, e := range entries {
		dir := filepath.Join(root, e.Name(), "bridge")
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, "vlan_filtering"))
		if err != nil {
			// A kernel without the file cannot do VLAN filtering.
			b = nil
		}
		out = append(out, Bridge{
			Name:          e.Name(),
			VLANFiltering: strings.TrimSpace(string(b)) == "1",
			Uplink:        physicalPort(root, e.Name()),
		})
	}
	return out, nil
}

// physicalPort returns the bridge's first port backed by hardware, meaning one
// with a device/ entry; virtual ports such as VLAN subinterfaces and veths
// have none.
func physicalPort(root, bridge string) string {
	ports, err := os.ReadDir(filepath.Join(root, bridge, "brif"))
	if err != nil {
		return ""
	}
	for _, p := range ports {
		if _, err := os.Stat(filepath.Join(root, p.Name(), "device")); err == nil {
			return p.Name()
		}
	}
	return ""
}

// WithUplink keeps only the bridges a VM can reach the network through. The
// result is never nil for a non-nil input, because callers read nil as "the
// host could not be enumerated".
func WithUplink(in []Bridge) []Bridge {
	if in == nil {
		return nil
	}
	out := make([]Bridge, 0, len(in))
	for _, b := range in {
		if b.Uplink != "" {
			out = append(out, b)
		}
	}
	return out
}

// HostInterface describes a single host network interface. Whether it is a
// bridge is answered by ListBridges, not here.
type HostInterface struct {
	// Name is the interface name (e.g. "eth0", "lo", "virbr0").
	Name string `json:"name"`
	// Flags is a pipe-separated list of active flags, e.g. "up|broadcast".
	Flags string `json:"flags"`
	// Addrs is the list of CIDR addresses assigned to the interface.
	Addrs []string `json:"addrs"`
	// Available is true when the interface is up.
	Available bool `json:"available"`
}

// ListInterfaces returns all host network interfaces with their addresses.
func ListInterfaces() ([]HostInterface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	out := make([]HostInterface, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			addrs = nil
		}

		cidrAddrs := make([]string, 0, len(addrs))
		for _, a := range addrs {
			cidrAddrs = append(cidrAddrs, a.String())
		}

		out = append(out, HostInterface{
			Name:      iface.Name,
			Flags:     formatFlags(iface.Flags),
			Addrs:     cidrAddrs,
			Available: iface.Flags&net.FlagUp != 0,
		})
	}
	return out, nil
}

func formatFlags(f net.Flags) string {
	var parts []string
	for _, pair := range []struct {
		flag net.Flags
		name string
	}{
		{net.FlagUp, "up"},
		{net.FlagBroadcast, "broadcast"},
		{net.FlagLoopback, "loopback"},
		{net.FlagPointToPoint, "pointtopoint"},
		{net.FlagMulticast, "multicast"},
	} {
		if f&pair.flag != 0 {
			parts = append(parts, pair.name)
		}
	}
	return strings.Join(parts, "|")
}

// DHCPLease holds a single DHCP lease entry from the libvirt dnsmasq status
// file for the default NAT network.
type DHCPLease struct {
	// IPAddress is the IPv4 address assigned to the VM.
	IPAddress string `json:"ip"`
	// MACAddress is the hardware address of the VM's virtual NIC.
	MACAddress string `json:"mac"`
	// Hostname is the hostname the VM sent with its DHCP request, which for
	// nano-cloud VMs matches the VM name.
	Hostname string `json:"vm_name"`
	// ExpiryTime is the Unix timestamp at which the lease expires.
	ExpiryTime int64 `json:"expiry_time"`
}

// rawLease mirrors the JSON shape libvirt's dnsmasq writes to virbr0.status.
type rawLease struct {
	IPAddress  string `json:"ip-address"`
	MACAddress string `json:"mac-address"`
	Hostname   string `json:"hostname"`
	ExpiryTime int64  `json:"expiry-time"`
}

// ReadLeases parses the dnsmasq status file at path and returns unexpired
// leases. A missing file means no leases yet and is not an error.
func ReadLeases(path string) ([]DHCPLease, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []DHCPLease{}, nil
		}
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only file; close error is not actionable

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	if len(strings.TrimSpace(string(data))) == 0 {
		return []DHCPLease{}, nil
	}

	var raw []rawLease
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	// dnsmasq leaves expired entries in the file until it next rewrites it.
	now := time.Now().Unix()
	leases := make([]DHCPLease, 0, len(raw))
	for _, r := range raw {
		if r.ExpiryTime > 0 && r.ExpiryTime <= now {
			continue
		}
		leases = append(leases, DHCPLease(r))
	}
	return leases, nil
}

// Manager wraps bridge detection, interface listing, and lease reading behind
// a single injectable dependency.
type Manager struct {
	leasesPath string
	// leases, when set, replaces the dnsmasq file as the lease source.
	leases LeaseSource
}

// New creates a Manager that reads dnsmasq DHCP leases from leasesPath.
func New(leasesPath string) *Manager {
	return &Manager{leasesPath: leasesPath}
}

// Bridges returns the host's Linux bridges and their VLAN capability.
func (m *Manager) Bridges() ([]Bridge, error) {
	return ListBridges("")
}

// Interfaces returns all host network interfaces with their addresses.
func (m *Manager) Interfaces() ([]HostInterface, error) {
	return ListInterfaces()
}

// Leases returns the active DHCP leases.
func (m *Manager) Leases() ([]DHCPLease, error) {
	if m.leases != nil {
		return m.leases.Read()
	}
	return ReadLeases(m.leasesPath)
}
