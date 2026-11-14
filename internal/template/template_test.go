package template

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "templates.json"))
}

func sample() metadata.VMEntry {
	running := true
	return metadata.VMEntry{
		ID:            "web-01",
		Hostname:      "web-01",
		PoolPath:      "/var/lib/nano-cloud/vm-disks",
		UUID:          "46acc901-1f2e-4a3b-8c7d-5e6f70819203",
		MAC:           "52:54:00:dc:cb:18",
		Running:       &running,
		StartedAtUnix: 1754800000,
		Disks:         []metadata.Disk{{Target: "vdb", Path: "/var/lib/nano-cloud/vm-disks/web-01-vdb.qcow2", SizeGB: 20}},
		ImageID:       "ubuntu-24.04",
		OS:            "ubuntu",
		VCPUs:         4,
		MemoryMB:      4096,
		DiskGB:        40,
		NetworkMode:   "bridge",
		CloudInitUser: "alex",
		Firmware:      metadata.FirmwareUEFI,
		SecureBoot:    true,
		CPUMode:       metadata.CPUHostPassthrough,
		CPUSet:        "0-3",
		PCIDevices:    []metadata.PCIDevice{{Address: "0000:01:00.0", Vendor: "NVIDIA"}},
		Service:       "db",
		CloudInit:     &metadata.CloudInitSpec{Packages: []string{"nginx"}, SSHKeys: []string{"ssh-ed25519 AAAA"}},
	}
}

// Guards that per-machine fields, especially disks pointing at the source VM's
// files, are not copied into a template.
func TestFromEntry_DropsWhatBelongsToOneMachine(t *testing.T) {
	got := FromEntry("web server", sample()).Spec

	for _, c := range []struct {
		field string
		blank bool
	}{
		{"ID", got.ID == ""},
		{"Hostname", got.Hostname == ""},
		{"PoolPath", got.PoolPath == ""},
		{"UUID", got.UUID == ""},
		{"MAC", got.MAC == ""},
		{"Running", got.Running == nil},
		{"StartedAtUnix", got.StartedAtUnix == 0},
		{"Disks", got.Disks == nil},
		{"Service", got.Service == ""},
	} {
		if !c.blank {
			t.Errorf("%s should be dropped from a template, got %#v", c.field, got)
		}
	}

	want := sample()
	for _, c := range []struct {
		field string
		kept  bool
	}{
		{"ImageID", got.ImageID == want.ImageID},
		{"VCPUs", got.VCPUs == want.VCPUs},
		{"MemoryMB", got.MemoryMB == want.MemoryMB},
		{"DiskGB", got.DiskGB == want.DiskGB},
		{"NetworkMode", got.NetworkMode == want.NetworkMode},
		{"CloudInitUser", got.CloudInitUser == want.CloudInitUser},
		{"Firmware", got.Firmware == want.Firmware},
		{"SecureBoot", got.SecureBoot == want.SecureBoot},
		{"CPUMode", got.CPUMode == want.CPUMode},
		{"CPUSet", got.CPUSet == want.CPUSet},
		{"PCIDevices", len(got.PCIDevices) == 1},
		{"CloudInit", got.CloudInit != nil && len(got.CloudInit.SSHKeys) == 1},
	} {
		if !c.kept {
			t.Errorf("%s should survive into a template, got %#v", c.field, got)
		}
	}
}

func TestStore_RoundTripsAndOrdersByName(t *testing.T) {
	s := newStore(t)
	for _, name := range []string{"web server", "alpine tiny"} {
		if err := s.Create(FromEntry(name, sample())); err != nil {
			t.Fatalf("Create(%q): %v", name, err)
		}
	}

	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].Name != "alpine tiny" || list[1].Name != "web server" {
		t.Fatalf("List should be ordered by name, got %+v", list)
	}

	got, err := s.Get("web server")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec.ImageID != "ubuntu-24.04" || got.CreatedAtUnix == 0 {
		t.Errorf("Get returned %+v, want the stored spec and a creation time", got)
	}
}

func TestStore_RefusesDuplicateAndUnknown(t *testing.T) {
	s := newStore(t)
	if err := s.Create(FromEntry("web server", sample())); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.Create(FromEntry("web server", sample())); !errors.Is(err, ErrExists) {
		t.Errorf("Create of a taken name = %v, want ErrExists", err)
	}
	if _, err := s.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get of an unknown name = %v, want ErrNotFound", err)
	}
	if err := s.Delete("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete of an unknown name = %v, want ErrNotFound", err)
	}

	if err := s.Delete("web server"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Errorf("after Delete, List = %+v, want empty", list)
	}
}

func TestValidateName(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"web server", true},
		{"Ubuntu 24.04 (4 vCPU)", true},
		{"", false},
		{"   ", false},
		{"a/b", false},  // would not match net/http's {name} path segment
		{"a\\b", false}, // and the Windows spelling of the same problem
		{"a\nb", false}, // a control character in a display name is never meant
		{strings.Repeat("a", maxNameLen), true},
		{strings.Repeat("a", maxNameLen+1), false},
	} {
		if err := ValidateName(tc.name); (err == nil) != tc.ok {
			t.Errorf("ValidateName(%q) = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestDecode_RefusesNewerSchema(t *testing.T) {
	_, err := decode([]byte(`{"schema_version": 99, "templates": {}}`))
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Errorf("decode of a v99 file = %v, want ErrSchemaTooNew", err)
	}
}
