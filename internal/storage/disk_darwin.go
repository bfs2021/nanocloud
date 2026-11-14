package storage

import "golang.org/x/sys/unix"

// On macOS the disk operations work on raw images (see disk_raw.go), with the
// same contracts as the qemu-img versions in disk_qemu.go.

// AllocateDisk makes destPath a sizeGB disk holding backingFile's contents,
// as an APFS clone of the image's raw form.
func AllocateDisk(backingFile, destPath string, sizeGB int) error {
	return allocateRaw(backingFile, destPath, sizeGB)
}

// CreateDisk makes a blank, sparse sizeGB disk at path.
func CreateDisk(path string, sizeGB int) error { return createRaw(path, sizeGB) }

// CopyDisk writes a standalone copy of srcPath to destPath.
func CopyDisk(srcPath, destPath string) error { return copyRaw(srcPath, destPath) }

// ResizeDisk grows the disk at path to sizeGB, and never shrinks it.
func ResizeDisk(path string, sizeGB int) error { return resizeRaw(path, sizeGB) }

// InspectDisk reports a disk's size.
func InspectDisk(path string) (DiskInfo, error) { return inspectRaw(path) }

// clonefile makes dst a copy-on-write clone of src (APFS).
func clonefile(src, dst string) error {
	return unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW)
}
