package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/storage"
)

// needCloneTools gates the tests that copy a disk and build a seed. The
// refusals are all reachable without qemu-img or xorriso.
func needCloneTools(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"qemu-img", "xorriso"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH: skipping test that clones a real disk", bin)
		}
	}
}

type cloneBody struct {
	Name     string   `json:"name"`
	Warnings []string `json:"warnings"`
}

func clone(t *testing.T, f specFixture, source, body string, wantStatus int) cloneBody {
	t.Helper()

	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/vms/"+source+"/clone", strings.NewReader(body)))
	if rec.Code != wantStatus {
		t.Fatalf("clone %s: status = %d, want %d (body: %s)", source, rec.Code, wantStatus, rec.Body)
	}

	var got cloneBody
	if wantStatus == http.StatusCreated {
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode clone response: %v (body: %s)", err, rec.Body)
		}
	}
	return got
}

// TestHandleVMClone_CopiesEveryDiskToItsOwnFile checks that attached disks are
// copied too, so no file is shared between the two domains.
func TestHandleVMClone_CopiesEveryDiskToItsOwnFile(t *testing.T) {
	needCloneTools(t)
	const name = "clone-source"
	f := newSpecFixture(t, name)

	if err := storage.CreateDisk(storage.DiskPathFor(f.poolDir, name), 1); err != nil {
		t.Fatalf("seed the source's boot disk: %v", err)
	}
	extra := f.do(t, http.MethodPost, "/vms/"+name+"/disks", `{"size_gb":1}`, http.StatusOK)
	sourceExtra := extra.Disks[1].Path

	got := clone(t, f, name, `{"name":"clone-copy"}`, http.StatusCreated)
	if got.Name != "clone-copy" || len(got.Warnings) != 0 {
		t.Errorf("response = %+v, want the clone's name and no warnings", got)
	}
	t.Cleanup(func() { f.mgr.Delete("clone-copy") }) //nolint:errcheck // best-effort cleanup in test

	if _, err := f.mgr.Get("clone-copy"); err != nil {
		t.Fatalf("the clone was not defined in libvirt: %v", err)
	}

	entry, err := f.store.Get("clone-copy")
	if err != nil {
		t.Fatalf("read the clone's spec: %v", err)
	}
	if entry.ID != "clone-copy" || entry.Hostname != "clone-copy" {
		t.Errorf("spec = %q/%q, want the clone's own name and hostname", entry.ID, entry.Hostname)
	}
	if len(entry.Disks) != 1 {
		t.Fatalf("disks = %+v, want the one attached disk, copied", entry.Disks)
	}
	if entry.Disks[0].Path == sourceExtra {
		t.Fatalf("the clone's %s points at the source's file %s — both domains would write it",
			entry.Disks[0].Target, sourceExtra)
	}
	for _, path := range []string{
		storage.DiskPathFor(f.poolDir, "clone-copy"),
		storage.ISOPathFor(f.poolDir, "clone-copy"),
		entry.Disks[0].Path,
	} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Errorf("the clone is missing %s: %v", path, statErr)
		}
	}

	// The source keeps everything it had.
	source, err := f.store.Get(name)
	if err != nil || len(source.Disks) != 1 || source.Disks[0].Path != sourceExtra {
		t.Errorf("cloning changed the source: %+v (%v)", source, err)
	}
}

// A device the source passes through is not copied, and the response says so.
func TestHandleVMClone_DropsPassthroughDevicesWithAWarning(t *testing.T) {
	needCloneTools(t)
	const name = "clone-devices"
	f := newSpecFixture(t, name)

	if err := storage.CreateDisk(storage.DiskPathFor(f.poolDir, name), 1); err != nil {
		t.Fatalf("seed the source's boot disk: %v", err)
	}
	// Written straight to the store: the test driver has no host devices, so
	// PATCH would refuse one.
	if err := f.store.Update(name, func(e *metadata.VMEntry) {
		e.USBDevices = []metadata.USBDevice{{VendorID: "0x1050", ProductID: "0x0407"}}
	}); err != nil {
		t.Fatalf("seed a passthrough device: %v", err)
	}

	got := clone(t, f, name, `{"name":"clone-nodev"}`, http.StatusCreated)
	t.Cleanup(func() { f.mgr.Delete("clone-nodev") }) //nolint:errcheck // best-effort cleanup in test

	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "host device") {
		t.Errorf("warnings = %v, want one explaining the devices were not copied", got.Warnings)
	}
	entry, err := f.store.Get("clone-nodev")
	if err != nil {
		t.Fatalf("read the clone's spec: %v", err)
	}
	if len(entry.USBDevices) != 0 {
		t.Errorf("usb_devices = %+v, want none on a clone", entry.USBDevices)
	}
}

// The refusals, all reachable with neither qemu-img nor xorriso installed.
func TestHandleVMClone_Refusals(t *testing.T) {
	const name = "clone-refusals"
	f := newSpecFixture(t, name)

	for _, tc := range []struct {
		what string
		body string
		want int
	}{
		{"a name that is not a safe path component", `{"name":"../escape"}`, http.StatusBadRequest},
		{"an empty name", `{"name":""}`, http.StatusBadRequest},
		{"a name a domain already has", `{"name":"` + name + `"}`, http.StatusConflict},
	} {
		t.Run(tc.what, func(t *testing.T) { clone(t, f, name, tc.body, tc.want) })
	}

	// A running source cannot be copied consistently.
	if err := f.mgr.Start(name); err != nil {
		t.Fatalf("start the source: %v", err)
	}
	defer f.mgr.Destroy(name) //nolint:errcheck // best-effort cleanup in test
	clone(t, f, name, `{"name":"clone-of-running"}`, http.StatusConflict)

	// And a VM nano-cloud never created has no spec to copy.
	clone(t, f, "not-a-vm", `{"name":"clone-of-nothing"}`, http.StatusNotFound)
}

// TestHandleVMClone_BuildsTheVLANItInherits checks that a clone ensures its
// inherited VLAN bridge before copying anything. libvirt would accept a
// missing bridge and fail at the clone's first start.
func TestHandleVMClone_BuildsTheVLANItInherits(t *testing.T) {
	seedVLANSource := func(t *testing.T, f specFixture, name string) {
		t.Helper()
		if err := f.store.Update(name, func(e *metadata.VMEntry) {
			e.NetworkMode, e.Bridge, e.VLANID, e.VLANUplink = metadata.NetworkBridge, "nc-vlan20", 20, "eth0"
		}); err != nil {
			t.Fatalf("seed the source's VLAN: %v", err)
		}
	}

	t.Run("provisions before copying", func(t *testing.T) {
		const name = "clone-vlan-source"
		prov := &fakeProvisioner{uplink: "eth0"}
		f := newSpecFixture(t, name, prov)
		seedVLANSource(t, f, name)

		// Status is deliberately unasserted: without the clone tools this dies
		// at qemu-img, which is after the call under test.
		rec := httptest.NewRecorder()
		f.srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/vms/"+name+"/clone",
			strings.NewReader(`{"name":"clone-vlan-copy"}`)))

		if len(prov.ensured) != 1 || prov.ensured[0] != "eth0/20" {
			t.Fatalf("Ensure calls = %v, want [eth0/20] — the clone defines against a bridge nothing built", prov.ensured)
		}
	})

	t.Run("a host that cannot build it refuses the clone", func(t *testing.T) {
		const name = "clone-vlan-unbuildable"
		f := newSpecFixture(t, name, &fakeProvisioner{uplink: "eth0", ensureErr: errors.New("no such device")})
		seedVLANSource(t, f, name)

		clone(t, f, name, `{"name":"clone-vlan-doomed"}`, http.StatusUnprocessableEntity)

		if _, err := os.Stat(filepath.Join(f.poolDir, "clone-vlan-doomed.qcow2")); !os.IsNotExist(err) {
			t.Errorf("a refused clone left a disk behind (stat err = %v)", err)
		}
	})
}
