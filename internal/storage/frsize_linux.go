//go:build linux

package storage

import "syscall"

// blockSizeForStatfs returns the fundamental block size that Blocks/Bfree/Bavail
// are counted in. On Linux this is Frsize (f_frsize per POSIX), not Bsize.
func blockSizeForStatfs(st syscall.Statfs_t) uint64 {
	return uint64(st.Frsize)
}
