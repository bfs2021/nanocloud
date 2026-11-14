//go:build !linux

package storage

import "syscall"

// blockSizeForStatfs returns the fundamental block size that Blocks/Bfree/Bavail
// are counted in. On Darwin (and other non-Linux) Bsize is the correct unit.
func blockSizeForStatfs(st syscall.Statfs_t) uint64 {
	return uint64(st.Bsize)
}
