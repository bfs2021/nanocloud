//go:build !windows

package metadata_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

// writeRaw drops a literal document on disk at the store's path.
func writeRaw(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "metadata.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	return p
}

// readVMs decodes the document's vms object as raw JSON, so a test can assert
// on keys VMEntry does not declare.
func readVMs(t *testing.T, path string) map[string]map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back %s: %v", path, err)
	}
	var doc struct {
		VMs map[string]map[string]json.RawMessage `json:"vms"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, raw)
	}
	return doc.VMs
}

// v0Doc is the legacy format: a bare top-level object of entries, no envelope.
const v0Doc = `{
  "web-01": {
    "id": "web-01",
    "pool_path": "/var/lib/nano-cloud/vm-disks",
    "network_mode": "nat",
    "cloud_init_user": "ubuntu",
    "hostname": "web-01",
    "image_id": "ubuntu-24.04",
    "os": "ubuntu",
    "started_at_unix": 1754000000
  }
}`

// TestRead_LegacyV0Document checks a v0 file reads with every field intact.
func TestRead_LegacyV0Document(t *testing.T) {
	dir := t.TempDir()
	p := writeRaw(t, dir, v0Doc)
	s := metadata.New(p)

	got, err := s.Read()
	if err != nil {
		t.Fatalf("Read legacy: %v", err)
	}
	e, ok := got["web-01"]
	if !ok {
		t.Fatalf("legacy entry missing; got %+v", got)
	}
	if e.ImageID != "ubuntu-24.04" || e.CloudInitUser != "ubuntu" || e.StartedAtUnix != 1754000000 {
		t.Errorf("legacy fields lost: %+v", e)
	}

	// A missing "running" key must read as unknown, not false.
	if _, ok := e.DesiredRunning(); ok {
		t.Errorf("legacy entry reports a known desired state (%v); want unknown (nil)", e.Running)
	}
}

// TestMigrate_V0ToV1 checks the on-disk rewrite and that it is idempotent.
func TestMigrate_V0ToV1(t *testing.T) {
	dir := t.TempDir()
	p := writeRaw(t, dir, v0Doc)
	s := metadata.New(p)

	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var doc struct {
		SchemaVersion int                         `json:"schema_version"`
		VMs           map[string]metadata.VMEntry `json:"vms"`
	}
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("migrated file is not valid JSON: %v\n%s", err, raw)
	}
	if doc.SchemaVersion != metadata.CurrentSchemaVersion {
		t.Errorf("schema_version = %d, want %d", doc.SchemaVersion, metadata.CurrentSchemaVersion)
	}
	if _, ok := doc.VMs["web-01"]; !ok {
		t.Errorf("entry lost during migration: %s", raw)
	}

	if err = s.Migrate(); err != nil {
		t.Fatalf("Migrate (second run): %v", err)
	}
	after, err := s.Read()
	if err != nil {
		t.Fatalf("Read after second Migrate: %v", err)
	}
	if len(after) != 1 {
		t.Errorf("entries after second Migrate = %d, want 1", len(after))
	}
}

// TestMigrate_NoFileOrEmpty checks that a missing or empty file is not an error.
func TestMigrate_NoFileOrEmpty(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		s := metadata.New(filepath.Join(t.TempDir(), "metadata.json"))
		if err := s.Migrate(); err != nil {
			t.Fatalf("Migrate on missing file: %v", err)
		}
	})
	t.Run("empty", func(t *testing.T) {
		dir := t.TempDir()
		s := metadata.New(writeRaw(t, dir, ""))
		if err := s.Migrate(); err != nil {
			t.Fatalf("Migrate on empty file: %v", err)
		}
	})
}

// TestRead_RefusesNewerSchema guards a downgrade: a newer schema must be
// refused, not rewritten without the fields this binary does not know.
func TestRead_RefusesNewerSchema(t *testing.T) {
	dir := t.TempDir()
	p := writeRaw(t, dir, `{"schema_version": 99, "vms": {"web-01": {"id": "web-01"}}}`)
	s := metadata.New(p)

	_, err := s.Read()
	if !errors.Is(err, metadata.ErrSchemaTooNew) {
		t.Fatalf("Read of v99 file: got %v, want ErrSchemaTooNew", err)
	}
	// The operator needs the on-disk version to pick the right binary.
	if got := err.Error(); got == "" || !strings.Contains(got, "99") {
		t.Errorf("error should name the on-disk version, got: %v", err)
	}

	if err := s.Migrate(); !errors.Is(err, metadata.ErrSchemaTooNew) {
		t.Errorf("Migrate of v99 file: got %v, want ErrSchemaTooNew", err)
	}
}

// TestDecode_VMNamedLikeEnvelopeKey checks that a v0 file with VMs named
// "schema_version" and "vms" is not misread as an envelope.
func TestDecode_VMNamedLikeEnvelopeKey(t *testing.T) {
	dir := t.TempDir()
	p := writeRaw(t, dir, `{
	  "schema_version": {"id": "schema_version", "hostname": "tricky", "os": "alpine"},
	  "vms":            {"id": "vms",            "hostname": "also-tricky", "os": "debian"},
	  "web-01":         {"id": "web-01",         "hostname": "normal",      "os": "ubuntu"}
	}`)
	s := metadata.New(p)

	got, err := s.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d: %+v", len(got), got)
	}
	for _, name := range []string{"schema_version", "vms", "web-01"} {
		if e, ok := got[name]; !ok {
			t.Errorf("entry %q lost — the legacy document was misread as an envelope", name)
		} else if e.ID != name {
			t.Errorf("entry %q: ID = %q, want %q", name, e.ID, name)
		}
	}

	if err = s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	after, err := s.Read()
	if err != nil {
		t.Fatalf("Read after Migrate: %v", err)
	}
	if len(after) != 3 {
		t.Errorf("entries after migration = %d, want 3: %+v", len(after), after)
	}
}

// TestWrite_ProducesEnvelope pins the shape of newly written files.
func TestWrite_ProducesEnvelope(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "metadata.json")
	s := metadata.New(p)

	running := true
	if err := s.Write(map[string]metadata.VMEntry{
		"web-01": {ID: "web-01", Running: &running, VCPUs: 2, MemoryMB: 2048, DiskGB: 20},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var doc struct {
		SchemaVersion int `json:"schema_version"`
		VMs           map[string]struct {
			Running  *bool `json:"running"`
			VCPUs    int   `json:"vcpus"`
			MemoryMB int   `json:"memory_mb"`
			DiskGB   int   `json:"disk_gb"`
		} `json:"vms"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, raw)
	}
	if doc.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", doc.SchemaVersion)
	}
	e := doc.VMs["web-01"]
	if e.Running == nil || !*e.Running {
		t.Errorf("running = %v, want true", e.Running)
	}
	if e.VCPUs != 2 || e.MemoryMB != 2048 || e.DiskGB != 20 {
		t.Errorf("spec fields not persisted: %+v", e)
	}
}

// TestSetRunning_RoundTrips checks that SetRunning(false) reads back as known.
func TestSetRunning_RoundTrips(t *testing.T) {
	s := newStore(t)
	if err := s.Upsert("vm-1", func(e *metadata.VMEntry) { e.SetRunning(false) }); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	e, err := s.Get("vm-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	running, ok := e.DesiredRunning()
	if !ok {
		t.Fatal("DesiredRunning reports unknown after SetRunning(false)")
	}
	if running {
		t.Error("DesiredRunning = true, want false")
	}
}

// TestPolicy_DefaultsToOnFailure pins that an in-guest poweroff is honoured by
// default.
func TestPolicy_DefaultsToOnFailure(t *testing.T) {
	var e metadata.VMEntry
	if got := e.Policy(); got != metadata.RestartOnFailure {
		t.Errorf("default policy = %q, want %q", got, metadata.RestartOnFailure)
	}
	e.RestartPolicy = metadata.RestartNever
	if got := e.Policy(); got != metadata.RestartNever {
		t.Errorf("explicit policy = %q, want %q", got, metadata.RestartNever)
	}
}

// TestModify_PreservesUnknownFields guards a rollback: a read-modify-write must
// keep fields written by a newer release.
func TestModify_PreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	p := writeRaw(t, dir, `{
	  "schema_version": 1,
	  "vms": {
	    "web-01": {
	      "id": "web-01",
	      "pool_path": "/var/lib/nano-cloud/vm-disks",
	      "vcpus": 2,
	      "node_hint": "box-2",
	      "network": {"mode": "bridge", "vlan_id": 20}
	    }
	  }
	}`)
	s := metadata.New(p)

	if err := s.Update("web-01", func(e *metadata.VMEntry) { e.MemoryMB = 4096 }); err != nil {
		t.Fatalf("Update: %v", err)
	}

	vm := readVMs(t, p)["web-01"]
	for key, want := range map[string]string{
		"node_hint": `"box-2"`,
		"network":   `{"mode": "bridge", "vlan_id": 20}`,
		"vcpus":     `2`,
		"memory_mb": `4096`,
	} {
		got, ok := vm[key]
		if !ok {
			t.Errorf("%q was dropped by a read-modify-write cycle", key)
			continue
		}
		if !json.Valid(got) {
			t.Errorf("%q is not valid JSON after round-trip: %s", key, got)
		}
		if equal, err := jsonEqual(got, []byte(want)); err != nil || !equal {
			t.Errorf("%q = %s, want %s", key, got, want)
		}
	}
}

// TestWrite_OmitsUnsetHardwareFields guards that unset hardware fields are not
// written. omitempty does nothing for a struct value, so nested specs must stay
// pointers.
func TestWrite_OmitsUnsetHardwareFields(t *testing.T) {
	dir := t.TempDir()
	p := writeRaw(t, dir, v0Doc)
	s := metadata.New(p)

	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	vm := readVMs(t, p)["web-01"]
	for _, key := range []string{
		"firmware", "secure_boot", "tpm",
		"cpu_mode", "cpu_topology", "cpu_set",
		"pci_devices", "usb_devices", "disks", "cloud_init",
		// Writing empty keys would make legacy entries look backfilled.
		"uuid", "mac",
	} {
		if _, present := vm[key]; present {
			t.Errorf("entry grew an unset %q key: %s", key, vm[key])
		}
	}
}

// TestModify_ClearedFieldIsNotResurrected checks that clearing a known field
// does not write back a stale retained copy.
func TestModify_ClearedFieldIsNotResurrected(t *testing.T) {
	dir := t.TempDir()
	p := writeRaw(t, dir, `{"schema_version": 1, "vms": {"web-01": {"id": "web-01", "vcpus": 4}}}`)
	s := metadata.New(p)

	if err := s.Update("web-01", func(e *metadata.VMEntry) { e.VCPUs = 0 }); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if got, present := readVMs(t, p)["web-01"]["vcpus"]; present {
		t.Errorf("vcpus came back as %s after being cleared", got)
	}
}

// TestSchemaTypes_EveryExportedFieldHasJSONTag checks every exported field is
// tagged; an untagged one is encoded under its Go name, which the frontend
// cannot read.
func TestSchemaTypes_EveryExportedFieldHasJSONTag(t *testing.T) {
	for _, v := range []any{
		metadata.VMEntry{}, metadata.CPUTopology{}, metadata.PCIDevice{},
		metadata.USBDevice{}, metadata.Disk{}, metadata.CloudInitSpec{}, metadata.WriteFile{},
	} {
		typ := reflect.TypeOf(v)
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.PkgPath != "" {
				continue // unexported; encoding/json never sees it
			}
			if f.Tag.Get("json") == "" {
				t.Errorf("%s.%s has no json tag", typ.Name(), f.Name)
			}
		}
	}
}

// jsonEqual compares two JSON documents by value, so an assertion does not
// depend on key order or whitespace.
func jsonEqual(a, b []byte) (bool, error) {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false, err
	}
	return reflect.DeepEqual(av, bv), nil
}

func TestVMEntry_VLANUplinkRoundTrips(t *testing.T) {
	in := metadata.VMEntry{ID: "web", NetworkMode: metadata.NetworkBridge, Bridge: "nc-vlan20", VLANID: 20, VLANUplink: "eth0"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out metadata.VMEntry
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.VLANUplink != "eth0" {
		t.Errorf("VLANUplink = %q, want eth0", out.VLANUplink)
	}
}

func TestVMEntry_UnsetVLANUplinkIsOmitted(t *testing.T) {
	b, err := json.Marshal(metadata.VMEntry{ID: "web", NetworkMode: metadata.NetworkNAT})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("vlan_uplink")) {
		t.Errorf("unset VLANUplink was written: %s", b)
	}
}
