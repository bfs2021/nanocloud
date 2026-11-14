package storage_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/storage"
)

func TestFreeSpace_ValidPath(t *testing.T) {
	dir := os.TempDir()
	free, total, err := storage.FreeSpace(dir)
	if err != nil {
		t.Fatalf("FreeSpace(%q): unexpected error: %v", dir, err)
	}
	if total == 0 {
		t.Errorf("FreeSpace: total = 0, expected > 0")
	}
	if free > total {
		t.Errorf("FreeSpace: free (%d) > total (%d), impossible", free, total)
	}
}

func TestFreeSpace_InvalidPath(t *testing.T) {
	_, _, err := storage.FreeSpace("/nonexistent/path/that/cannot/exist/xyz123")
	if err == nil {
		t.Fatal("FreeSpace: expected error for non-existent path, got nil")
	}
}

func TestFreeSpace_TempDir(t *testing.T) {
	dir := t.TempDir()
	free, total, err := storage.FreeSpace(dir)
	if err != nil {
		t.Fatalf("FreeSpace(%q): %v", dir, err)
	}
	if total == 0 {
		t.Errorf("total bytes = 0 for path %q", dir)
	}
	t.Logf("FreeSpace(%q): free=%d total=%d", dir, free, total)
}

func TestAllocateDisk_SkipIfQemuImgMissing(t *testing.T) {
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not on PATH: skipping AllocateDisk test")
	}

	dir := t.TempDir()

	backingPath := filepath.Join(dir, "base.raw")
	if err := createRawFile(backingPath, 1<<20 /* 1 MiB */); err != nil {
		t.Fatalf("create backing file: %v", err)
	}

	// AllocateDisk passes -F qcow2, so the backing file must be qcow2.
	backingQcow2 := filepath.Join(dir, "base.qcow2")
	convertCmd := exec.Command("qemu-img", "convert", "-f", "raw", "-O", "qcow2", backingPath, backingQcow2)
	if out, err := convertCmd.CombinedOutput(); err != nil {
		t.Fatalf("qemu-img convert: %v — output: %s", err, out)
	}

	destPath := filepath.Join(dir, "disk.qcow2")
	t.Cleanup(func() { os.Remove(destPath) }) //nolint:errcheck // best-effort test cleanup

	if err := storage.AllocateDisk(backingQcow2, destPath, 1); err != nil {
		t.Fatalf("AllocateDisk() error: %v", err)
	}

	if _, err := os.Stat(destPath); err != nil {
		t.Errorf("AllocateDisk() produced no output file: %v", err)
	}
}

func TestDeleteDisk_NonExistent(t *testing.T) {
	if err := storage.DeleteDisk("/tmp/nano-cloud-nonexistent-disk-xyz.qcow2"); err != nil {
		t.Errorf("DeleteDisk on non-existent file returned error: %v", err)
	}
}

func TestDeleteDisk_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.qcow2")
	if err := os.WriteFile(path, []byte("fake qcow2"), 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	if err := storage.DeleteDisk(path); err != nil {
		t.Fatalf("DeleteDisk() error: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists after DeleteDisk: %v", err)
	}
}

// createRawFile writes size bytes of zeros to path.
func createRawFile(path string, size int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // truncate errors are caught inline; close is best-effort
	if err := f.Truncate(size); err != nil {
		return err
	}
	return nil
}

// TestDiskLifecycle_SkipIfQemuImgMissing creates, inspects and grows a disk,
// reading back the result after each step.
func TestDiskLifecycle_SkipIfQemuImgMissing(t *testing.T) {
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not on PATH: skipping disk lifecycle test")
	}

	path := filepath.Join(t.TempDir(), "data.qcow2")
	if err := storage.CreateDisk(path, 2); err != nil {
		t.Fatalf("CreateDisk: %v", err)
	}

	info, err := storage.InspectDisk(path)
	if err != nil {
		t.Fatalf("InspectDisk: %v", err)
	}
	if want := int64(2) * storage.BytesPerGB; info.VirtualSizeBytes != want {
		t.Errorf("virtual size = %d, want %d", info.VirtualSizeBytes, want)
	}
	if info.Snapshots != 0 {
		t.Errorf("snapshots = %d on a freshly created disk, want 0", info.Snapshots)
	}

	if err = storage.ResizeDisk(path, 4); err != nil {
		t.Fatalf("ResizeDisk: %v", err)
	}
	grown, err := storage.InspectDisk(path)
	if err != nil {
		t.Fatalf("InspectDisk after resize: %v", err)
	}
	if want := int64(4) * storage.BytesPerGB; grown.VirtualSizeBytes != want {
		t.Errorf("virtual size after resize = %d, want %d", grown.VirtualSizeBytes, want)
	}

	// Without --shrink, qemu-img must refuse a smaller size.
	if err := storage.ResizeDisk(path, 1); err == nil {
		t.Error("ResizeDisk shrank a disk — qemu-img was handed a smaller size without --shrink and accepted it")
	}
}

// An empty pool would yield paths relative to the daemon's cwd, which could
// name an unrelated file.
func TestPathsFor_RefusesAVMWithNoPool(t *testing.T) {
	if disk, iso, ok := storage.PathsFor("", "web-01"); ok {
		t.Errorf("PathsFor with no pool returned %q/%q, want a refusal", disk, iso)
	}

	disk, iso, ok := storage.PathsFor("/pool", "web-01")
	if !ok {
		t.Fatal("PathsFor refused a real pool")
	}
	if disk != storage.DiskPathFor("/pool", "web-01") || iso != storage.ISOPathFor("/pool", "web-01") {
		t.Errorf("PathsFor = %q/%q, want the same naming convention the rest of the daemon uses", disk, iso)
	}
}
