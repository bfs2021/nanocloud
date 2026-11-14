package vm

import (
	"encoding/xml"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

// liveNATInterface is a running type='network' interface. libvirt adds the
// bridge it attached the port to, which must not be read as drift.
const liveNATInterface = `<domain type='kvm'><devices>
  <interface type='network'>
    <mac address='52:54:00:aa:bb:cc'/>
    <source network='default' portid='ff01c1de-3a1e-4f2a-9d64-1f0e2b3c4d5e' bridge='virbr0'/>
    <target dev='vnet3'/>
    <model type='virtio'/>
    <alias name='net0'/>
  </interface>
</devices></domain>`

const liveTaggedBridgeInterface = `<domain type='kvm'><devices>
  <interface type='bridge'>
    <mac address='52:54:00:aa:bb:cc'/>
    <source bridge='br-lan'/>
    <vlan><tag id='20'/></vlan>
    <target dev='vnet4'/>
    <model type='virtio'/>
    <alias name='net0'/>
  </interface>
</devices></domain>`

func TestParseDomainNetworkShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		want NetworkShape
	}{
		{"running NAT keeps no bridge", liveNATInterface, NetworkShape{Mode: metadata.NetworkNAT}},
		{"running tagged bridge", liveTaggedBridgeInterface, NetworkShape{Mode: metadata.NetworkBridge, Bridge: "br-lan", VLANTag: 20}},
		{"no interface at all", `<domain type='kvm'><devices></devices></domain>`, NetworkShape{Mode: metadata.NetworkNAT}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDomainNetworkShape(tc.doc)
			if err != nil {
				t.Fatalf("ParseDomainNetworkShape: %v", err)
			}
			if got != tc.want {
				t.Errorf("= %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestNetworkShapeSurvivesARoundTrip: every entry, legacy ones included, must
// read as undrifted against the XML it renders, or an upgrade alone lights the
// restart badge.
func TestNetworkShapeSurvivesARoundTrip(t *testing.T) {
	for _, e := range []metadata.VMEntry{
		{ID: "nat", NetworkMode: "nat"},
		{ID: "legacy-bridge", NetworkMode: "bridge"}, // no Bridge recorded: br0
		{ID: "tagged", NetworkMode: "bridge", Bridge: "br-lan", VLANID: 20},
		// Provisioned: no <vlan> tag, since the host subinterface already tags.
		{ID: "provisioned-vlan", NetworkMode: "bridge", Bridge: "nc-vlan20", VLANID: 20, VLANUplink: "eth0"},
	} {
		t.Run(e.ID, func(t *testing.T) {
			doc, err := xml.Marshal(DomainSpecFromEntry(e, "/d.qcow2", "/d.iso"))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			live, err := ParseDomainNetworkShape(string(doc))
			if err != nil {
				t.Fatalf("ParseDomainNetworkShape: %v", err)
			}
			if want := NetworkShapeOf(e); live != want {
				t.Errorf("rendered %+v but the spec asks for %+v — this pins the restart badge on for the VM's whole life", live, want)
			}
		})
	}
}
