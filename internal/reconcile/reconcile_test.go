package reconcile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	libvirt "github.com/digitalocean/go-libvirt"

	"github.com/nanocloud-io/nanocloud/internal/events"
	"github.com/nanocloud-io/nanocloud/internal/hostnet"
	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/storage"
	"github.com/nanocloud-io/nanocloud/internal/vm"
)

// TestDecide covers backoff, give-up, the stability window, paused and
// restart_policy in one table: all are a function of one tick's inputs.
func TestDecide(t *testing.T) {
	now := time.Now()
	on, always, never := metadata.RestartOnFailure, metadata.RestartAlways, metadata.RestartNever

	tests := []struct {
		name string
		in   tickInput
		want tickAction
	}{
		{"absent from libvirt, no disk left -> degraded, nothing to rebuild onto", tickInput{present: false}, actionDegraded},
		{"absent from libvirt, disk intact -> rebuild it", tickInput{present: false, diskExists: true, now: now}, actionDefine},
		{
			// Without these two the loop would call DomainDefineXML every tick forever.
			"absent, disk intact, backoff not elapsed -> wait",
			tickInput{present: false, diskExists: true, now: now, nextAttempt: now.Add(5 * time.Second)},
			actionBackoff,
		},
		{
			"absent, disk intact, already gave up -> stay given up",
			tickInput{present: false, diskExists: true, now: now, gaveUp: true},
			actionGaveUp,
		},
		{
			// Existence is not power state; attemptDefine decides whether to boot it.
			"absent, disk intact, policy never -> still rebuild it",
			tickInput{present: false, diskExists: true, policy: never, now: now},
			actionDefine,
		},
		{"running, just started -> wait", tickInput{present: true, state: libvirt.DomainRunning, runningSince: now, now: now}, actionNone},
		{"running, not yet stable -> wait", tickInput{present: true, state: libvirt.DomainRunning, runningSince: now.Add(-30 * time.Second), now: now}, actionNone},
		{"running, stable 60s -> converge, resets attempts", tickInput{present: true, state: libvirt.DomainRunning, runningSince: now.Add(-61 * time.Second), now: now}, actionConverge},
		{"blocked counts as running", tickInput{present: true, state: libvirt.DomainBlocked, runningSince: now.Add(-61 * time.Second), now: now}, actionConverge},
		{"paused -> user's call, skip", tickInput{present: true, state: libvirt.DomainPaused}, actionPaused},
		{"pmsuspended -> skip", tickInput{present: true, state: libvirt.DomainPmsuspended}, actionPaused},
		{"mid-shutdown -> wait for it to settle", tickInput{present: true, state: libvirt.DomainShutdown}, actionNone},
		{
			"shutoff, external kill, on-failure, eligible now -> start",
			tickInput{present: true, state: libvirt.DomainShutoff, reason: int32(libvirt.DomainShutoffDestroyed), policy: on, now: now},
			actionStart,
		},
		{
			"shutoff, external kill, backoff not elapsed -> wait",
			tickInput{present: true, state: libvirt.DomainShutoff, reason: int32(libvirt.DomainShutoffDestroyed), policy: on, now: now, nextAttempt: now.Add(5 * time.Second)},
			actionBackoff,
		},
		{
			"shutoff, external kill, already gave up -> stay given up",
			tickInput{present: true, state: libvirt.DomainShutoff, reason: int32(libvirt.DomainShutoffDestroyed), policy: on, now: now, gaveUp: true},
			actionGaveUp,
		},
		{
			"shutoff, clean in-guest poweroff, on-failure -> honor as intent",
			tickInput{present: true, state: libvirt.DomainShutoff, reason: int32(libvirt.DomainShutoffShutdown), policy: on, now: now},
			actionHonorPoweroff,
		},
		{
			"shutoff, clean poweroff, policy always -> restart anyway",
			tickInput{present: true, state: libvirt.DomainShutoff, reason: int32(libvirt.DomainShutoffShutdown), policy: always, now: now},
			actionStart,
		},
		{
			"shutoff, policy never -> leave it alone",
			tickInput{present: true, state: libvirt.DomainShutoff, reason: int32(libvirt.DomainShutoffDestroyed), policy: never, now: now},
			actionNone,
		},
		{
			"crashed, on-failure -> restart",
			tickInput{present: true, state: libvirt.DomainCrashed, policy: on, now: now},
			actionStart,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := decide(tc.in); got != tc.want {
				t.Errorf("decide(%+v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestBackoffFor_CapsAndOrders(t *testing.T) {
	prev := time.Duration(0)
	for n := 1; n <= maxAttempts; n++ {
		d := backoffFor(n)
		if d > maxBackoff+maxBackoff/10 { // allow for jitter headroom
			t.Errorf("backoffFor(%d) = %s, exceeds cap %s", n, d, maxBackoff)
		}
		if n > 1 && d < prev/2 {
			t.Errorf("backoffFor(%d) = %s, want roughly increasing from backoffFor(%d) = %s", n, d, n-1, prev)
		}
		prev = d
	}
}

func newTestStore(t *testing.T) *metadata.Store {
	t.Helper()
	return metadata.New(filepath.Join(t.TempDir(), "metadata.json"))
}

// seedPool returns a pool holding an empty boot disk and seed ISO for name.
func seedPool(t *testing.T, name string) string {
	t.Helper()
	pool := t.TempDir()
	disk, iso, ok := storage.PathsFor(pool, name)
	if !ok {
		t.Fatalf("PathsFor(%q, %q) refused a real pool", pool, name)
	}
	for _, p := range []string{disk, iso} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}
	return pool
}

// fakeHV models a guest that boots and dies seconds later: every Start succeeds
// and only the next Observe shows it down. upAfter is the start count after
// which it stays up (0 = never).
type fakeHV struct {
	starts  int
	upAfter int
	// absent reports no domain until the name is defined again.
	absent    bool
	defines   int
	defined   map[string]bool
	defineErr error
	identity  vm.DomainIdentity

	// observed, when non-nil, replaces Observe's crash-loop reply.
	observed []vm.ObservedDomain
	// started records every name passed to Start.
	started []string
}

func (h *fakeHV) Observe() ([]vm.ObservedDomain, error) {
	if h.observed != nil {
		return h.observed, nil
	}
	if h.absent && !h.defined["crasher"] {
		return nil, nil
	}
	state := libvirt.DomainShutoff
	if h.upAfter > 0 && h.starts >= h.upAfter {
		state = libvirt.DomainRunning
	}
	return []vm.ObservedDomain{{Name: "crasher", State: state, Reason: int32(libvirt.DomainShutoffCrashed)}}, nil
}

func (h *fakeHV) Start(name string) error {
	h.starts++
	h.started = append(h.started, name)
	return nil
}

func (h *fakeHV) DefineFromSpec(e metadata.VMEntry, _, _ string) error {
	h.defines++
	if h.defineErr != nil {
		return h.defineErr
	}
	if h.defined == nil {
		h.defined = make(map[string]bool)
	}
	h.defined[e.ID] = true
	return nil
}

func (h *fakeHV) Identity(string) (vm.DomainIdentity, error) { return h.identity, nil }

// runTicks seeds a running VM whose boot disk exists and drives n ticks.
func runTicks(t *testing.T, hv *fakeHV, n int) *Loop {
	t.Helper()
	store := newTestStore(t)
	pool := seedPool(t, "crasher")
	if err := store.Upsert("crasher", func(e *metadata.VMEntry) {
		e.ID, e.PoolPath = "crasher", pool
		e.SetRunning(true)
	}); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	l := New(hv, store, events.New(0), nil)
	now := time.Now()
	for i := 0; i < n; i++ {
		l.reconcileOnce(now)
		now = now.Add(tickInterval)
	}
	return l
}

func writeEntry(t *testing.T, store *metadata.Store, e metadata.VMEntry) {
	t.Helper()
	if err := store.Upsert(e.ID, func(dst *metadata.VMEntry) { *dst = e }); err != nil {
		t.Fatalf("seed entry %q: %v", e.ID, err)
	}
}

func boolPtr(b bool) *bool { return &b }

func newTestLoopWithVLAN(t *testing.T, vlan VLANProvisioner) (*Loop, *fakeHV, *metadata.Store) {
	t.Helper()
	store := newTestStore(t)
	hv := &fakeHV{}
	return New(hv, store, events.New(0), vlan), hv, store
}

// TestLoop_CrashLoopGivesUp: every start here succeeds, so counting only failed
// starts would restart the VM forever. stableWindow is what makes it give up.
func TestLoop_CrashLoopGivesUp(t *testing.T) {
	hv := &fakeHV{}
	l := runTicks(t, hv, 200) // ~33 minutes of ticks

	st := l.getState("crasher")
	if !st.gaveUp {
		t.Errorf("gaveUp = false after 200 ticks and %d starts, want true", hv.starts)
	}
	if hv.starts != maxAttempts {
		t.Errorf("issued %d starts, want exactly %d — backoff and give-up should cap it", hv.starts, maxAttempts)
	}
}

func countKind(t *testing.T, ring *events.Ring, kind string) int {
	t.Helper()
	evs, _ := ring.Since(0, 0)
	n := 0
	for _, e := range evs {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// A VM that comes back and stays up must not accumulate toward give-up.
func TestLoop_StableVMClearsAttempts(t *testing.T) {
	hv := &fakeHV{upAfter: 1}
	l := runTicks(t, hv, 20) // well past stableWindow

	st := l.getState("crasher")
	if hv.starts != 1 {
		t.Errorf("issued %d starts for a VM that recovered, want 1", hv.starts)
	}
	if st.attempts != 0 || st.gaveUp {
		t.Errorf("attempts = %d, gaveUp = %v after stabilising, want 0, false", st.attempts, st.gaveUp)
	}
}

// TestLoop_RecoveryIsNarratedOnlyAfterTrouble: vm.recovered fires once after a
// restart, and never after an ordinary start, or the feed fills with noise.
func TestLoop_RecoveryIsNarratedOnlyAfterTrouble(t *testing.T) {
	tests := []struct {
		name string
		hv   *fakeHV
		want int
	}{
		// Shutoff until the loop starts it, then up for the rest of the run.
		{"restarted, then stable -> narrate once", &fakeHV{upAfter: 1}, 1},
		// Already running from the first tick; the loop never intervenes.
		{"never in trouble -> stay quiet", &fakeHV{starts: 1, upAfter: 1}, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := runTicks(t, tc.hv, 20) // well past stableWindow

			if got := countKind(t, l.ring, events.KindVMRecovered); got != tc.want {
				t.Errorf("got %d %s events, want %d", got, events.KindVMRecovered, tc.want)
			}
			if got := l.States()["crasher"]; got != (VMHealth{}) {
				t.Errorf("States()[crasher] = %+v after stabilising, want zero — a recovered VM carries no trouble", got)
			}
		})
	}
}

// TestLoop_GiveUpClearedByObservedStableRunning: give-up clears once the VM is
// observed running for stableWindow, or vmPhase keeps showing it as broken.
func TestLoop_GiveUpClearedByObservedStableRunning(t *testing.T) {
	hv := &fakeHV{}
	l := runTicks(t, hv, 200) // crash-loops all the way to give-up
	if !l.getState("crasher").gaveUp {
		t.Fatal("precondition: want gaveUp after the crash-loop run")
	}

	hv.upAfter = hv.starts // every subsequent Observe reports it running
	now := time.Now()
	for i := 0; i < 20; i++ {
		l.reconcileOnce(now)
		now = now.Add(tickInterval)
	}

	if got := l.States()["crasher"]; got != (VMHealth{}) {
		t.Errorf("States()[crasher] = %+v after running steadily, want zero", got)
	}
	if got := countKind(t, l.ring, events.KindVMRecovered); got != 1 {
		t.Errorf("got %d %s events, want 1", got, events.KindVMRecovered)
	}
}

// TestLoop_StoppedVMDropsItsHealth: prune only covers deleted VMs, so a VM
// stopped mid-crash-loop must drop its health or it reads as "gave up".
func TestLoop_StoppedVMDropsItsHealth(t *testing.T) {
	l := runTicks(t, &fakeHV{}, 200)
	if got := l.States()["crasher"]; !got.GaveUp {
		t.Fatalf("precondition: States()[crasher] = %+v, want GaveUp", got)
	}

	if err := l.meta.Update("crasher", func(e *metadata.VMEntry) { e.SetRunning(false) }); err != nil {
		t.Fatalf("stop the VM: %v", err)
	}
	l.reconcileOnce(time.Now())

	if got, ok := l.States()["crasher"]; ok {
		t.Errorf("States()[crasher] = %+v after the user stopped it, want no entry", got)
	}
}

// poweroffHV reports a clean in-guest shutdown, which fakeHV cannot produce.
type poweroffHV struct{}

func (poweroffHV) DefineFromSpec(metadata.VMEntry, string, string) error { return nil }
func (poweroffHV) Identity(string) (vm.DomainIdentity, error)            { return vm.DomainIdentity{}, nil }

func (poweroffHV) Observe() ([]vm.ObservedDomain, error) {
	return []vm.ObservedDomain{{
		Name: "crasher", State: libvirt.DomainShutoff, Reason: int32(libvirt.DomainShutoffShutdown),
	}}, nil
}
func (poweroffHV) Start(string) error { return nil }

// TestLoop_HonorPoweroffIsNarrated: honoring an in-guest poweroff changes the
// user's desired state, so it must emit an event.
func TestLoop_HonorPoweroffIsNarrated(t *testing.T) {
	store := newTestStore(t)
	if err := store.Upsert("crasher", func(e *metadata.VMEntry) { e.ID = "crasher"; e.SetRunning(true) }); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	ring := events.New(0)
	l := New(poweroffHV{}, store, ring, nil)

	l.reconcileOnce(time.Now())

	entry, err := store.Get("crasher")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if running, _ := entry.DesiredRunning(); running {
		t.Error("spec still says running after a clean in-guest poweroff, want stopped")
	}
	if got := countKind(t, ring, events.KindVMPoweredOff); got != 1 {
		t.Errorf("got %d %s events, want 1", got, events.KindVMPoweredOff)
	}
}

// TestLoop_StatesReflectsBookkeeping also checks that States returns a copy.
func TestLoop_StatesReflectsBookkeeping(t *testing.T) {
	l := runTicks(t, &fakeHV{}, 200) // same crash-loop-to-give-up run as above

	got := l.States()["crasher"]
	if want := (VMHealth{GaveUp: true, Attempts: maxAttempts}); got != want {
		t.Errorf("States()[crasher] = %+v, want %+v", got, want)
	}

	got.Attempts = 0 // mutate the caller's copy
	if l.States()["crasher"].Attempts != maxAttempts {
		t.Error("States() aliases guarded state")
	}
}

// TestLoop_DegradedIsStickyAndEventsOnce: vm.degraded is a degraded VM's only
// path to the UI and must fire once. "ghost" has no pool to rebuild onto.
func TestLoop_DegradedIsStickyAndEventsOnce(t *testing.T) {
	store := newTestStore(t)
	if err := store.Upsert("ghost", func(e *metadata.VMEntry) { e.ID = "ghost"; e.SetRunning(true) }); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	ring := events.New(0)
	l := New(&fakeHV{absent: true}, store, ring, nil)

	now := time.Now()
	for i := 0; i < 3; i++ {
		l.reconcileOnce(now)
		now = now.Add(tickInterval)
	}

	if got := l.States()["ghost"]; !got.Degraded {
		t.Errorf("States()[ghost].Degraded = false after 3 ticks absent from libvirt, want true")
	}
	if n := countKind(t, ring, events.KindVMDegraded); n != 1 {
		t.Errorf("got %d %s events over 3 ticks, want exactly 1", n, events.KindVMDegraded)
	}
}

// TestLoop_DegradedStaysOnceForAStartedVM: a started VM's spec carries
// StartedAtUnix, which the tick's state reset compares against.
func TestLoop_DegradedStaysOnceForAStartedVM(t *testing.T) {
	store := newTestStore(t)
	pool := seedPool(t, "ghost")
	started := time.Now().Add(-2 * time.Minute).Unix()
	if err := store.Upsert("ghost", func(e *metadata.VMEntry) {
		e.ID, e.PoolPath, e.StartedAtUnix = "ghost", pool, started
		e.SetRunning(true)
	}); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	disk, _, _ := storage.PathsFor(pool, "ghost")
	if err := os.Remove(disk); err != nil {
		t.Fatalf("remove boot disk: %v", err)
	}
	ring := events.New(0)
	l := New(&fakeHV{absent: true}, store, ring, nil)

	now := time.Now()
	for i := 0; i < 4; i++ {
		l.reconcileOnce(now)
		now = now.Add(tickInterval)
	}

	if n := countKind(t, ring, events.KindVMDegraded); n != 1 {
		t.Errorf("got %d %s events over 4 ticks, want exactly 1", n, events.KindVMDegraded)
	}
}

// TestLoop_IncompleteSpecIsDegradedNotGivenUp: no retry fixes an incomplete
// spec, and counting attempts would report both degraded and gave-up.
func TestLoop_IncompleteSpecIsDegradedNotGivenUp(t *testing.T) {
	hv := &fakeHV{absent: true, defineErr: vm.ErrIncompleteSpec}
	ring := events.New(0)
	store := newTestStore(t)
	pool := seedPool(t, "crasher")
	if err := store.Upsert("crasher", func(e *metadata.VMEntry) {
		e.ID, e.PoolPath = "crasher", pool
		e.SetRunning(true)
	}); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	l := New(hv, store, ring, nil)
	// Far enough apart that backoff never explains an idle tick.
	now := time.Now()
	for i := 0; i < 20; i++ {
		l.reconcileOnce(now)
		now = now.Add(2 * maxBackoff)
	}

	got := l.States()["crasher"]
	if !got.Degraded {
		t.Errorf("Degraded = false, want true: no retry can complete a spec")
	}
	if got.Attempts != 0 || got.GaveUp {
		t.Errorf("States() = %+v, want no attempts consumed and no give-up", got)
	}
	if n := countKind(t, ring, events.KindVMDegraded); n != 1 {
		t.Errorf("got %d %s events over 20 ticks, want exactly 1", n, events.KindVMDegraded)
	}
	if n := countKind(t, ring, events.KindVMGaveUp); n != 0 {
		t.Errorf("got %d %s events, want none", n, events.KindVMGaveUp)
	}
	// Still re-polled, so a hand-fixed metadata.json is noticed without a restart.
	if hv.defines < 2 {
		t.Errorf("define was attempted %d times over 20 ticks; a fixed spec would never be noticed", hv.defines)
	}
}

// TestLoop_RebuiltVMWithPolicyNeverStaysOff: restart_policy never means the loop
// does not boot the guest, not that an undefined VM stays gone.
func TestLoop_RebuiltVMWithPolicyNeverStaysOff(t *testing.T) {
	hv := &fakeHV{absent: true}
	ring := events.New(0)
	store := newTestStore(t)
	pool := seedPool(t, "crasher")
	if err := store.Upsert("crasher", func(e *metadata.VMEntry) {
		e.ID, e.PoolPath, e.RestartPolicy = "crasher", pool, metadata.RestartNever
		e.SetRunning(true)
	}); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	l := New(hv, store, ring, nil)
	l.reconcileOnce(time.Now())

	if hv.defines != 1 {
		t.Errorf("defines = %d, want 1: the domain comes back whatever the policy says", hv.defines)
	}
	if hv.starts != 0 {
		t.Errorf("starts = %d, want 0: restart_policy never means the loop does not boot it", hv.starts)
	}
	if n := countKind(t, ring, events.KindVMRedefined); n != 1 {
		t.Errorf("got %d %s events, want exactly 1", n, events.KindVMRedefined)
	}
}

// connectTestDriver connects to test:///default, skipping without libvirtd.
func connectTestDriver(t *testing.T) *vm.Manager {
	t.Helper()
	m, err := vm.New("test:///default")
	if err != nil {
		t.Skipf("libvirt not available (%v): skipping", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// TestAdopt_FillsRunningFromLiveState needs a real connection: adoption only
// happens while libvirt is reachable.
func TestAdopt_FillsRunningFromLiveState(t *testing.T) {
	m := connectTestDriver(t)
	store := newTestStore(t)
	ring := events.New(0)

	// test:///default pre-populates a running domain named "test".
	if err := store.Upsert("test", func(e *metadata.VMEntry) { e.ID = "test" }); err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	l := New(m, store, ring, nil)
	l.reconcileOnce(time.Now())

	entry, err := store.Get("test")
	if err != nil {
		t.Fatalf("Get after adoption: %v", err)
	}
	running, ok := entry.DesiredRunning()
	if !ok || !running {
		t.Errorf("Running after adoption = (%v, %v), want (true, true)", running, ok)
	}

	evs, _ := ring.Since(0, 0)
	if len(evs) == 0 || evs[0].Kind != events.KindSpecAdopted {
		t.Errorf("expected a %s event, got %+v", events.KindSpecAdopted, evs)
	}
}

// TestLoop_RecoversDestroyedDomain destroys a domain under the loop and expects
// a restart.
func TestLoop_RecoversDestroyedDomain(t *testing.T) {
	m := connectTestDriver(t)
	store := newTestStore(t)
	ring := events.New(0)
	const name = "reconcile-it-vm"

	err := m.Create(vm.VMCreateParams{
		Name: name, ImagePath: "/nonexistent/base.qcow2", DiskPath: "/nonexistent/disk.qcow2",
		CloudInitISO: "/nonexistent/seed.iso", VCPUs: 1, MemoryMB: 256, NetworkMode: "nat",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		_ = m.Destroy(name)
		_ = m.Delete(name)
	})

	err = store.Upsert(name, func(e *metadata.VMEntry) {
		e.ID = name
		e.SetRunning(true)
		e.StartedAtUnix = time.Now().Unix()
	})
	if err != nil {
		t.Fatalf("seed spec: %v", err)
	}

	l := New(m, store, ring, nil)
	l.reconcileOnce(time.Now()) // establishes runningSince; nothing to do yet

	if err = m.Destroy(name); err != nil {
		t.Fatalf("Destroy (simulated cord-yank): %v", err)
	}

	l.reconcileOnce(time.Now())

	got, err := m.Get(name)
	if err != nil || got.State != "running" {
		t.Errorf("state after recovery tick = (%v, %v), want running", got, err)
	}

	if n := countKind(t, ring, events.KindVMRestarted); n != 1 {
		t.Errorf("got %d %s events for %q, want 1", n, events.KindVMRestarted, name)
	}
}

// TestLoop_RedefinesUndefinedDomain: a domain undefined with its disk intact
// must come back as the same machine. A regenerated MAC still "comes back",
// with a new IP, so the identity assertion is the point.
func TestLoop_RedefinesUndefinedDomain(t *testing.T) {
	m := connectTestDriver(t)

	const name = "redefine-probe"
	pool := seedPool(t, name)
	disk, iso, _ := storage.PathsFor(pool, name)

	spec := metadata.VMEntry{ID: name, PoolPath: pool, NetworkMode: "nat", VCPUs: 1, MemoryMB: 256}
	spec.SetRunning(true)
	store := newTestStore(t)
	if err := store.Upsert(name, func(e *metadata.VMEntry) { *e = spec }); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	if err := m.DefineFromSpec(spec, disk, iso); err != nil {
		t.Fatalf("define: %v", err)
	}
	t.Cleanup(func() {
		_ = m.Destroy(name)
		_ = m.Delete(name)
	})
	if err := m.Start(name); err != nil {
		t.Fatalf("start: %v", err)
	}

	ring := events.New(0)
	l := New(m, store, ring, nil)
	now := time.Now()
	l.reconcileOnce(now)

	before, err := m.Identity(name)
	if err != nil {
		t.Fatalf("Identity: %v", err)
	}
	if before.UUID == "" || before.MAC == "" {
		t.Fatalf("libvirt assigned no identity to work with: %+v", before)
	}
	recorded, err := store.Get(name)
	if err != nil {
		t.Fatalf("read back spec: %v", err)
	}
	if recorded.UUID != before.UUID || recorded.MAC != before.MAC {
		t.Fatalf("spec records uuid=%q mac=%q, want the live %+v — without the backfill a rebuild has nothing to restore",
			recorded.UUID, recorded.MAC, before)
	}

	// virsh destroy && virsh undefine. The disk stays, unlike the degraded case.
	if err = m.Destroy(name); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if err = m.Delete(name); err != nil {
		t.Fatalf("undefine: %v", err)
	}

	// Far enough on that backoff never explains an idle tick.
	now = now.Add(2 * maxBackoff)
	l.reconcileOnce(now)

	after, err := m.Identity(name)
	if err != nil {
		t.Fatalf("the domain was not rebuilt: %v", err)
	}
	if after != before {
		t.Errorf("came back as a different machine: %+v -> %+v", before, after)
	}
	v, err := m.Get(name)
	if err != nil || v.State != "running" {
		t.Errorf("rebuilt domain state = %v (%v), want running", v, err)
	}
	if n := countKind(t, ring, events.KindVMRedefined); n != 1 {
		t.Errorf("got %d %s events, want exactly 1", n, events.KindVMRedefined)
	}
}

type fakeVLAN struct {
	ensured []string
	err     error
}

func (f *fakeVLAN) Ensure(uplink string, vid int) (string, error) {
	f.ensured = append(f.ensured, fmt.Sprintf("%s/%d", uplink, vid))
	// The loop discards this value, so a duplicated literal would drift unnoticed.
	return hostnet.BridgeName(vid), f.err
}

func TestReconcile_EnsuresTheVLANBeforeStarting(t *testing.T) {
	// After a reboot the VLAN link must exist before libvirt starts the domain.
	vlan := &fakeVLAN{}
	l, hv, store := newTestLoopWithVLAN(t, vlan)

	writeEntry(t, store, metadata.VMEntry{
		ID: "web", Running: boolPtr(true),
		NetworkMode: metadata.NetworkBridge, Bridge: "nc-vlan20",
		VLANID: 20, VLANUplink: "eth0",
	})
	hv.observed = []vm.ObservedDomain{{Name: "web", State: libvirt.DomainShutoff}}

	l.reconcileOnce(time.Now())

	if len(vlan.ensured) != 1 || vlan.ensured[0] != "eth0/20" {
		t.Fatalf("Ensure calls = %v, want [eth0/20]", vlan.ensured)
	}
	if len(hv.started) != 1 || hv.started[0] != "web" {
		t.Fatalf("started = %v, want [web]", hv.started)
	}
}

func TestReconcile_AFailedEnsureDoesNotStartTheVM(t *testing.T) {
	// Starting onto a missing bridge fails in libvirt with an error naming neither
	// the VLAN nor the cause.
	vlan := &fakeVLAN{err: errors.New("bridge \"nc-vlan20\" has the unexpected port \"eth1\"")}
	l, hv, store := newTestLoopWithVLAN(t, vlan)

	writeEntry(t, store, metadata.VMEntry{
		ID: "web", Running: boolPtr(true),
		NetworkMode: metadata.NetworkBridge, Bridge: "nc-vlan20",
		VLANID: 20, VLANUplink: "eth0",
	})
	hv.observed = []vm.ObservedDomain{{Name: "web", State: libvirt.DomainShutoff}}

	l.reconcileOnce(time.Now())

	if len(hv.started) != 0 {
		t.Fatalf("started %v despite a failed provision", hv.started)
	}
	if got := l.States()["web"]; !got.Degraded {
		t.Error("a VM whose VLAN could not be provisioned is not reported degraded")
	}
}

// TestReconcile_ARebuildEnsuresTheVLANBeforeDefining: DomainDefineXML does not
// check the bridge exists, so a rebuild must ensure the VLAN first.
func TestReconcile_ARebuildEnsuresTheVLANBeforeDefining(t *testing.T) {
	vlanEntry := func(pool string) metadata.VMEntry {
		return metadata.VMEntry{
			ID: "web", PoolPath: pool, Running: boolPtr(true),
			NetworkMode: metadata.NetworkBridge, Bridge: "nc-vlan20",
			VLANID: 20, VLANUplink: "eth0",
		}
	}

	vlan := &fakeVLAN{}
	l, hv, store := newTestLoopWithVLAN(t, vlan)
	writeEntry(t, store, vlanEntry(seedPool(t, "web")))
	hv.observed = nil // libvirt has no record of it

	l.reconcileOnce(time.Now())

	if len(vlan.ensured) != 1 || vlan.ensured[0] != "eth0/20" {
		t.Fatalf("Ensure calls = %v, want [eth0/20] before the rebuild", vlan.ensured)
	}
	if hv.defines != 1 {
		t.Fatalf("defines = %d, want 1", hv.defines)
	}

	// A bridge that could not be built must stop the rebuild.
	failing := &fakeVLAN{err: errors.New(`bridge "nc-vlan20" has the unexpected port "eth1"`)}
	l2, hv2, store2 := newTestLoopWithVLAN(t, failing)
	writeEntry(t, store2, vlanEntry(seedPool(t, "web")))
	hv2.observed = nil

	l2.reconcileOnce(time.Now())

	if hv2.defines != 0 {
		t.Fatalf("defined %d domains despite a failed provision", hv2.defines)
	}
	if got := l2.States()["web"]; !got.Degraded {
		t.Error("a VM whose VLAN could not be provisioned before a rebuild is not reported degraded")
	}
}

// TestReconcile_AFailedLegacyConversionDoesNotWedgeTheVM: a failed conversion
// write (a full disk, say) must still report the VM degraded. An entry the store
// does not hold is the portable way to make Update fail.
func TestReconcile_AFailedLegacyConversionDoesNotWedgeTheVM(t *testing.T) {
	l, _, _ := newTestLoopWithVLAN(t, &fakeVLAN{})

	legacy := metadata.VMEntry{
		ID: "web", Running: boolPtr(true),
		NetworkMode: metadata.NetworkBridge, Bridge: "br0", VLANID: 20,
	}
	l.reconcileEntry("web", legacy, vm.ObservedDomain{}, false, time.Now())

	if got := l.States()["web"]; !got.Degraded {
		t.Fatal("a VM whose VLAN conversion keeps failing is reported nowhere the operator can see it")
	}
}

func TestReconcile_ConvertsALegacyTaggedEntryOnce(t *testing.T) {
	vlan := &fakeVLAN{}
	l, hv, store := newTestLoopWithVLAN(t, vlan)

	// The legacy shape: a tag, an operator-picked bridge, no uplink.
	writeEntry(t, store, metadata.VMEntry{
		ID: "web", Running: boolPtr(true),
		NetworkMode: metadata.NetworkBridge, Bridge: "br0", VLANID: 20,
	})
	hv.observed = []vm.ObservedDomain{{Name: "web", State: libvirt.DomainRunning}}

	l.reconcileOnce(time.Now())

	e, _ := store.Get("web")
	if e.VLANUplink != "br0" {
		t.Errorf("VLANUplink = %q, want br0 — the bridge it used to tag on becomes the parent", e.VLANUplink)
	}
	if e.Bridge != "nc-vlan20" {
		t.Errorf("Bridge = %q, want nc-vlan20", e.Bridge)
	}
}

func TestReconcile_LegacyConversionDoesNotProvisionOnTheSameTick(t *testing.T) {
	// Ensure waits for the uplink, so a just-converted entry provisions on the next
	// tick, after the write has committed.
	vlan := &fakeVLAN{}
	l, hv, store := newTestLoopWithVLAN(t, vlan)

	writeEntry(t, store, metadata.VMEntry{
		ID: "web", Running: boolPtr(true),
		NetworkMode: metadata.NetworkBridge, Bridge: "br0", VLANID: 20,
	})
	hv.observed = []vm.ObservedDomain{{Name: "web", State: libvirt.DomainShutoff}}

	l.reconcileOnce(time.Now())
	if len(vlan.ensured) != 0 {
		t.Fatalf("provisioned on the conversion tick: %v", vlan.ensured)
	}

	l.reconcileOnce(time.Now())
	if len(vlan.ensured) != 1 || vlan.ensured[0] != "br0/20" {
		t.Fatalf("Ensure calls = %v, want [br0/20] on the second tick", vlan.ensured)
	}
}

// TestConvertLegacyVLAN_LeavesAnAlreadyConvertedEntryUntouched exercises the
// closure's blanks-only guard with a stale snapshot.
func TestConvertLegacyVLAN_LeavesAnAlreadyConvertedEntryUntouched(t *testing.T) {
	l, _, store := newTestLoopWithVLAN(t, &fakeVLAN{})

	writeEntry(t, store, metadata.VMEntry{
		ID: "web", Running: boolPtr(true),
		NetworkMode: metadata.NetworkBridge,
		Bridge:      "nc-vlan20", VLANID: 20, VLANUplink: "eth0",
	})

	stale := metadata.VMEntry{
		ID: "web", Running: boolPtr(true),
		NetworkMode: metadata.NetworkBridge, Bridge: "br0", VLANID: 20,
	}
	if err := l.convertLegacyVLAN("web", stale); err != nil {
		t.Fatalf("convertLegacyVLAN: %v", err)
	}

	e, err := store.Get("web")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if e.VLANUplink != "eth0" || e.Bridge != "nc-vlan20" {
		t.Errorf("convertLegacyVLAN overwrote an already-converted entry: uplink=%q bridge=%q, want eth0/nc-vlan20",
			e.VLANUplink, e.Bridge)
	}
}

// TestConvertLegacyVLAN_DoesNotProvisionOntoAConcurrentlyClearedVLAN: a PATCH
// clearing the VLAN between the batch read and the write must not yield
// "nc-vlan0". The blanks-only guard misses it, as VLANUplink is "" either way.
func TestConvertLegacyVLAN_DoesNotProvisionOntoAConcurrentlyClearedVLAN(t *testing.T) {
	l, _, store := newTestLoopWithVLAN(t, &fakeVLAN{})

	// On disk, as left by the concurrent PATCH that cleared the VLAN.
	writeEntry(t, store, metadata.VMEntry{
		ID: "web", Running: boolPtr(true),
		NetworkMode: metadata.NetworkBridge, Bridge: "br0",
	})

	// The stale batch-read view reconcileEntry acted on, from before the clear.
	stale := metadata.VMEntry{
		ID: "web", Running: boolPtr(true),
		NetworkMode: metadata.NetworkBridge, Bridge: "br0", VLANID: 20,
	}
	if err := l.convertLegacyVLAN("web", stale); err != nil {
		t.Fatalf("convertLegacyVLAN: %v", err)
	}

	e, err := store.Get("web")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if e.VLANUplink != "" || e.Bridge != "br0" {
		t.Errorf("convertLegacyVLAN wrote onto a concurrently cleared VLAN: uplink=%q bridge=%q, want untouched (\"\", \"br0\")",
			e.VLANUplink, e.Bridge)
	}
}

// A paused loop must neither start a VM nor report one as crash-looping, and
// must act again once resumed.
func TestLoop_PauseStopsActingAndReporting(t *testing.T) {
	hv := &fakeHV{}
	l := runTicks(t, hv, 3)
	if hv.starts == 0 || len(l.States()) == 0 {
		t.Fatalf("setup: %d starts, states %v — want the loop acting before the pause", hv.starts, l.States())
	}

	l.SetEnabled(false)
	before := hv.starts
	now := time.Now()
	for i := 0; i < 20; i++ {
		l.reconcileOnce(now)
		now = now.Add(tickInterval)
	}
	if hv.starts != before {
		t.Errorf("paused loop issued %d starts", hv.starts-before)
	}
	if st := l.States(); len(st) != 0 {
		t.Errorf("paused loop reports %v, want nothing", st)
	}

	l.SetEnabled(true)
	l.reconcileOnce(now)
	if hv.starts == before {
		t.Error("resumed loop did not act")
	}
}
