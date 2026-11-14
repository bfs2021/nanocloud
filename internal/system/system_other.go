//go:build !linux && !(darwin && cgo)

// Package system provides host-level metrics. On other platforms the Reader
// is a no-op stub.
package system

// Reader is a no-op stub on non-Linux platforms.
type Reader struct{}

// New returns a no-op Reader.
func New() *Reader { return &Reader{} }

// Hostname always returns "unknown" on non-Linux platforms.
func (r *Reader) Hostname() string { return "unknown" }

// MemoryStats always returns zeros on non-Linux platforms.
func (r *Reader) MemoryStats() (usedMB, totalMB uint64, err error) { return 0, 0, nil }

// Ticks always returns a zero CPUTicks on non-Linux platforms.
func (r *Reader) Ticks() (CPUTicks, error) { return CPUTicks{}, nil }
