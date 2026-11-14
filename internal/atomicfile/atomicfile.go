// Package atomicfile holds a small state file whose replacements are
// all-or-nothing and whose read-modify-write cycles are serialised across
// goroutines and processes.
//
//   - A mutex serialises goroutines; a flock serialises other processes.
//   - The flock is taken on a sidecar (path + ".lock"), never on the live
//     file. Renaming over the live file swaps its inode, so a lock held on it
//     would stop guarding anything.
//   - Writes go to a temp file in the same directory and are renamed into place.
//   - The parent directory is fsynced after the rename. Without it the rename
//     is atomic but not durable, and a power cut can revert the file.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// File is one such state file. The zero value is not usable; call New.
type File struct {
	mu sync.Mutex
	// path is the live file. lockPath is the sidecar flocked around every
	// access; it is never renamed, so its inode is stable.
	path     string
	lockPath string
	// mode is the permission every replacement is written with.
	mode os.FileMode
}

// New returns a File backed by path. The file need not exist yet; it reads as
// empty and is created by the first Update that writes.
func New(path string) *File {
	return &File{path: path, lockPath: path + ".lock", mode: 0o644}
}

// NewPrivate is New for a file only its owner may read, such as one holding
// passwords or keys.
func NewPrivate(path string) *File {
	return &File{path: path, lockPath: path + ".lock", mode: 0o600}
}

// Path returns the live file's path.
func (f *File) Path() string { return f.path }

// Read returns the current contents under a shared lock. A missing file reads
// as nil with no error, so a fresh install is not treated as a failure.
func (f *File) Read() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	lf, err := f.acquire(false)
	if err != nil {
		return nil, err
	}
	defer release(lf)

	return f.read()
}

// Update runs fn on the current contents and replaces the file with what fn
// returns, holding an exclusive lock across the whole cycle. If fn returns a
// nil slice the file is left untouched; if it returns an error, nothing is
// written and the error is returned unchanged.
func (f *File) Update(fn func(current []byte) ([]byte, error)) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	lf, err := f.acquire(true)
	if err != nil {
		return err
	}
	defer release(lf)

	current, err := f.read()
	if err != nil {
		return err
	}
	next, err := fn(current)
	if err != nil || next == nil {
		return err
	}
	return f.replace(next)
}

// read loads the file without locking. Callers must hold the lock already.
func (f *File) read() ([]byte, error) {
	data, err := os.ReadFile(f.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("atomicfile: read %q: %w", f.path, err)
	}
	return data, nil
}

// acquire opens and locks the sidecar, creating the parent directory first so
// dev and E2E runs can point a store at a directory that does not exist yet.
func (f *File) acquire(exclusive bool) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(f.lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("atomicfile: create directory for %q: %w", f.lockPath, err)
	}
	lf, err := os.OpenFile(f.lockPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("atomicfile: open lock file %q: %w", f.lockPath, err)
	}
	if err := lock(lf, exclusive); err != nil {
		lf.Close() //nolint:errcheck // already returning a lock error; close is best-effort
		return nil, fmt.Errorf("atomicfile: lock %q: %w", f.lockPath, err)
	}
	return lf, nil
}

func release(lf *os.File) {
	unlock(lf) //nolint:errcheck // best-effort; the fd is closed next
	lf.Close() //nolint:errcheck // best-effort lock file cleanup
}

// replace writes data into a temp file and renames it over the live one.
// Callers must hold an exclusive lock already.
func (f *File) replace(data []byte) error {
	// Uniquely named and in the destination directory, so concurrent writers
	// cannot collide and rename stays on one filesystem.
	tf, err := os.CreateTemp(filepath.Dir(f.path), "."+filepath.Base(f.path)+"-*")
	if err != nil {
		return fmt.Errorf("atomicfile: create temp file near %q: %w", f.path, err)
	}
	tmp := tf.Name()
	cleanup := func() {
		tf.Close()     //nolint:errcheck // best-effort cleanup on an error path
		os.Remove(tmp) //nolint:errcheck
	}

	if _, err := tf.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("atomicfile: write temp file %q: %w", tmp, err)
	}
	// CreateTemp makes the file 0600.
	if err := tf.Chmod(f.mode); err != nil {
		cleanup()
		return fmt.Errorf("atomicfile: chmod temp file %q: %w", tmp, err)
	}
	if err := tf.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("atomicfile: sync temp file %q: %w", tmp, err)
	}
	if err := tf.Close(); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("atomicfile: close temp file %q: %w", tmp, err)
	}

	if err := os.Rename(tmp, f.path); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("atomicfile: rename %q → %q: %w", tmp, f.path, err)
	}
	return syncDir(f.path)
}

// syncDir fsyncs the directory containing path so a completed rename survives
// a power cut.
func syncDir(path string) error {
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("atomicfile: open parent dir of %q: %w", path, err)
	}
	defer d.Close() //nolint:errcheck // read-only dir handle; close error is not actionable
	if err := d.Sync(); err != nil {
		return fmt.Errorf("atomicfile: fsync parent dir of %q: %w", path, err)
	}
	return nil
}
