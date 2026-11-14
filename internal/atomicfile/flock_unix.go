//go:build !windows

package atomicfile

import (
	"os"

	"golang.org/x/sys/unix"
)

// lock takes a POSIX advisory lock on the sidecar — exclusive for a write
// cycle, shared for a read.
func lock(f *os.File, exclusive bool) error {
	how := unix.LOCK_SH
	if exclusive {
		how = unix.LOCK_EX
	}
	return unix.Flock(int(f.Fd()), how)
}

func unlock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
