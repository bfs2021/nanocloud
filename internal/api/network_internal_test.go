package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/network"
)

// hostBridges is what a real host reports: bridges with a physical port. Only
// the untagged path checks against it.
var hostBridges = []network.Bridge{
	{Name: "br0", Uplink: "eth0"},
	{Name: "br-flat", Uplink: "eth1"},
}

func TestValidateNetwork(t *testing.T) {
	for _, tc := range []struct {
		name       string
		entry      metadata.VMEntry
		bridges    []network.Bridge
		wantStatus int
		wantIn     string
	}{
		{name: "nat", entry: metadata.VMEntry{NetworkMode: "nat"}, bridges: hostBridges},
		{name: "legacy entry with no mode", entry: metadata.VMEntry{}, bridges: hostBridges},
		{name: "bridge, untagged", entry: metadata.VMEntry{NetworkMode: "bridge"}, bridges: hostBridges},
		{
			name:       "a tag on NAT",
			entry:      metadata.VMEntry{NetworkMode: "nat", VLANID: 20},
			bridges:    hostBridges,
			wantStatus: http.StatusBadRequest, wantIn: "invisible",
		},
		{
			name:       "an invented mode",
			entry:      metadata.VMEntry{NetworkMode: "macvtap"},
			bridges:    hostBridges,
			wantStatus: http.StatusBadRequest, wantIn: "macvtap",
		},
		{
			name:       "a tag outside 802.1Q",
			entry:      metadata.VMEntry{NetworkMode: "bridge", VLANID: 5000},
			bridges:    hostBridges,
			wantStatus: http.StatusBadRequest, wantIn: "5000",
		},
		{
			name:       "a bridge this host does not have",
			entry:      metadata.VMEntry{NetworkMode: "bridge", Bridge: "vmbr9"},
			bridges:    hostBridges,
			wantStatus: http.StatusUnprocessableEntity, wantIn: "vmbr9",
		},
		{
			// nil is "the host could not be read", and must not turn every
			// bridge VM into a validation failure.
			name:  "an unreadable host skips the host half",
			entry: metadata.VMEntry{NetworkMode: "bridge", Bridge: "br-flat", VLANID: 20},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, err := validateNetwork(tc.entry, tc.bridges)
			if status != tc.wantStatus {
				t.Fatalf("status = %d (%v), want %d", status, err, tc.wantStatus)
			}
			if tc.wantIn != "" && !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("refusal does not say %q: %v", tc.wantIn, err)
			}
		})
	}
}

// nc-vlan20 is created at Ensure time, so requiring it in the host's bridge
// list would refuse every first VM on a new VLAN.
func TestValidateNetwork_ProvisionedVLANDoesNotNeedTheBridgeToExistYet(t *testing.T) {
	e := metadata.VMEntry{
		NetworkMode: metadata.NetworkBridge,
		Bridge:      "nc-vlan20", VLANID: 20, VLANUplink: "eth0",
	}
	bridges := []network.Bridge{{Name: "br0", Uplink: "eth0"}}
	if status, err := validateNetwork(e, bridges); err != nil {
		t.Fatalf("validateNetwork = %d, %v; want accepted", status, err)
	}
}

// A VLAN on a bridge without vlan_filtering is accepted: the flag is not read.
func TestValidateNetwork_NeverMentionsVLANFiltering(t *testing.T) {
	e := metadata.VMEntry{
		NetworkMode: metadata.NetworkBridge,
		Bridge:      "br0", VLANID: 20, VLANUplink: "eth0",
	}
	bridges := []network.Bridge{{Name: "br0", Uplink: "eth0", VLANFiltering: false}}
	status, err := validateNetwork(e, bridges)
	if err != nil {
		t.Fatalf("validateNetwork = %d, %v; want accepted", status, err)
	}
}

func TestValidateNetwork_UntaggedBridgeMustBeOneTheHostHas(t *testing.T) {
	e := metadata.VMEntry{NetworkMode: metadata.NetworkBridge, Bridge: "nosuchbr"}
	bridges := []network.Bridge{{Name: "br0", Uplink: "eth0"}}
	status, err := validateNetwork(e, bridges)
	if err == nil || status != http.StatusUnprocessableEntity {
		t.Fatalf("validateNetwork = %d, %v; want 422", status, err)
	}
}

// The server fills the uplink in before validating. An entry arriving
// tagged-but-unprovisioned from a client is a request we cannot satisfy.
func TestValidateNetwork_TagWithoutUplinkIsRejected(t *testing.T) {
	e := metadata.VMEntry{NetworkMode: metadata.NetworkBridge, Bridge: "br0", VLANID: 20}
	bridges := []network.Bridge{{Name: "br0", Uplink: "eth0"}}
	status, err := validateNetwork(e, bridges)
	if err == nil || status != http.StatusUnprocessableEntity {
		t.Fatalf("validateNetwork = %d, %v; want 422", status, err)
	}
}
