package stats

import (
	"testing"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/system"
	"github.com/nanocloud-io/nanocloud/internal/vm"
)

type fakeHV struct {
	samples []vm.VMSample
	err     error
}

func (f *fakeHV) SampleAll() ([]vm.VMSample, error) { return f.samples, f.err }

type fakeSys struct {
	ticks           system.CPUTicks
	usedMB, totalMB uint64
}

func (f *fakeSys) Ticks() (system.CPUTicks, error)      { return f.ticks, nil }
func (f *fakeSys) MemoryStats() (uint64, uint64, error) { return f.usedMB, f.totalMB, nil }

func TestVMCPUPercent(t *testing.T) {
	base := time.Unix(1000, 0)
	tests := []struct {
		name          string
		prevNs, curNs uint64
		elapsed       time.Duration
		nrVCPU        uint16
		want          float32
	}{
		{"half a core busy over 5s", 0, 2_500_000_000, 5 * time.Second, 1, 50},
		{"fully busy across 2 vcpus", 0, 10_000_000_000, 5 * time.Second, 2, 100},
		{"counter reset (VM restarted)", 5_000_000_000, 100_000_000, 5 * time.Second, 1, 0},
		{"zero elapsed (duplicate tick)", 0, 1_000_000_000, 0, 1, 0},
		{"zero vcpus", 0, 1_000_000_000, 5 * time.Second, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := vmCPUPercent(tc.prevNs, base, tc.curNs, base.Add(tc.elapsed), tc.nrVCPU)
			if got != tc.want {
				t.Errorf("vmCPUPercent() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRing_EvictsOldestAndFiltersBySince(t *testing.T) {
	r := newRing(3)
	for i := int64(1); i <= 5; i++ {
		r.add(Sample{At: i})
	}
	if got := r.since(0); len(got) != 3 || got[0].At != 3 || got[2].At != 5 {
		t.Fatalf("since(0) = %+v, want ats [3 4 5]", got)
	}
	if got := r.since(3); len(got) != 2 || got[0].At != 4 {
		t.Errorf("since(3) = %+v, want ats [4 5]", got)
	}
}

// Two ticks 37s apart check that the sampler keeps the previous sample, uses
// the real elapsed time rather than the interval, and records true timestamps.
func TestSampler_HoldsStateAcrossTicksAndKeepsTrueTimestamps(t *testing.T) {
	t0 := time.Unix(1000, 0)
	hv := &fakeHV{samples: []vm.VMSample{{Name: "web", At: t0, CPUTimeNs: 1_000_000_000, NrVCPU: 1, MemUsedKB: 524_288, MaxMemKB: 2_097_152}}}
	sys := &fakeSys{ticks: system.CPUTicks{User: 100, Idle: 900}, usedMB: 1, totalMB: 2}
	s := New(hv, sys)
	s.tick(t0)

	t1 := t0.Add(37 * time.Second)
	hv.samples[0].At, hv.samples[0].CPUTimeNs = t1, 1_000_000_000+3_700_000_000 // 3.7s busy of 1 vCPU over 37s = 10%
	sys.ticks = system.CPUTicks{User: 190, Idle: 900}                           // +90 busy of +90 total ticks = 100%
	s.tick(t1)

	got := s.VM("web", 0)
	if len(got) != 2 || got[0].At != t0.Unix() || got[1].At != t1.Unix() {
		t.Fatalf("VM samples = %+v, want real timestamps [%d %d]", got, t0.Unix(), t1.Unix())
	}
	if got[0].CPUPercent != 0 {
		t.Errorf("first sample CPUPercent = %v, want 0 (no previous sample yet)", got[0].CPUPercent)
	}
	if got[0].MemUsedMB != 512 || got[0].MemTotalMB != 2048 {
		t.Errorf("memory = %d/%d MB, want 512/2048 — KiB converted at the sampler, used is the guest figure", got[0].MemUsedMB, got[0].MemTotalMB)
	}
	if want := float32(10); got[1].CPUPercent < want-0.5 || got[1].CPUPercent > want+0.5 {
		t.Errorf("second sample CPUPercent = %v, want ~%v", got[1].CPUPercent, want)
	}
	if hostGot := s.Host(0); len(hostGot) != 2 || hostGot[1].CPUPercent != 100 {
		t.Errorf("host samples = %+v, want second at 100%%", hostGot)
	}

	// Host counters going backwards must read 0, not an underflowed delta.
	sys.ticks = system.CPUTicks{User: 1, Idle: 1}
	s.tick(t1.Add(5 * time.Second))
	if hostGot := s.Host(0); len(hostGot) != 3 || hostGot[2].CPUPercent != 0 {
		t.Errorf("host sample after counter reset = %+v, want 0%%", hostGot)
	}
}

// A deleted VM's ring and counters are dropped; surviving VMs keep theirs.
func TestSampler_PrunesVMsThatVanish(t *testing.T) {
	hv := &fakeHV{samples: []vm.VMSample{
		{Name: "web", At: time.Unix(1000, 0), NrVCPU: 1},
		{Name: "db", At: time.Unix(1000, 0), NrVCPU: 1},
	}}
	s := New(hv, &fakeSys{})
	s.tick(time.Unix(1000, 0))

	hv.samples = hv.samples[:1] // "db" deleted
	s.tick(time.Unix(1005, 0))

	if got := s.VM("db", 0); got != nil {
		t.Errorf("VM(\"db\") after delete = %+v, want nil", got)
	}
	if got := s.VM("web", 0); len(got) != 2 {
		t.Errorf("VM(\"web\") = %d samples, want 2 — a surviving VM keeps its history", len(got))
	}
	s.mu.Lock()
	nRings, nPrev := len(s.vmRings), len(s.prevVMs)
	s.mu.Unlock()
	if nRings != 1 || nPrev != 1 {
		t.Errorf("vmRings=%d prevVMs=%d, want 1 and 1 — deleted VM's state was not reclaimed", nRings, nPrev)
	}
}

// A Hypervisor error skips the VM sample but not the host one, and the next
// successful tick resumes.
func TestSampler_SkipsVMTickOnHypervisorErrorThenRecovers(t *testing.T) {
	hv := &fakeHV{err: vm.ErrNotConnected}
	sys := &fakeSys{ticks: system.CPUTicks{User: 10, Idle: 90}, usedMB: 1, totalMB: 2}
	s := New(hv, sys)
	s.tick(time.Unix(1000, 0))

	if got := s.VM("web", 0); got != nil {
		t.Errorf("VM samples after a failed tick = %v, want nil", got)
	}
	if got := s.Host(0); len(got) != 1 {
		t.Errorf("host samples = %d, want 1 — host ticks independently of the hypervisor", len(got))
	}

	hv.err = nil
	hv.samples = []vm.VMSample{{Name: "web", At: time.Unix(1005, 0), NrVCPU: 1}}
	s.tick(time.Unix(1005, 0))
	if got := s.VM("web", 0); len(got) != 1 {
		t.Errorf("VM samples after recovery = %d, want 1", len(got))
	}
}
