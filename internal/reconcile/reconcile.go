// Package reconcile brings each VM's live state back in line with its spec
// (metadata.json) after a crash, power loss or reboot.
//
// It only starts or rebuilds. Specs that say stopped are filtered out before
// decide, so a stale read can never stop a VM.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"sync"
	"time"

	libvirt "github.com/digitalocean/go-libvirt"

	"github.com/nanocloud-io/nanocloud/internal/events"
	"github.com/nanocloud-io/nanocloud/internal/hostnet"
	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/storage"
	"github.com/nanocloud-io/nanocloud/internal/vm"
)

const (
	tickInterval = 10 * time.Second
	maxAttempts  = 6
	// stableWindow is how long a VM must stay up before its attempts reset.
	// Without it a VM that dies seconds after each boot never gives up.
	stableWindow = 60 * time.Second
	baseBackoff  = 10 * time.Second
	maxBackoff   = 5 * time.Minute
)

// Hypervisor is the subset of *vm.Manager the loop calls, so tests can fake it.
type Hypervisor interface {
	Observe() ([]vm.ObservedDomain, error)
	Start(name string) error
	DefineFromSpec(e metadata.VMEntry, diskPath, isoPath string) error
	// Identity reads what libvirt assigned a live domain, so the spec can record
	// it before the domain is lost.
	Identity(name string) (vm.DomainIdentity, error)
}

// VLANProvisioner is the subset of *hostnet.Manager the loop calls. The loop
// never picks an uplink: the spec records the one the VM was created against.
type VLANProvisioner interface {
	Ensure(uplink string, vid int) (string, error)
}

// vmState is per-VM backoff bookkeeping. It is in memory only, so a daemon
// restart clears a give-up on purpose.
type vmState struct {
	attempts      int
	nextAttempt   time.Time
	gaveUp        bool
	degraded      bool      // libvirt has no record of the domain; logged once
	runningSince  time.Time // zero when not currently observed running
	lastStartedAt int64     // last metadata.StartedAtUnix this loop has seen
}

// Loop owns the reconcile tick. Construct with New, run with Run.
type Loop struct {
	hv   Hypervisor
	meta *metadata.Store
	ring *events.Ring
	vlan VLANProvisioner

	mu        sync.Mutex
	states    map[string]vmState
	connected *bool // nil until the first Observe; then last known state
	paused    bool
}

// New returns a Loop.
func New(hv Hypervisor, meta *metadata.Store, ring *events.Ring, vlan VLANProvisioner) *Loop {
	return &Loop{hv: hv, meta: meta, ring: ring, vlan: vlan, states: make(map[string]vmState)}
}

// SetEnabled pauses or resumes self-healing. Pausing drops the backoff state,
// so a VM is not reported as crash-looping by a loop that is not acting on it.
func (l *Loop) SetEnabled(on bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.paused == !on {
		return
	}
	l.paused = !on
	if l.paused {
		l.states = make(map[string]vmState)
		log.Printf("reconcile: paused — VMs will not be restarted automatically")
	} else {
		log.Printf("reconcile: resumed")
	}
}

// Run ticks until ctx is canceled, starting with an immediate sweep.
func (l *Loop) Run(ctx context.Context) {
	l.reconcileOnce(time.Now())
	t := time.NewTicker(tickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.reconcileOnce(time.Now())
		}
	}
}

// Enabled reports whether the loop is acting on VMs.
func (l *Loop) Enabled() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.paused
}

// reconcileOnce is one tick, with now injected so tests can drive it directly.
func (l *Loop) reconcileOnce(now time.Time) {
	l.mu.Lock()
	paused := l.paused
	l.mu.Unlock()
	if paused {
		return
	}
	entries, err := l.meta.Read()
	if err != nil {
		log.Printf("reconcile: read metadata: %v", err)
		return
	}
	l.prune(entries)

	observed, err := l.hv.Observe()
	l.noteConnection(err == nil)
	if err != nil {
		return // nothing safe to do without live state; retried next tick
	}
	byName := make(map[string]vm.ObservedDomain, len(observed))
	for _, o := range observed {
		byName[o.Name] = o
	}
	for id, entry := range entries {
		obs, present := byName[id]
		l.reconcileEntry(id, entry, obs, present, now)
	}
}

// reconcileEntry adopts an undetermined spec or drives one that wants running.
func (l *Loop) reconcileEntry(id string, entry metadata.VMEntry, obs vm.ObservedDomain, present bool, now time.Time) {
	// Identity can only be read while the domain exists and is only needed once
	// it is gone, so record it on sight, stopped VMs included.
	if present && (entry.UUID == "" || entry.MAC == "") {
		l.backfillIdentity(id)
	}

	// A tag with no uplink predates VLAN provisioning. Convert it here, like the
	// identity backfill, because a migration may run before libvirtd is up.
	var convertErr error
	if entry.VLANID > 0 && entry.VLANUplink == "" && entry.NetworkMode == metadata.NetworkBridge {
		convertErr = l.convertLegacyVLAN(id, entry)
		if convertErr == nil {
			return // provisioned on the next tick, once the entry has a parent
		}
		// A conversion that keeps failing (a full disk) must not take the VM out of
		// the loop. Falling through is safe: the legacy shape still renders its tag.
	}

	running, ok := entry.DesiredRunning()
	if !ok {
		l.adopt(id, present, obs)
		return
	}
	if !running {
		// prune only covers deleted VMs. Without this, a VM stopped mid-crash-loop
		// reports gave-up forever.
		l.clearState(id)
		return
	}

	st := l.getState(id)
	// StartedAtUnix changing without this loop writing it means a user start, the
	// only thing that clears a give-up.
	if entry.StartedAtUnix != st.lastStartedAt {
		st = vmState{lastStartedAt: entry.StartedAtUnix}
	}

	// Marked after the reset above, which would otherwise discard it.
	if convertErr != nil {
		l.markDegraded(id, &st, "its VLAN could not be converted to the provisioned shape", convertErr.Error())
	}

	if present && isRunningState(obs.State) {
		if st.runningSince.IsZero() {
			st.runningSince = now
		}
	} else {
		st.runningSince = time.Time{}
	}

	in := tickInput{
		present: present, state: obs.State, reason: obs.Reason, policy: entry.Policy(),
		gaveUp: st.gaveUp, nextAttempt: st.nextAttempt, runningSince: st.runningSince, now: now,
	}
	if !present {
		in.diskExists = bootDiskExists(entry)
	}

	switch decide(in) {
	case actionConverge:
		// Only after a recovery, or every ordinary start would log this a minute later.
		if st.attempts > 0 || st.gaveUp || st.degraded {
			l.ring.Append(events.Event{Level: events.LevelInfo, Kind: events.KindVMRecovered, VM: id,
				Message: fmt.Sprintf("recovered — running steadily for %s", stableWindow)})
		}
		// Observed uptime is evidence of recovery, so it clears give-up too.
		st = vmState{lastStartedAt: st.lastStartedAt, runningSince: st.runningSince}
	case actionHonorPoweroff:
		if err := l.meta.Update(id, func(e *metadata.VMEntry) { e.SetRunning(false) }); err != nil {
			log.Printf("reconcile: honor poweroff of %q: %v", id, err)
		} else {
			l.ring.Append(events.Event{Level: events.LevelInfo, Kind: events.KindVMPoweredOff, VM: id,
				Message: "powered itself off; honoring it as intent"})
		}
	case actionStart:
		l.attemptStart(id, &st, now)
	case actionDefine:
		l.attemptDefine(id, &st, now)
	case actionDegraded:
		l.markDegraded(id, &st,
			"should be running, but libvirt has no record of it and its disk is gone too", "")
	}
	l.setState(id, st)
}

// markDegraded records a state the loop cannot act on, once. A degraded VM has
// no row in the VM list, so this event is the only report of it.
func (l *Loop) markDegraded(id string, st *vmState, message, detail string) {
	if st.degraded {
		return
	}
	st.degraded = true
	log.Printf("reconcile: %q %s", id, message)
	l.ring.Append(events.Event{Level: events.LevelError, Kind: events.KindVMDegraded, VM: id,
		Message: message, Detail: detail})
}

// attemptStart starts a VM and arms the next backoff.
func (l *Loop) attemptStart(id string, st *vmState, now time.Time) {
	fresh, ok := l.freshIntent(id)
	if !ok {
		return
	}

	if !l.ensureVLAN(id, st, fresh, now) {
		return
	}

	err := l.hv.Start(id)

	// Every start counts, successful or not. A VM that dies seconds after booting
	// gets a successful DomainCreate every time, so counting only failures would
	// restart it forever. Only stableWindow clears the counter.
	st.attempts++
	st.nextAttempt = now.Add(backoffFor(st.attempts))

	if err != nil {
		l.ring.Append(events.Event{Level: events.LevelWarn, Kind: events.KindVMRestarted, VM: id,
			Message: fmt.Sprintf("restart attempt %d/%d failed, retrying at %s", st.attempts, maxAttempts, st.nextAttempt.Format(time.RFC3339)),
			Detail:  err.Error()})
	} else {
		// Advance lastStartedAt only if the write landed, or the next tick reads the
		// mismatch as a user start and wipes the attempt count.
		if uerr := l.meta.Update(id, func(e *metadata.VMEntry) { e.StartedAtUnix = now.Unix() }); uerr != nil {
			log.Printf("reconcile: record start of %q: %v", id, uerr)
		} else {
			st.lastStartedAt = now.Unix()
		}
		l.ring.Append(events.Event{Level: events.LevelInfo, Kind: events.KindVMRestarted, VM: id,
			Message: fmt.Sprintf("restarted (attempt %d/%d)", st.attempts, maxAttempts)})
	}

	l.giveUpIfExhausted(id, st)
}

// ensureVLAN recreates the VLAN link a VM rides, which is how a VLAN survives
// a host reboot, and reports whether the caller may proceed. It guards defines
// too: DomainDefineXML accepts a missing bridge and the VM then fails at boot.
// A failure arms the backoff so `ip link add` does not run on every tick.
func (l *Loop) ensureVLAN(id string, st *vmState, e metadata.VMEntry, now time.Time) bool {
	if l.vlan == nil || e.VLANUplink == "" || e.VLANID <= 0 {
		return true
	}
	if _, err := l.vlan.Ensure(e.VLANUplink, e.VLANID); err != nil {
		st.attempts++
		st.nextAttempt = now.Add(backoffFor(st.attempts))
		l.markDegraded(id, st, "VLAN could not be provisioned", err.Error())
		l.giveUpIfExhausted(id, st)
		return false
	}
	return true
}

// attemptDefine rebuilds a domain libvirt has lost and, unless the restart
// policy forbids it, starts it in the same attempt. Leaving the start to the
// next tick would depend on the shutoff reason libvirt reports for a domain
// that never ran, and a driver reporting SHUTDOWN would turn on-failure into
// honor-poweroff.
func (l *Loop) attemptDefine(id string, st *vmState, now time.Time) {
	fresh, ok := l.freshIntent(id)
	if !ok {
		return
	}
	disk, iso, ok := storage.PathsFor(fresh.PoolPath, fresh.ID)
	if !ok {
		l.markDegraded(id, st, "should be running, but libvirt has no record of it and it records no storage pool to rebuild from", "")
		return
	}
	if !l.ensureVLAN(id, st, fresh, now) {
		return
	}

	if err := l.hv.DefineFromSpec(fresh, disk, iso); err != nil {
		if errors.Is(err, vm.ErrIncompleteSpec) {
			// A retry cannot complete a spec, so this spends no attempt. It is re-polled
			// at the slowest rate so a hand edit of metadata.json is noticed.
			st.nextAttempt = now.Add(maxBackoff)
			l.markDegraded(id, st, "cannot be rebuilt: its spec does not describe a complete machine", err.Error())
			return
		}
		st.attempts++
		st.nextAttempt = now.Add(backoffFor(st.attempts))
		l.ring.Append(events.Event{Level: events.LevelWarn, Kind: events.KindVMRedefined, VM: id,
			Message: fmt.Sprintf("rebuild attempt %d/%d failed, retrying at %s", st.attempts, maxAttempts, st.nextAttempt.Format(time.RFC3339)),
			Detail:  err.Error()})
		l.giveUpIfExhausted(id, st)
		return
	}

	// Define and start together count as one attempt.
	st.attempts++
	st.nextAttempt = now.Add(backoffFor(st.attempts))
	st.degraded = false // the domain exists again

	// restart_policy never rebuilds the domain but leaves it off.
	restart, _ := restartDecision(fresh.Policy(), libvirt.DomainShutoff, int32(libvirt.DomainShutoffUnknown))
	if !restart {
		l.ring.Append(events.Event{Level: events.LevelInfo, Kind: events.KindVMRedefined, VM: id,
			Message: fmt.Sprintf("rebuilt from its spec (attempt %d/%d); left stopped, because its restart policy is %s", st.attempts, maxAttempts, fresh.Policy())})
		l.giveUpIfExhausted(id, st)
		return
	}

	if err := l.hv.Start(id); err != nil {
		l.ring.Append(events.Event{Level: events.LevelWarn, Kind: events.KindVMRedefined, VM: id,
			Message: fmt.Sprintf("rebuilt from its spec (attempt %d/%d), but would not start", st.attempts, maxAttempts),
			Detail:  startFailureDetail(err, iso)})
		l.giveUpIfExhausted(id, st)
		return
	}

	// As in attemptStart, an unrecorded StartedAtUnix would read as a user start.
	if uerr := l.meta.Update(id, func(e *metadata.VMEntry) { e.StartedAtUnix = now.Unix() }); uerr != nil {
		log.Printf("reconcile: record rebuild of %q: %v", id, uerr)
	} else {
		st.lastStartedAt = now.Unix()
	}
	l.ring.Append(events.Event{Level: events.LevelInfo, Kind: events.KindVMRedefined, VM: id,
		Message: fmt.Sprintf("rebuilt from its spec and started (attempt %d/%d)", st.attempts, maxAttempts)})
	l.giveUpIfExhausted(id, st)
}

// startFailureDetail names the seed ISO when it is missing. libvirt does not
// check disk sources at define time, so a missing ISO only fails the start.
func startFailureDetail(err error, isoPath string) string {
	if _, statErr := os.Stat(isoPath); statErr != nil {
		return fmt.Sprintf("%v (its cloud-init seed %s is missing, which is likely the cause)", err, isoPath)
	}
	return err.Error()
}

// backfillIdentity records the UUID and MAC libvirt assigned, so a rebuilt
// domain keeps its MAC and with it its DHCP lease. No event: after an upgrade
// it would fire for the whole fleet.
func (l *Loop) backfillIdentity(id string) {
	live, err := l.hv.Identity(id)
	if err != nil {
		log.Printf("reconcile: read identity of %q: %v", id, err)
		return
	}
	if live.UUID == "" {
		return // every domain has one, so an empty read is a failed read in disguise
	}
	// Fill blanks only; a recorded identity is never overwritten.
	err = l.meta.Update(id, func(e *metadata.VMEntry) {
		if e.UUID == "" {
			e.UUID = live.UUID
		}
		if e.MAC == "" {
			e.MAC = live.MAC
		}
	})
	if err != nil {
		log.Printf("reconcile: record identity of %q: %v", id, err)
	}
}

// convertLegacyVLAN moves a pre-provisioning entry onto the provisioned shape:
// its old bridge becomes the parent of a subinterface and the guest attaches
// to the bridge Ensure creates. The VM then reports restart_required.
func (l *Loop) convertLegacyVLAN(id string, entry metadata.VMEntry) error {
	err := l.meta.Update(id, func(e *metadata.VMEntry) {
		if e.VLANUplink != "" {
			return // blanks only, as backfillIdentity is
		}
		// The closure sees the file as it is now. A PATCH that cleared the VLAN since
		// the batch read must not get a bridge Ensure never creates.
		if e.VLANID <= 0 {
			return
		}
		e.VLANUplink = e.BridgeOf()
		e.Bridge = hostnet.BridgeName(e.VLANID)
	})
	if err != nil {
		log.Printf("reconcile: convert VLAN of %q: %v", id, err)
	}
	return err
}

// bootDiskExists reports whether a vanished domain has a disk to rebuild onto.
func bootDiskExists(e metadata.VMEntry) bool {
	disk, _, ok := storage.PathsFor(e.PoolPath, e.ID)
	if !ok {
		return false
	}
	_, err := os.Stat(disk)
	return err == nil
}

// freshIntent re-reads the spec entry right before acting, so a stop or delete
// that landed after the batch read wins. For a rebuild, a stale read would
// resurrect a domain the delete handler just undefined.
func (l *Loop) freshIntent(id string) (metadata.VMEntry, bool) {
	fresh, err := l.meta.Get(id)
	if err != nil {
		return metadata.VMEntry{}, false // deleted since the batch read
	}
	if running, ok := fresh.DesiredRunning(); !ok || !running {
		return metadata.VMEntry{}, false // stopped since the batch read
	}
	return fresh, true
}

// giveUpIfExhausted retires a VM once the attempt budget is spent. Rebuilds
// and starts share one budget.
func (l *Loop) giveUpIfExhausted(id string, st *vmState) {
	if st.attempts < maxAttempts || st.gaveUp {
		return
	}
	st.gaveUp = true
	l.ring.Append(events.Event{Level: events.LevelError, Kind: events.KindVMGaveUp, VM: id,
		Message: fmt.Sprintf("gave up restarting after %d attempts", maxAttempts)})
}

// adopt fills Running for an entry that predates the field, from observed
// libvirt state. It is only called after a successful Observe.
func (l *Loop) adopt(id string, present bool, obs vm.ObservedDomain) {
	running := present && isRunningState(obs.State)
	if err := l.meta.Update(id, func(e *metadata.VMEntry) { e.SetRunning(running) }); err != nil {
		log.Printf("reconcile: adopt %q: %v", id, err)
		return
	}
	state := "stopped"
	if running {
		state = "running"
	}
	l.ring.Append(events.Event{Level: events.LevelInfo, Kind: events.KindSpecAdopted, VM: id,
		Message: fmt.Sprintf("adopted as %s from live libvirt state", state)})
}

// noteConnection emits libvirt.lost/connected on transitions only.
func (l *Loop) noteConnection(connected bool) {
	l.mu.Lock()
	prev := l.connected
	l.connected = &connected
	l.mu.Unlock()

	if prev == nil || *prev == connected {
		return
	}
	kind, msg, lvl := events.KindLibvirtConnected, "libvirt connection restored", events.LevelInfo
	if !connected {
		kind, msg, lvl = events.KindLibvirtLost, "libvirt connection lost", events.LevelError
	}
	l.ring.Append(events.Event{Level: lvl, Kind: kind, Message: msg})
}

// prune drops bookkeeping for VMs no longer in the spec.
func (l *Loop) prune(entries map[string]metadata.VMEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for id := range l.states {
		if _, ok := entries[id]; !ok {
			delete(l.states, id)
		}
	}
}

// VMHealth is the per-VM health the API reports.
type VMHealth struct {
	GaveUp   bool `json:"gave_up"`
	Attempts int  `json:"attempts"`
	Degraded bool `json:"degraded"`
}

// States returns a copy of each VM's health.
func (l *Loop) States() map[string]VMHealth {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]VMHealth, len(l.states))
	if l.paused { // a tick already under way when paused may have added some
		return out
	}
	for id, st := range l.states {
		out[id] = VMHealth{GaveUp: st.gaveUp, Attempts: st.attempts, Degraded: st.degraded}
	}
	return out
}

func (l *Loop) getState(id string) vmState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.states[id]
}

func (l *Loop) setState(id string, st vmState) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.states[id] = st
}

func (l *Loop) clearState(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.states, id)
}

func isRunningState(s libvirt.DomainState) bool {
	return s == libvirt.DomainRunning || s == libvirt.DomainBlocked
}

// backoffFor returns the delay after n attempts: 10s doubling up to 5m, with
// up to 10% jitter so VMs that failed together don't retry in lockstep.
func backoffFor(n int) time.Duration {
	d := baseBackoff * time.Duration(1<<uint(n-1))
	if d <= 0 || d > maxBackoff {
		d = maxBackoff
	}
	return time.Duration(float64(d) * (0.9 + rand.Float64()*0.2))
}
