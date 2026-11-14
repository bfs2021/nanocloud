//go:build !darwin

package storage

// DiskExt is the file extension of a VM disk on this platform.
const DiskExt = ".qcow2"

// removeSidecars does nothing for qcow2: its snapshots live inside the image
// and go with it.
func removeSidecars(string) {}
