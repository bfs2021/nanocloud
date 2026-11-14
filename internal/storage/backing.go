package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// BackingFile returns the absolute path of the backing image of the qcow2
// overlay at path, or "" for a standalone qcow2 or a raw disk. It reads the
// qcow2 header directly, so qemu-img is not needed.
func BackingFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("storage: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only; close error is not actionable

	// magic(4) version(4) backing_file_offset(8) backing_file_size(4)
	var hdr [20]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return "", nil // too short to be a qcow2
		}
		return "", fmt.Errorf("storage: read %s: %w", path, err)
	}
	if string(hdr[:4]) != "QFI\xfb" {
		return "", nil
	}
	off := binary.BigEndian.Uint64(hdr[8:16])
	size := binary.BigEndian.Uint32(hdr[16:20])
	if off == 0 || size == 0 {
		return "", nil
	}
	if size > 1023 { // qcow2's own limit on the name
		return "", fmt.Errorf("storage: %s: backing file name of %d bytes", path, size)
	}
	name := make([]byte, size)
	if _, err := f.ReadAt(name, int64(off)); err != nil {
		return "", fmt.Errorf("storage: read backing file name of %s: %w", path, err)
	}
	backing := string(name)
	// A relative name is relative to the overlay, as qemu resolves it.
	if !filepath.IsAbs(backing) {
		backing = filepath.Join(filepath.Dir(path), backing)
	}
	return filepath.Clean(backing), nil
}
