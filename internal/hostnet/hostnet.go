package hostnet

import (
	"fmt"
	"strings"
)

// maxVLANID is the largest 802.1Q tag; 4095 is reserved by the standard.
const maxVLANID = 4094

// Ensure makes vid reachable on uplink and returns the bridge a guest should
// attach to, untagged. It is idempotent: each object is created only when
// absent, so a crash between the two adds recovers on the next call.
//
// It refuses rather than repairs. An object with one of our names but an
// unexpected shape is managed by something else, and repairing it would modify
// state this package did not create.
func (m *Manager) Ensure(uplink string, vid int) (string, error) {
	if vid < 1 || vid > maxVLANID {
		return "", fmt.Errorf("VLAN %d is not a valid 802.1Q tag — it must be 1-%d", vid, maxVLANID)
	}
	if strings.HasPrefix(uplink, bridgePrefix) {
		return "", fmt.Errorf("uplink %q cannot be a nano-cloud VLAN bridge — stacking a VLAN on a VLAN is not something this provisions", uplink)
	}
	if !m.ifaceExists(uplink) {
		return "", fmt.Errorf("this host has no interface named %q to put VLAN %d on", uplink, vid)
	}

	link, bridge := LinkName(uplink, vid), BridgeName(vid)
	if len(link) > maxIfaceName {
		return "", fmt.Errorf("the interface name %q is too long — the kernel allows %d characters, so VLAN %d cannot ride %q", link, maxIfaceName, vid, uplink)
	}

	// All checks run before any command, so a refusal leaves nothing half built.
	makeLink := !m.ifaceExists(link)
	if !makeLink {
		if parent, got, ok := m.vlanOf(link); !ok || parent != uplink || got != vid {
			return "", fmt.Errorf("%q already exists and is not a VLAN %d link on %s — refusing to touch it", link, vid, uplink)
		}
		// Other network managers use the same <uplink>.<vid> name, so an
		// existing link may belong to the operator's own bridge or carry the
		// management address. Adopting it would steal the port or drop L3.
		master, enslaved, masterErr := m.linkMaster(link)
		if masterErr != nil {
			return "", fmt.Errorf("could not read which bridge %q belongs to: %w — refusing to touch it", link, masterErr)
		}
		if enslaved && master != bridge {
			return "", fmt.Errorf("%q is already enslaved to %q, not %q — refusing to touch it", link, master, bridge)
		}
		linkAddrs, linkAddrErr := m.addrs(link)
		if linkAddrErr != nil {
			return "", fmt.Errorf("could not read %q's addresses: %w — refusing to touch it", link, linkAddrErr)
		}
		if a := hostAddress(linkAddrs); a != "" {
			return "", fmt.Errorf("%q holds address %s, so it is in use as a host interface — refusing to touch it", link, a)
		}
	}

	makeBridge := !m.ifaceExists(bridge)
	if !makeBridge && !m.isBridge(bridge) {
		return "", fmt.Errorf("%q already exists and is not a bridge — refusing to touch it", bridge)
	}
	// An unreadable bridge is refused, not treated as having no ports.
	ports, portsErr := m.bridgePorts(bridge)
	if portsErr != nil {
		return "", fmt.Errorf("could not read bridge %q's ports: %w — refusing to touch it", bridge, portsErr)
	}
	if !makeBridge {
		// libvirt adds a tap per running guest; any other port is foreign.
		for _, p := range ports {
			if p != link && !m.isTap(p) {
				return "", fmt.Errorf("bridge %q has the unexpected port %q — something else is managing it, so refusing to touch it", bridge, p)
			}
		}
		bridgeAddrs, bridgeAddrErr := m.addrs(bridge)
		if bridgeAddrErr != nil {
			return "", fmt.Errorf("could not read bridge %q's addresses: %w — refusing to touch it", bridge, bridgeAddrErr)
		}
		if a := hostAddress(bridgeAddrs); a != "" {
			return "", fmt.Errorf("bridge %q holds address %s, so it is in use as a host interface — refusing to touch it", bridge, a)
		}
	}

	enslave := true
	for _, p := range ports {
		if p == link {
			enslave = false
		}
	}

	// The host must not be reachable on a guest VLAN, so IPv6 goes off before
	// each link comes up, including on adoption.
	ipv6 := m.ipv6Present()

	if makeLink {
		if err := m.run("link", "add", "link", uplink, "name", link, "type", "vlan", "id", fmt.Sprint(vid)); err != nil {
			return "", err
		}
	}
	if ipv6 {
		if err := m.disableIPv6(link); err != nil {
			return "", err
		}
	}
	if err := m.run("link", "set", link, "up"); err != nil {
		return "", err
	}
	if makeBridge {
		if err := m.run("link", "add", "name", bridge, "type", "bridge"); err != nil {
			return "", err
		}
	}
	if ipv6 {
		if err := m.disableIPv6(bridge); err != nil {
			return "", err
		}
	}
	if enslave {
		if err := m.run("link", "set", link, "master", bridge); err != nil {
			return "", err
		}
	}
	if err := m.run("link", "set", bridge, "up"); err != nil {
		return "", err
	}
	return bridge, nil
}
