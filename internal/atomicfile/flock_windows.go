//go:build windows

package atomicfile

import (
	"errors"
	"os"
)

// The Windows build exists only so dependent packages compile.
var errNotSupported = errors.New("atomicfile: file locking is not supported on Windows")

func lock(_ *os.File, _ bool) error { return errNotSupported }

func unlock(_ *os.File) error { return errNotSupported }
