//go:build darwin && cgo

package system

import "testing"

func TestDarwinReaderReportsTheHost(t *testing.T) {
	r := New()
	if r.Hostname() == "unknown" {
		t.Error("Hostname() = unknown")
	}
	used, total, err := r.MemoryStats()
	if err != nil || total == 0 || used == 0 || used > total {
		t.Fatalf("MemoryStats() = %d used, %d total, %v", used, total, err)
	}
	ticks, err := r.Ticks()
	if err != nil || ticks.Total() == 0 || ticks.Idle == 0 {
		t.Fatalf("Ticks() = %+v, %v", ticks, err)
	}
	if ticks.Busy() > ticks.Total() {
		t.Fatalf("busy %d exceeds total %d", ticks.Busy(), ticks.Total())
	}
}
