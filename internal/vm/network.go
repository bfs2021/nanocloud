package vm

import (
	"encoding/xml"
	"fmt"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

// NetworkShape is the comparable part of a machine's networking, like CPUShape.
type NetworkShape struct {
	Mode string
	// Bridge is empty on NAT, where libvirt owns the bridge.
	Bridge  string
	VLANTag int
}

// NetworkShapeOf reads the shape a spec asks for. VLANTag follows
// DomainSpecFromEntry: a provisioned VM (VLANUplink set) attaches untagged to
// nc-vlan<id>, so its live domain never shows a tag.
func NetworkShapeOf(e metadata.VMEntry) NetworkShape {
	if e.NetworkMode != metadata.NetworkBridge {
		return NetworkShape{Mode: metadata.NetworkNAT}
	}
	shape := NetworkShape{Mode: metadata.NetworkBridge, Bridge: e.BridgeOf()}
	if e.VLANID > 0 && e.VLANUplink == "" {
		shape.VLANTag = e.VLANID
	}
	return shape
}

// ParseDomainNetworkShape reads the shape a domain has from libvirt's XML. It
// dispatches on the interface type and reads nothing more for NAT, because a
// running type='network' interface also reports the bridge libvirt chose:
//
//	<source network='default' portid='ff01…' bridge='virbr0'/>
//
// Reading that would report every NAT VM as needing a restart.
func ParseDomainNetworkShape(doc string) (NetworkShape, error) {
	var parsed struct {
		Devices struct {
			Interfaces []struct {
				Type   string `xml:"type,attr"`
				Source struct {
					Bridge string `xml:"bridge,attr"`
				} `xml:"source"`
				VLAN struct {
					Tag struct {
						ID int `xml:"id,attr"`
					} `xml:"tag"`
				} `xml:"vlan"`
			} `xml:"interface"`
		} `xml:"devices"`
	}
	if err := xml.Unmarshal([]byte(doc), &parsed); err != nil {
		return NetworkShape{}, fmt.Errorf("vm: parse domain network shape: %w", err)
	}

	// A domain with no interface reads as NAT rather than an error, so one machine
	// nano-cloud did not build cannot fail the whole spec response.
	if len(parsed.Devices.Interfaces) == 0 {
		return NetworkShape{Mode: metadata.NetworkNAT}, nil
	}

	i := parsed.Devices.Interfaces[0]
	if i.Type != "bridge" {
		return NetworkShape{Mode: metadata.NetworkNAT}, nil
	}
	return NetworkShape{Mode: metadata.NetworkBridge, Bridge: i.Source.Bridge, VLANTag: i.VLAN.Tag.ID}, nil
}
