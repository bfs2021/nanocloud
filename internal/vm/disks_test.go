package vm

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

// TestNextDiskTarget: the target ends up in the guest's /etc/fstab, so it must
// come from the spec, stay stable across redefines and never collide.
func TestNextDiskTarget(t *testing.T) {
	tests := []struct {
		name    string
		disks   []metadata.Disk
		want    string
		wantErr bool
	}{
		{name: "the first attached disk follows the boot overlay", want: "vdb"},
		{
			name:  "the next one follows that",
			disks: []metadata.Disk{{Target: "vdb"}},
			want:  "vdc",
		},
		{
			// A freed letter is reused before the end is extended, or a VM that gains
			// and loses disks over time runs out of letters.
			name:  "a gap left by a detach is reused",
			disks: []metadata.Disk{{Target: "vdc"}, {Target: "vdd"}},
			want:  "vdb",
		},
		{
			name:  "never the boot overlay, whatever the spec claims",
			disks: []metadata.Disk{{Target: "vdb"}, {Target: "vdc"}},
			want:  "vdd",
		},
		{name: "a full VM is refused rather than given a made-up name", disks: allTargets(), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NextDiskTarget(metadata.VMEntry{ID: "vm", Disks: tt.disks})
			if (err != nil) != tt.wantErr {
				t.Fatalf("NextDiskTarget = %q, %v; wantErr %v", got, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("NextDiskTarget = %q, want %q", got, tt.want)
			}
		})
	}
}

// allTargets fills every letter this repo hands out, boot overlay included.
func allTargets() []metadata.Disk {
	out := make([]metadata.Disk, 0, maxDisks-1)
	for i := 1; i < maxDisks; i++ {
		out = append(out, metadata.Disk{Target: fmt.Sprintf("vd%c", 'a'+i)})
	}
	return out
}

// TestBuildDomainXML_ExtraDisks pins where an attached disk lands and its
// defaults. The boot overlay and seed cdrom keep their positions.
func TestBuildDomainXML_ExtraDisks(t *testing.T) {
	e := baseEntry()
	e.Disks = []metadata.Disk{
		{Target: "vdb", Path: "/pool/test-vm-vdb.qcow2", SizeGB: 10},
		// Format and Bus are only set by a hand-edited metadata.json, so both the
		// set and unset cases must render.
		{Target: "vdc", Path: "/pool/data.raw", SizeGB: 5, Format: "raw", Bus: "sata"},
	}

	disks := parseDoc(t, DomainSpecFromEntry(e, "/pool/test-vm.qcow2", "/pool/test-vm-cidata.iso")).find("devices/disk")
	if len(disks) != 4 {
		t.Fatalf("got %d disks, want 4 (boot overlay + seed ISO + 2 attached)", len(disks))
	}

	for _, tc := range []struct {
		label, got, want string
	}{
		{"boot stays first", disks[0].find("target")[0].attr("dev"), "vda"},
		{"seed stays second", disks[1].attr("device"), "cdrom"},
		{"first attached target", disks[2].find("target")[0].attr("dev"), "vdb"},
		{"first attached bus defaults to virtio", disks[2].find("target")[0].attr("bus"), "virtio"},
		{"first attached format defaults to qcow2", disks[2].find("driver")[0].attr("type"), "qcow2"},
		{"first attached source", disks[2].find("source")[0].attr("file"), "/pool/test-vm-vdb.qcow2"},
		{"second attached honours its bus", disks[3].find("target")[0].attr("bus"), "sata"},
		{"second attached honours its format", disks[3].find("driver")[0].attr("type"), "raw"},
		{"an attached disk is a disk, not a cdrom", disks[2].attr("device"), "disk"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.label, tc.got, tc.want)
		}
	}
}

// TestParseDomainDiskTargets: the seed cdrom must not count, or every VM reads
// as restart-required.
func TestParseDomainDiskTargets(t *testing.T) {
	const doc = `<domain type='kvm'>
  <devices>
    <disk type='file' device='disk'><source file='/pool/web.qcow2'/><target dev='vda' bus='virtio'/></disk>
    <disk type='file' device='cdrom'><source file='/pool/web-cidata.iso'/><target dev='sda' bus='sata'/></disk>
    <disk type='file' device='disk'><source file='/pool/web-vdb.qcow2'/><target dev='vdb' bus='virtio'/></disk>
  </devices>
</domain>`

	got, err := ParseDomainDiskTargets(doc)
	if err != nil {
		t.Fatalf("ParseDomainDiskTargets: %v", err)
	}
	if want := []string{"vda", "vdb"}; !reflect.DeepEqual(got, want) {
		t.Errorf("targets = %v, want %v", got, want)
	}
}

// TestDiskTargets: the spec side must include the boot overlay, which is not in
// Disks, or every VM looks like it lost a disk.
func TestDiskTargets(t *testing.T) {
	e := metadata.VMEntry{ID: "web", Disks: []metadata.Disk{{Target: "vdc"}, {Target: "vdb"}}}

	if got, want := DiskTargets(e), []string{"vda", "vdb", "vdc"}; !reflect.DeepEqual(got, want) {
		t.Errorf("DiskTargets = %v, want %v", got, want)
	}
}
