//go:build !darwin

package storage

import "errors"

var errNoClone = errors.New("file cloning unsupported")

// clonefile is APFS-only; elsewhere the raw disk code falls back to a sparse
// copy. Only the raw-disk tests reach it off macOS.
func clonefile(_, _ string) error { return errNoClone }
