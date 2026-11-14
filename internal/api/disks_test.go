package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/api"
	"github.com/nanocloud-io/nanocloud/internal/config"
	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/storage"
)

// needQemuImg gates the tests that put a real file on disk. The refusals are
// all reachable without qemu-img.
func needQemuImg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not on PATH: skipping test that creates a real disk")
	}
}

// seedDisk records a disk on the spec and puts a placeholder file at its path,
// which is enough for detach and for the refusals.
func seedDisk(t *testing.T, f specFixture, vmName, target string) string {
	t.Helper()

	entry, err := f.store.Get(vmName)
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	path := filepath.Join(entry.PoolPath, vmName+"-"+target+storage.DiskExt)
	if err := os.WriteFile(path, []byte("not a real qcow2"), 0o600); err != nil {
		t.Fatalf("seed disk file: %v", err)
	}
	if err := f.store.Update(vmName, func(e *metadata.VMEntry) {
		e.Disks = append(e.Disks, metadata.Disk{Target: target, Path: path, SizeGB: 1})
	}); err != nil {
		t.Fatalf("seed disk entry: %v", err)
	}
	return path
}

// TestHandleDiskAttach_RecordsAndRenders checks that an attached disk is on
// the host, in the spec and in the domain.
func TestHandleDiskAttach_RecordsAndRenders(t *testing.T) {
	needQemuImg(t)
	const name = "disk-attach-probe"
	f := newSpecFixture(t, name)

	got := f.do(t, http.MethodPost, "/vms/"+name+"/disks", `{"size_gb":1}`, http.StatusOK)
	if len(got.Disks) != 2 || got.Disks[1].Target != "vdb" {
		t.Fatalf("disks = %+v, want the boot overlay and a new vdb", got.Disks)
	}
	if !got.Disks[0].Boot || got.Disks[1].Boot {
		t.Errorf("boot flags = %v/%v, want the overlay marked and the new disk not", got.Disks[0].Boot, got.Disks[1].Boot)
	}
	if _, err := os.Stat(got.Disks[1].Path); err != nil {
		t.Errorf("no file at %s: %v", got.Disks[1].Path, err)
	}

	entry, err := f.store.Get(name)
	if err != nil {
		t.Fatalf("read back spec: %v", err)
	}
	if len(entry.Disks) != 1 || entry.Disks[0].Target != "vdb" {
		t.Errorf("metadata.json records %+v, want one disk on vdb", entry.Disks)
	}

	hw, err := f.mgr.ActiveHardware(name)
	if err != nil {
		t.Fatalf("read live hardware: %v", err)
	}
	if len(hw.DiskTargets) != 2 {
		t.Errorf("the defined domain has disks %v, want vda and vdb — the spec was written but not applied", hw.DiskTargets)
	}
}

// TestHandleDiskAttach_SkipsAnOccupiedFilename checks that attach never reuses
// the filename of a detached disk that is still on disk.
func TestHandleDiskAttach_SkipsAnOccupiedFilename(t *testing.T) {
	needQemuImg(t)
	const name = "disk-orphan-probe"
	f := newSpecFixture(t, name)

	// An orphan exactly where the first vdb would land, as a detach leaves one.
	orphan := seedDisk(t, f, name, "vdb")
	if err := f.store.Update(name, func(e *metadata.VMEntry) { e.Disks = nil }); err != nil {
		t.Fatalf("detach the seeded disk: %v", err)
	}

	got := f.do(t, http.MethodPost, "/vms/"+name+"/disks", `{"size_gb":1}`, http.StatusOK)
	if got.Disks[1].Path == orphan {
		t.Fatalf("the new disk reused %s, which still holds the detached disk's data", orphan)
	}
	if body, err := os.ReadFile(orphan); err != nil || string(body) != "not a real qcow2" {
		t.Errorf("the orphan at %s was overwritten (err %v)", orphan, err)
	}
}

func TestHandleDiskDetach_KeepsTheFileUnlessAsked(t *testing.T) {
	const name = "disk-detach-probe"
	f := newSpecFixture(t, name)
	path := seedDisk(t, f, name, "vdb")

	got := f.do(t, http.MethodDelete, "/vms/"+name+"/disks/vdb", "", http.StatusOK)
	if len(got.Disks) != 1 {
		t.Errorf("disks = %+v, want the boot overlay alone", got.Disks)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("detach removed %s without being asked to: %v", path, err)
	}

	// And with the flag, it goes.
	seedDisk(t, f, name, "vdb")
	f.do(t, http.MethodDelete, "/vms/"+name+"/disks/vdb?delete_file=true", "", http.StatusOK)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s survived delete_file=true: %v", path, err)
	}
}

func TestHandleDiskRefusals(t *testing.T) {
	tests := []struct {
		name, method, path, body string
		wantStatus               int
		wantIn                   string
	}{
		{
			name: "attaching nothing", method: http.MethodPost, path: "/disks", body: `{"size_gb":0}`,
			wantStatus: http.StatusBadRequest, wantIn: "size_gb",
		},
		{
			// The boot disk cannot be detached.
			name: "detaching the boot disk", method: http.MethodDelete, path: "/disks/vda", body: "",
			wantStatus: http.StatusBadRequest, wantIn: "boots from",
		},
		{
			name: "detaching a disk that is not there", method: http.MethodDelete, path: "/disks/vdz", body: "",
			wantStatus: http.StatusNotFound, wantIn: "vdz",
		},
		{
			name: "resizing a disk that is not there", method: http.MethodPost, path: "/disks/vdz/resize", body: `{"size_gb":5}`,
			wantStatus: http.StatusNotFound, wantIn: "vdz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const name = "disk-refusal-probe"
			f := newSpecFixture(t, name)

			got := f.do(t, tt.method, "/vms/"+name+tt.path, tt.body, tt.wantStatus)
			if !strings.Contains(got.raw, tt.wantIn) {
				t.Errorf("refusal does not mention %q: %s", tt.wantIn, got.raw)
			}
		})
	}
}

// TestHandleDiskResize_GrowsOnly checks that a shrink is refused: it would
// discard whatever the guest wrote past the new end.
func TestHandleDiskResize_GrowsOnly(t *testing.T) {
	needQemuImg(t)
	const name = "disk-resize-probe"
	f := newSpecFixture(t, name)

	attached := f.do(t, http.MethodPost, "/vms/"+name+"/disks", `{"size_gb":2}`, http.StatusOK)
	path := attached.Disks[1].Path

	grown := f.do(t, http.MethodPost, "/vms/"+name+"/disks/vdb/resize", `{"size_gb":4}`, http.StatusOK)
	if grown.Disks[1].SizeGB != 4 {
		t.Errorf("size_gb = %d after growing to 4", grown.Disks[1].SizeGB)
	}

	for _, body := range []string{`{"size_gb":1}`, `{"size_gb":4}`} {
		got := f.do(t, http.MethodPost, "/vms/"+name+"/disks/vdb/resize", body, http.StatusBadRequest)
		if !strings.Contains(got.raw, "only be grown") {
			t.Errorf("refusal for %s does not say why: %s", body, got.raw)
		}
	}

	// The file itself, not just the record.
	info, err := storage.InspectDisk(path)
	if err != nil {
		t.Fatalf("inspect %s: %v", path, err)
	}
	if got := info.VirtualSizeBytes / storage.BytesPerGB; got != 4 {
		t.Errorf("the image is %d GB, want 4 — the size was recorded but not applied", got)
	}
}

// TestHandleDiskResize_RefusesWhatQemuImgCannotDo covers a running VM and an
// image holding internal snapshots, where qemu-img's own errors name neither.
func TestHandleDiskResize_RefusesWhatQemuImgCannotDo(t *testing.T) {
	needQemuImg(t)
	const name = "disk-resize-refuse-probe"
	f := newSpecFixture(t, name)

	attached := f.do(t, http.MethodPost, "/vms/"+name+"/disks", `{"size_gb":1}`, http.StatusOK)
	path := attached.Disks[1].Path

	// Snapshots first, while the VM is off, or the running check wins.
	snapshotDisk(t, path, "before-upgrade")
	got := f.do(t, http.MethodPost, "/vms/"+name+"/disks/vdb/resize", `{"size_gb":4}`, http.StatusConflict)
	if !strings.Contains(got.raw, "snapshot") {
		t.Errorf("refusal does not mention the snapshots holding it up: %s", got.raw)
	}

	if err := f.mgr.Start(name); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	got = f.do(t, http.MethodPost, "/vms/"+name+"/disks/vdb/resize", `{"size_gb":4}`, http.StatusConflict)
	if !strings.Contains(got.raw, "running") {
		t.Errorf("refusal does not say the VM has to be stopped: %s", got.raw)
	}
}

func TestHandleVMDelete_RemovesEveryDisk(t *testing.T) {
	const name = "disk-delete-probe"
	f := newSpecFixture(t, name)

	first := seedDisk(t, f, name, "vdb")
	second := seedDisk(t, f, name, "vdc")

	f.do(t, http.MethodDelete, "/vms/"+name, "", http.StatusNoContent)

	for _, path := range []string{first, second} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s survived the VM being deleted: %v", path, err)
		}
	}
}

// TestHandleDiskResize_RefusesWhenItCannotTellIfTheVMIsStopped: a VM whose
// state cannot be read may still be running, so it is not treated as stopped.
func TestHandleDiskResize_RefusesWhenItCannotTellIfTheVMIsStopped(t *testing.T) {
	const name = "disk-unreachable-probe"

	store := metadata.New(filepath.Join(t.TempDir(), "metadata.json"))
	if err := store.Upsert(name, func(e *metadata.VMEntry) {
		e.ID, e.PoolPath, e.DiskGB = name, t.TempDir(), 10
	}); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	srv := api.New(api.Deps{
		WebFS: minimalWebFS(), Cfg: &config.Config{},
		VMManager: disconnectedManager(), MetaStore: store,
	})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/vms/"+name+"/disks/vda/resize", strings.NewReader(`{"size_gb":20}`)))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d — an unreadable state must not be read as stopped (body: %s)",
			rec.Code, http.StatusServiceUnavailable, rec.Body)
	}

	entry, err := store.Get(name)
	if err != nil {
		t.Fatalf("read back spec: %v", err)
	}
	if entry.DiskGB != 10 {
		t.Errorf("disk_gb = %d, want it left at 10 — the resize was refused", entry.DiskGB)
	}
}

// snapshotDisk stores a snapshot of the disk at path the way this platform's
// backend does: inside the qcow2 image on Linux, as a clone beside a raw
// image on macOS.
func snapshotDisk(t *testing.T, path, name string) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		dir := storage.SnapshotDir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+storage.DiskExt), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if out, err := exec.Command("qemu-img", "snapshot", "-c", name, path).CombinedOutput(); err != nil {
		t.Fatalf("qemu-img snapshot: %v — %s", err, out)
	}
}
