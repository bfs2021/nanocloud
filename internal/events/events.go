// Package events provides the operational feed: a fixed-size, in-memory ring
// of things nano-cloud did on its own, such as restarting a VM or losing
// libvirt.
//
// The ring lives as long as the process, like the reconcile-loop state it
// reports on. Readers poll Since with the cursor they last received.
package events

import (
	"sync"
	"time"
)

// DefaultCapacity is how many events the ring retains, roughly 200 KB.
const DefaultCapacity = 1000

const (
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// Event kinds. The frontend switches on this closed set.
const (
	KindVMRestarted      = "vm.restarted"
	KindVMRecovered      = "vm.recovered"
	KindVMGaveUp         = "vm.gave_up"
	KindVMDegraded       = "vm.degraded"
	KindVMRedefined      = "vm.redefined"
	KindVMPoweredOff     = "vm.powered_off"
	KindLibvirtLost      = "libvirt.lost"
	KindLibvirtConnected = "libvirt.connected"
	KindSpecAdopted      = "spec.adopted"
	KindServiceReady     = "service.ready"
	KindServiceFailed    = "service.failed"
	// KindServiceProgress is a step of a service's build, or a retry.
	KindServiceProgress = "service.progress"
	// KindServiceAlert is an alert of a monitoring service starting to fire,
	// or ending.
	KindServiceAlert = "service.alert"
)

// Event is one entry in the feed. Seq and At are assigned by Append. Message
// is rendered as-is and Detail carries the underlying cause.
type Event struct {
	// Seq is also the polling cursor. It starts at 1 and resets when the
	// daemon restarts.
	Seq     uint64    `json:"seq"`
	At      time.Time `json:"at"`
	Level   string    `json:"level"`
	Kind    string    `json:"kind"`
	VM      string    `json:"vm,omitempty"`
	Message string    `json:"message"`
	Detail  string    `json:"detail,omitempty"`
}

// Ring is a fixed-capacity, concurrency-safe event buffer. Once full, each
// append overwrites the oldest entry.
//
// A nil *Ring is valid and both methods are no-ops, so an unwired feed cannot
// panic the reconcile loop.
type Ring struct {
	mu sync.Mutex
	// buf is indexed by (Seq-1) % len(buf); its length is the capacity.
	buf  []Event
	next uint64
}

// New returns a Ring retaining the last capacity events. A non-positive
// capacity falls back to DefaultCapacity.
func New(capacity int) *Ring {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Ring{buf: make([]Event, capacity), next: 1}
}

// Append records e, stamping it with the next sequence number and the time.
// Callers should emit on state transitions only, not on every tick.
func (r *Ring) Append(e Event) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	e.Seq = r.next
	e.At = time.Now().UTC()
	r.buf[(e.Seq-1)%uint64(len(r.buf))] = e
	r.next++
}

// Since returns the events recorded after cursor, oldest first, with the
// cursor for the next call. A limit above zero caps the page, and the returned
// cursor then resumes where the page stopped. Pass 0 to read everything held.
//
// A cursor outside the retained window, below it or ahead of it, resumes at
// the oldest event held. A cursor ahead of the ring means the daemon restarted
// and sequence numbers began again at 1.
func (r *Ring) Since(cursor uint64, limit int) ([]Event, uint64) {
	if r == nil {
		return nil, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	capacity := uint64(len(r.buf))
	latest := r.next - 1
	if cursor > latest {
		cursor = 0
	}

	oldest := uint64(1)
	if latest > capacity {
		oldest = latest - capacity + 1
	}
	start := max(cursor+1, oldest)

	out := make([]Event, 0, pageSize(start, latest, limit))
	for seq := start; seq <= latest; seq++ {
		if limit > 0 && len(out) == limit {
			break
		}
		out = append(out, r.buf[(seq-1)%capacity])
	}
	if n := len(out); n > 0 {
		cursor = out[n-1].Seq
	}
	return out, cursor
}

func pageSize(start, latest uint64, limit int) int {
	if start > latest {
		return 0
	}
	n := latest - start + 1
	if limit > 0 && uint64(limit) < n {
		return limit
	}
	return int(n)
}
