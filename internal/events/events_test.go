package events_test

import (
	"sync"
	"testing"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/events"
)

func fill(r *events.Ring, n int) {
	for i := 1; i <= n; i++ {
		r.Append(events.Event{Kind: events.KindVMRestarted})
	}
}

func seqs(evs []events.Event) []uint64 {
	out := make([]uint64, len(evs))
	for i, e := range evs {
		out[i] = e.Seq
	}
	return out
}

func equalSeqs(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSince(t *testing.T) {
	tests := []struct {
		name       string
		capacity   int
		appends    int
		cursor     uint64
		limit      int
		wantSeqs   []uint64
		wantCursor uint64
	}{
		{name: "empty ring", capacity: 10},
		{name: "cursor 0 reads everything", capacity: 10, appends: 3, wantSeqs: []uint64{1, 2, 3}, wantCursor: 3},
		{name: "resumes after the cursor", capacity: 10, appends: 5, cursor: 3, wantSeqs: []uint64{4, 5}, wantCursor: 5},
		{name: "cursor at head returns nothing", capacity: 10, appends: 5, cursor: 5, wantCursor: 5},
		{name: "limit caps the page", capacity: 10, appends: 5, limit: 2, wantSeqs: []uint64{1, 2}, wantCursor: 2},
		// Once full, e1/e2 are overwritten and a cursor into the dropped
		// window resumes at the oldest event held.
		{name: "oldest overwritten when full", capacity: 3, appends: 5, wantSeqs: []uint64{3, 4, 5}, wantCursor: 5},
		{name: "cursor below retained window", capacity: 3, appends: 5, cursor: 1, wantSeqs: []uint64{3, 4, 5}, wantCursor: 5},
		// A cursor saved before a daemon restart points past the head.
		{name: "cursor ahead of the ring is stale", capacity: 10, appends: 2, cursor: 450, wantSeqs: []uint64{1, 2}, wantCursor: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := events.New(tc.capacity)
			fill(r, tc.appends)

			evs, cursor := r.Since(tc.cursor, tc.limit)

			if got := seqs(evs); !equalSeqs(got, tc.wantSeqs) {
				t.Errorf("seqs = %v, want %v", got, tc.wantSeqs)
			}
			if cursor != tc.wantCursor {
				t.Errorf("cursor = %d, want %d", cursor, tc.wantCursor)
			}
		})
	}
}

func TestAppend_StampsSequenceAndTimeOverAnythingTheCallerSet(t *testing.T) {
	r := events.New(10)

	before := time.Now()
	r.Append(events.Event{Seq: 99, At: time.Unix(0, 0), Level: events.LevelWarn, Kind: events.KindVMGaveUp, VM: "web-01", Message: "gave up"})
	after := time.Now()

	evs, _ := r.Since(0, 0)
	got := evs[0]
	if got.Seq != 1 {
		t.Errorf("Seq = %d, want 1 — the ring assigns it", got.Seq)
	}
	if got.At.Before(before) || got.At.After(after) {
		t.Errorf("At = %v, want within [%v, %v]", got.At, before, after)
	}
	if got.Level != events.LevelWarn || got.Kind != events.KindVMGaveUp || got.VM != "web-01" || got.Message != "gave up" {
		t.Errorf("payload = %+v, want it preserved verbatim", got)
	}
}

// A paging client must not skip events between two calls.
func TestSince_ReturnedCursorResumesWhereThePageStopped(t *testing.T) {
	r := events.New(10)
	fill(r, 5)

	_, cursor := r.Since(0, 2)
	next, _ := r.Since(cursor, 2)

	if got := seqs(next); !equalSeqs(got, []uint64{3, 4}) {
		t.Errorf("second page = %v, want [3 4]", got)
	}
}

// An unwired feed must not panic the reconcile loop.
func TestNilRing_IsInert(t *testing.T) {
	var r *events.Ring
	r.Append(events.Event{Kind: events.KindVMGaveUp})

	if evs, cursor := r.Since(0, 0); len(evs) != 0 || cursor != 0 {
		t.Errorf("Since = (%v, %d), want (nil, 0)", evs, cursor)
	}
}

func TestNew_NonPositiveCapacityFallsBackToDefault(t *testing.T) {
	r := events.New(0)
	for i := 0; i <= events.DefaultCapacity; i++ {
		r.Append(events.Event{Kind: events.KindVMRestarted})
	}

	if evs, _ := r.Since(0, 0); len(evs) != events.DefaultCapacity {
		t.Errorf("retained %d events, want %d", len(evs), events.DefaultCapacity)
	}
}

func TestSince_ReturnsACopyCallersCannotCorrupt(t *testing.T) {
	r := events.New(3)
	r.Append(events.Event{Kind: events.KindVMRestarted, Message: "original"})

	evs, _ := r.Since(0, 0)
	evs[0].Message = "mutated"

	if again, _ := r.Since(0, 0); again[0].Message != "original" {
		t.Errorf("ring event = %q, want %q", again[0].Message, "original")
	}
}

// Run with -race: the reconcile loop appends while HTTP handlers read.
func TestRing_ConcurrentAppendAndSince(t *testing.T) {
	r := events.New(64)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.Append(events.Event{Kind: events.KindVMRestarted})
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var cursor uint64
			for j := 0; j < 100; j++ {
				_, cursor = r.Since(cursor, 10)
			}
		}()
	}
	wg.Wait()

	if _, cursor := r.Since(0, 0); cursor != 800 {
		t.Errorf("cursor = %d, want 800 — every append must get a unique sequence", cursor)
	}
}
