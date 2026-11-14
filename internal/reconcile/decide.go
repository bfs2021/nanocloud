package reconcile

import (
	"time"

	libvirt "github.com/digitalocean/go-libvirt"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

// tickAction is what one VM's tick resolves to, so decide's table test can
// assert on it directly.
type tickAction int

const (
	actionNone tickAction = iota
	actionConverge
	actionPaused
	actionBackoff
	actionGaveUp
	actionHonorPoweroff
	actionStart
	// actionDefine: libvirt has no record of the domain but its boot disk is
	// still there, so it was undefined outside nano-cloud. A reboot or crash
	// never causes this, since libvirt persists definitions.
	actionDefine
	// actionDegraded is the same observation with no disk behind it. It is not
	// sticky: a pool that mounts late turns it into actionDefine.
	actionDegraded
)

// tickInput is everything decide needs, resolved by the caller so that decide
// does no I/O.
type tickInput struct {
	present bool
	// diskExists is only resolved when present is false.
	diskExists   bool
	state        libvirt.DomainState
	reason       int32
	policy       string
	gaveUp       bool
	nextAttempt  time.Time
	runningSince time.Time
	now          time.Time
}

// decide maps one VM's observed state and backoff to this tick's action.
// Callers pass only entries whose spec says running:true, so a stale read can
// never lead to a stop.
func decide(in tickInput) tickAction {
	if !in.present {
		if !in.diskExists {
			return actionDegraded
		}
		// Bounded like a start, or DomainDefineXML would be retried every tick.
		if in.gaveUp {
			return actionGaveUp
		}
		if in.now.Before(in.nextAttempt) {
			return actionBackoff
		}
		// restart_policy is not consulted here: existence is not power state.
		// attemptDefine decides whether the rebuilt domain boots.
		return actionDefine
	}

	switch in.state {
	case libvirt.DomainRunning, libvirt.DomainBlocked:
		if !in.runningSince.IsZero() && in.now.Sub(in.runningSince) >= stableWindow {
			return actionConverge
		}
		return actionNone
	case libvirt.DomainPaused, libvirt.DomainPmsuspended:
		return actionPaused
	case libvirt.DomainShutoff, libvirt.DomainCrashed:
		if in.gaveUp {
			return actionGaveUp
		}
		restart, adopt := restartDecision(in.policy, in.state, in.reason)
		if adopt {
			return actionHonorPoweroff
		}
		if !restart {
			return actionNone
		}
		if in.now.Before(in.nextAttempt) {
			return actionBackoff
		}
		return actionStart
	default:
		// DomainNostate or a transient DomainShutdown: wait for it to settle.
		return actionNone
	}
}

// restartDecision decides whether a stopped VM whose spec says running:true
// should restart. adopt is true for a clean in-guest poweroff under the
// default policy, which is honored by setting the spec to running:false
// rather than fighting the guest.
func restartDecision(policy string, state libvirt.DomainState, reason int32) (restart, adopt bool) {
	switch policy {
	case metadata.RestartNever:
		return false, false
	case metadata.RestartAlways:
		return true, false
	default: // on-failure
		if state == libvirt.DomainShutoff && libvirt.DomainShutoffReason(reason) == libvirt.DomainShutoffShutdown {
			return false, true
		}
		return true, false
	}
}
