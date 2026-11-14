// Package stats samples host and per-VM CPU and memory every 5s and keeps an
// hour of history in memory, so stats requests are served from the last
// sample instead of blocking on a measurement window. History does not survive
// a restart.
package stats

import (
	"context"
	"sync"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/system"
	"github.com/nanocloud-io/nanocloud/internal/vm"
)

const (
	interval  = 5 * time.Second
	retention = time.Hour
	// capacity is how many samples one series retains: 1h at one every 5s.
	capacity = int(retention / interval)
)

// Sample is one point on a CPU/memory timeline. An hour of samples is about
// 17 KB per series.
type Sample struct {
	At         int64   `json:"at"`
	CPUPercent float32 `json:"cpu_percent"`
	MemUsedMB  uint32  `json:"mem_used_mb"`
	MemTotalMB uint32  `json:"mem_total_mb"`
}

// Hypervisor is the subset of *vm.Manager the sampler calls, so tests can fake
// it without libvirt.
type Hypervisor interface {
	SampleAll() ([]vm.VMSample, error)
}

// SystemReader is the subset of *system.Reader the sampler calls.
type SystemReader interface {
	Ticks() (system.CPUTicks, error)
	MemoryStats() (usedMB, totalMB uint64, err error)
}

// prevVM is the last raw counters seen for one VM, so CPU% can be derived
// from the delta between ticks.
type prevVM struct {
	cpuTimeNs uint64
	at        time.Time
}

// Sampler polls host and VM counters on its own tick and keeps a bounded
// history per series. It never dials libvirt itself; a Hypervisor error such
// as vm.ErrNotConnected skips that tick's VM sample.
type Sampler struct {
	hv  Hypervisor
	sys SystemReader

	host *ring

	mu      sync.Mutex
	vmRings map[string]*ring
	prevVMs map[string]prevVM
	prevSys system.CPUTicks
	haveSys bool
}

// New returns a Sampler. Call Run to start its tick.
func New(hv Hypervisor, sys SystemReader) *Sampler {
	return &Sampler{
		hv: hv, sys: sys,
		host:    newRing(capacity),
		vmRings: make(map[string]*ring),
		prevVMs: make(map[string]prevVM),
	}
}

// Run ticks every interval until ctx is canceled, taking the first sample
// immediately.
func (s *Sampler) Run(ctx context.Context) {
	s.tick(time.Now())
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(time.Now())
		}
	}
}

func (s *Sampler) tick(now time.Time) {
	s.sampleHost(now)
	s.sampleVMs(now)
}

func (s *Sampler) sampleHost(now time.Time) {
	ticks, err := s.sys.Ticks()
	if err != nil {
		return
	}
	usedMB, totalMB, err := s.sys.MemoryStats()
	if err != nil {
		return
	}

	s.mu.Lock()
	prev, have := s.prevSys, s.haveSys
	s.prevSys, s.haveSys = ticks, true
	s.mu.Unlock()

	// /proc/stat counters are cumulative ticks, so CPU% is busy over total
	// between two samples.
	var pct float32
	if have && ticks.Total() > prev.Total() {
		pct = clampPercent(float64(ticks.Busy()-prev.Busy()) / float64(ticks.Total()-prev.Total()) * 100)
	}
	s.host.add(Sample{At: now.Unix(), CPUPercent: pct, MemUsedMB: uint32(usedMB), MemTotalMB: uint32(totalMB)})
}

func (s *Sampler) sampleVMs(now time.Time) {
	samples, err := s.hv.SampleAll()
	if err != nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	live := make(map[string]struct{}, len(samples))
	for _, vs := range samples {
		live[vs.Name] = struct{}{}

		prev, have := s.prevVMs[vs.Name]
		s.prevVMs[vs.Name] = prevVM{cpuTimeNs: vs.CPUTimeNs, at: vs.At}

		var pct float32
		if have {
			pct = vmCPUPercent(prev.cpuTimeNs, prev.at, vs.CPUTimeNs, vs.At, vs.NrVCPU)
		}

		r, ok := s.vmRings[vs.Name]
		if !ok {
			r = newRing(capacity)
			s.vmRings[vs.Name] = r
		}
		r.add(Sample{At: now.Unix(), CPUPercent: pct, MemUsedMB: uint32(vs.MemUsedKB / 1024), MemTotalMB: uint32(vs.MaxMemKB / 1024)})
	}

	// Drop VMs libvirt no longer defines. SampleAll lists inactive domains
	// too, so a stopped VM keeps its history and only a deleted one goes.
	for name := range s.vmRings {
		if _, ok := live[name]; !ok {
			delete(s.vmRings, name)
			delete(s.prevVMs, name)
		}
	}
}

// Host returns host samples recorded after the given unix timestamp, oldest
// first. Pass 0 for the full retained window. A nil *Sampler returns nil.
func (s *Sampler) Host(since int64) []Sample {
	if s == nil {
		return nil
	}
	return s.host.since(since)
}

// VM returns the named VM's samples recorded after the given unix timestamp,
// oldest first, or nil if that VM has never been sampled.
func (s *Sampler) VM(name string, since int64) []Sample {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	r, ok := s.vmRings[name]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return r.since(since)
}

// vmCPUPercent is the CPU time consumed between two samples as a percentage
// of the wall time available across nrVCPU cores. A counter that went
// backwards or a non-positive elapsed time reads as 0.
func vmCPUPercent(prevNs uint64, prevAt time.Time, curNs uint64, curAt time.Time, nrVCPU uint16) float32 {
	if nrVCPU == 0 || !curAt.After(prevAt) || curNs < prevNs {
		return 0
	}
	elapsedNs := float64(curAt.Sub(prevAt).Nanoseconds())
	pct := float64(curNs-prevNs) / (elapsedNs * float64(nrVCPU)) * 100
	return clampPercent(pct)
}

func clampPercent(pct float64) float32 {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return float32(pct)
}

// ring is a fixed-capacity, timestamp-ordered sample buffer; once full, each
// add overwrites the oldest entry.
type ring struct {
	mu   sync.Mutex
	buf  []Sample
	next int // next write index, mod len(buf)
	n    int // valid entries held, caps at len(buf)
}

func newRing(capacity int) *ring {
	return &ring{buf: make([]Sample, capacity)}
}

func (r *ring) add(s Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.next] = s
	r.next = (r.next + 1) % len(r.buf)
	if r.n < len(r.buf) {
		r.n++
	}
}

func (r *ring) since(unix int64) []Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Sample, 0, r.n)
	start := (r.next - r.n + len(r.buf)) % len(r.buf)
	for i := 0; i < r.n; i++ {
		s := r.buf[(start+i)%len(r.buf)]
		if s.At > unix {
			out = append(out, s)
		}
	}
	return out
}
