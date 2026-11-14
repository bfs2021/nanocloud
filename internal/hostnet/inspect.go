// Package hostnet provisions VLAN connectivity on the host, additively.
//
// It creates two objects per VLAN, a subinterface <uplink>.<vid> and a bridge
// nc-vlan<vid>, and modifies nothing it did not create: it never assigns an IP,
// enslaves a physical NIC or touches the uplink, so the operator's own
// connection is never at risk. The name is the record, so there is no state
// file.
//
// There is deliberately no Remove: an idle subinterface costs nothing, and
// not deleting rules out removing an interface that belongs to someone else.
package hostnet

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// bridgePrefix namespaces every bridge this package creates. A matching name
// alone does not make a bridge ours; Ensure also checks its shape.
const bridgePrefix = "nc-vlan"

// maxIfaceName is IFNAMSIZ-1. Longer names are refused rather than truncated,
// since two VLANs could truncate to the same name.
const maxIfaceName = 15

// BridgeName is the bridge a guest on vid attaches to, untagged.
func BridgeName(vid int) string { return fmt.Sprintf("%s%d", bridgePrefix, vid) }

// LinkName is the VLAN subinterface that does the tagging.
func LinkName(uplink string, vid int) string { return fmt.Sprintf("%s.%d", uplink, vid) }

// VLANLink is one provisioned VLAN as the host currently has it.
type VLANLink struct {
	Bridge string `json:"bridge"`
	Link   string `json:"link"`
	Uplink string `json:"uplink"`
	VID    int    `json:"vlan_id"`
}

// Manager reads and writes host VLAN state. root is "" for the real filesystem
// or a fixture tree in tests; run, addrs and disableIPv6 are test seams.
type Manager struct {
	root        string
	run         func(args ...string) error
	addrs       func(iface string) ([]string, error)
	disableIPv6 func(iface string) error
}

// New returns a Manager against the real host.
func New() *Manager {
	m := &Manager{
		run: func(args ...string) error {
			out, err := exec.Command("ip", args...).CombinedOutput()
			if err != nil {
				return fmt.Errorf("ip %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
			}
			return nil
		},
		addrs: func(iface string) ([]string, error) {
			i, err := net.InterfaceByName(iface)
			if err != nil {
				return nil, err
			}
			as, err := i.Addrs()
			if err != nil {
				return nil, err
			}
			out := make([]string, 0, len(as))
			for _, a := range as {
				out = append(out, a.String())
			}
			return out, nil
		},
	}
	m.disableIPv6 = m.writeDisableIPv6
	return m
}

// writeDisableIPv6 turns the host's own IPv6 off on one link; guests still get
// IPv6 through the bridge. addrgenmode none is not enough, because a router
// advertisement on the guest VLAN would still give the bridge a SLAAC address.
func (m *Manager) writeDisableIPv6(iface string) error {
	p := m.path("proc", "sys", "net", "ipv6", "conf", iface, "disable_ipv6")
	if err := os.WriteFile(p, []byte("1\n"), 0o644); err != nil {
		return fmt.Errorf("turn off host IPv6 on %q: %w", iface, err)
	}
	return nil
}

// ipv6Present reports whether the kernel has IPv6 at all (not ipv6.disable=1).
func (m *Manager) ipv6Present() bool {
	_, err := os.Stat(m.path("proc", "sys", "net", "ipv6", "conf"))
	return err == nil
}

// hostAddress returns the first configured address in addrs, skipping
// link-local ones the kernel assigns to any link that comes up. An address
// that does not parse counts as configured.
func hostAddress(addrs []string) string {
	for _, a := range addrs {
		p, err := netip.ParsePrefix(a)
		if err == nil && p.Addr().IsLinkLocalUnicast() {
			continue
		}
		return a
	}
	return ""
}

// path joins beneath the manager's root, where "" is the real filesystem root.
func (m *Manager) path(parts ...string) string {
	root := m.root
	if root == "" {
		root = "/"
	}
	return filepath.Join(append([]string{root}, parts...)...)
}

func (m *Manager) ifaceExists(name string) bool {
	_, err := os.Stat(m.path("sys", "class", "net", name))
	return err == nil
}

func (m *Manager) isBridge(name string) bool {
	_, err := os.Stat(m.path("sys", "class", "net", name, "bridge"))
	return err == nil
}

// isTap reports a tun/tap device, the kind libvirt attaches a guest NIC with.
func (m *Manager) isTap(name string) bool {
	_, err := os.Stat(m.path("sys", "class", "net", name, "tun_flags"))
	return err == nil
}

// bridgePorts lists a bridge's enslaved interfaces. A missing brif directory
// means zero ports; any other read failure is returned so the caller refuses
// instead of adopting a bridge it could not inspect.
func (m *Manager) bridgePorts(name string) ([]string, error) {
	entries, err := os.ReadDir(m.path("sys", "class", "net", name, "brif"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out, nil
}

// linkMaster returns the bridge name is enslaved to, from its sysfs master
// symlink; ok is false when it has none. Only a missing symlink means no
// master. Other errors are returned so the caller fails closed.
func (m *Manager) linkMaster(name string) (master string, ok bool, err error) {
	target, err := os.Readlink(m.path("sys", "class", "net", name, "master"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	return filepath.Base(target), true, nil
}

// vlanOf reads a subinterface's parent and tag from procfs. A non-VLAN
// interface has no such file.
func (m *Manager) vlanOf(name string) (parent string, vid int, ok bool) {
	f, err := os.Open(m.path("proc", "net", "vlan", name))
	if err != nil {
		return "", 0, false
	}
	defer f.Close() //nolint:errcheck // read-only proc file; close error is not actionable

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "VID:"); i >= 0 && vid == 0 {
			fields := strings.Fields(line[i+len("VID:"):])
			if len(fields) > 0 {
				vid, _ = strconv.Atoi(fields[0])
			}
		}
		if strings.HasPrefix(line, "Device:") {
			parent = strings.TrimSpace(strings.TrimPrefix(line, "Device:"))
		}
	}
	return parent, vid, parent != "" && vid != 0
}

// List reports the VLANs this package has provisioned, identified by the
// nc-vlan<n> bridge name.
func (m *Manager) List() ([]VLANLink, error) {
	entries, err := os.ReadDir(m.path("sys", "class", "net"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []VLANLink{}, nil
		}
		return nil, fmt.Errorf("read interfaces: %w", err)
	}

	out := []VLANLink{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, bridgePrefix) || !m.isBridge(name) {
			continue
		}
		vid, atoiErr := strconv.Atoi(strings.TrimPrefix(name, bridgePrefix))
		if atoiErr != nil {
			continue
		}
		// List is best-effort: an unreadable bridge is skipped.
		ports, portsErr := m.bridgePorts(name)
		if portsErr != nil {
			continue
		}
		for _, port := range ports {
			parent, pvid, ok := m.vlanOf(port)
			if ok && pvid == vid {
				out = append(out, VLANLink{Bridge: name, Link: port, Uplink: parent, VID: vid})
			}
		}
	}
	return out, nil
}

// DefaultRouteInterface returns the interface carrying the default route,
// used as the VLAN uplink. A host with no default route is an error rather
// than a guess.
func (m *Manager) DefaultRouteInterface() (string, error) {
	f, err := os.Open(m.path("proc", "net", "route"))
	if err != nil {
		return "", fmt.Errorf("read routes: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only proc file; close error is not actionable

	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[1] == "00000000" {
			return fields[0], nil
		}
	}
	return "", errors.New("this host has no default route, so there is no interface to put a VLAN on")
}
