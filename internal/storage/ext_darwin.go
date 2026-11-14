package storage

import "os"

// DiskExt is the file extension of a VM disk on this platform. Every disk is a
// sparse raw file, since Virtualization.framework boots raw images only.
const DiskExt = ".raw"

// removeSidecars removes a deleted disk's snapshots, which live beside it.
func removeSidecars(path string) { _ = os.RemoveAll(SnapshotDir(path)) }
