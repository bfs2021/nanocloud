package vm

import (
	"encoding/xml"
	"errors"
	"fmt"
	"sort"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

// BootDiskTarget is the guest device of the overlay every VM boots from. It is
// never in metadata.VMEntry.Disks, because its path derives from the pool and
// the VM name.
const BootDiskTarget = bootDiskTarget

// maxDisks limits targets to vda through vdz, so running out is a clear
// refusal rather than an unexpected vdaa.
const maxDisks = 26

// ErrNoDiskTargets means every target name is taken.
var ErrNoDiskTargets = errors.New("vm: no free disk target")

// NextDiskTarget picks the guest device name for a disk about to be attached.
// It allocates from the spec, not the live domain, so a target stays stable
// across redefines once a guest has written it into /etc/fstab.
func NextDiskTarget(e metadata.VMEntry) (string, error) {
	taken := map[string]bool{BootDiskTarget: true}
	for _, d := range e.Disks {
		taken[d.Target] = true
	}

	for i := 0; i < maxDisks; i++ {
		name := fmt.Sprintf("vd%c", 'a'+i)
		if !taken[name] {
			return name, nil
		}
	}
	return "", fmt.Errorf("%w: %q already has the %d disks a VM can hold", ErrNoDiskTargets, e.ID, maxDisks)
}

// DiskTargets reduces a spec to the guest device names it describes, boot disk
// included, for comparison against the machine that is running.
func DiskTargets(e metadata.VMEntry) []string {
	out := make([]string, 0, len(e.Disks)+1)
	out = append(out, BootDiskTarget)
	for _, d := range e.Disks {
		out = append(out, d.Target)
	}
	sort.Strings(out)
	return out
}

// ParseDomainDiskTargets reads a domain's disk targets from libvirt's XML. Only
// device='disk' counts: the seed ISO is a cdrom and would read as drift on
// every VM.
func ParseDomainDiskTargets(xmlStr string) ([]string, error) {
	var doc struct {
		Devices struct {
			Disks []struct {
				Device string `xml:"device,attr"`
				Target struct {
					Dev string `xml:"dev,attr"`
				} `xml:"target"`
			} `xml:"disk"`
		} `xml:"devices"`
	}
	if err := xml.Unmarshal([]byte(xmlStr), &doc); err != nil {
		return nil, fmt.Errorf("vm: parse domain disks: %w", err)
	}

	out := make([]string, 0, len(doc.Devices.Disks))
	for _, d := range doc.Devices.Disks {
		if d.Device == "disk" && d.Target.Dev != "" {
			out = append(out, d.Target.Dev)
		}
	}
	sort.Strings(out)
	return out, nil
}

// extraDisks maps the disks a VM has beyond its boot overlay.
func extraDisks(e metadata.VMEntry) []disk {
	out := make([]disk, 0, len(e.Disks))
	for _, d := range e.Disks {
		out = append(out, disk{
			Type: "file", Device: "disk",
			Driver: diskDrv{Name: "qemu", Type: orDefault(d.Format, "qcow2")},
			Source: diskSrc{File: d.Path},
			Target: diskTgt{Dev: d.Target, Bus: orDefault(d.Bus, "virtio")},
		})
	}
	return out
}

// orDefault applies the spec's "empty means the default" convention. Only a
// hand-edited metadata.json sets these fields.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
