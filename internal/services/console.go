package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/events"
)

// Console features beyond building: install logs, retrying a failed service,
// and activity feed notes.

// LogView is the end of one VM's install log.
type LogView struct {
	VM string `json:"vm"`
	// State is the install's own: installing, waiting, ready or "failed: …",
	// or cloud-init's status before the install has started.
	State string `json:"state"`
	Log   string `json:"log"`
}

// Log line counts: what a reader gets unless they ask, and the most they can.
const (
	defaultLogLines = 200
	maxLogLines     = 1000
)

// Log returns the tail of one VM's install log, read over SSH, during or
// after the build.
func (m *Manager) Log(ctx context.Context, name, vm string, lines int) (LogView, error) {
	api, err := m.client()
	if err != nil {
		return LogView{}, err
	}
	inst, err := m.store.Get(name)
	if err != nil {
		return LogView{}, err
	}
	member := false
	for _, mem := range inst.members() {
		member = member || mem.VM == vm
	}
	if !member {
		return LogView{}, errorOf(ErrNotFound, "%s has no VM named %q", name, vm)
	}
	if lines <= 0 {
		lines = defaultLogLines
	}
	lines = min(lines, maxLogLines)

	vms, err := api.vms(ctx)
	if err != nil {
		return LogView{}, listError(err)
	}
	v, ok := vms[vm]
	switch {
	case !ok:
		return LogView{}, errorOf(ErrNotFound, "the VM %s no longer exists", vm)
	case v.Phase != "ready" || v.IP == "":
		return LogView{}, errorOf(ErrNotReady, "%s is not up yet; its log can be read once it has an address", vm)
	}
	key := inst.hostKey(vm)
	state, _, err := m.guest.Run(ctx, v.IP, key, probeCmd)
	if errors.Is(err, ErrHostKeyChanged) {
		return LogView{}, fmt.Errorf("%s presented a different SSH host key from the one first seen", vm)
	}
	if err != nil {
		return LogView{}, errorOf(ErrNotReady, "nano-cloud cannot read %s's log at %s: %s", vm, v.IP, sshReason(err))
	}
	out, _, err := m.guest.Run(ctx, v.IP, key, fmt.Sprintf("tail -n %d %s 2>/dev/null; true", lines, logFile))
	if err != nil {
		return LogView{}, errorOf(ErrNotReady, "nano-cloud cannot read %s's log at %s: %s", vm, v.IP, sshReason(err))
	}
	return LogView{VM: vm, State: strings.TrimSpace(state), Log: out}, nil
}

// rerunCmd starts the install script again, detached from the SSH session.
// The state goes back to installing first, so a look in between does not
// find the old failure.
var rerunCmd = "echo installing | sudo -n tee " + stateFile + " >/dev/null && " +
	"sudo -n systemd-run --quiet --collect --unit=nanocloud-service-install-$(date +%s) " + installPath

// Retry resumes a failed service at the step it failed in. A failed install
// reruns on each VM where it failed; recipe installs are safe to rerun.
func (m *Manager) Retry(ctx context.Context, name string) (View, error) {
	inst, err := m.store.Get(name)
	if err != nil {
		return View{}, err
	}
	if inst.State != StateFailed {
		return View{}, errorOf(ErrNotReady, "%s has not failed, so there is nothing to retry", name)
	}
	from := inst.FailedIn
	if !building(from) {
		return View{}, errorOf(ErrNotReady, "%s cannot be picked up again; delete it and launch it again", name)
	}
	if from == StateInstalling {
		if err := m.rerunInstalls(ctx, inst); err != nil {
			return View{}, err
		}
	}
	if err := m.store.Update(name, func(i *Instance) {
		i.State, i.FailedIn, i.Reason = from, "", ""
		i.RetriedAtUnix = time.Now().Unix()
	}); err != nil {
		return View{}, err
	}
	m.forgetLooks(name) // the failed build's; this one looks afresh
	m.note(name, fmt.Sprintf("Retrying %s from %s.", name, stepName(from)))
	m.startWorker(name)
	return m.Get(ctx, name)
}

// rerunInstalls restarts the install on each VM whose install failed. Every
// VM must be up, since a stopped one cannot report its install state.
func (m *Manager) rerunInstalls(ctx context.Context, inst Instance) error {
	api, err := m.client()
	if err != nil {
		return err
	}
	vms, err := api.vms(ctx)
	if err != nil {
		return listError(err)
	}
	for _, mem := range inst.members() {
		v, ok := vms[mem.VM]
		if !ok {
			return errorOf(ErrNotReady, "the VM %s no longer exists; delete %s and launch it again", mem.VM, inst.Name)
		}
		if v.Phase != "ready" || v.IP == "" {
			return errorOf(ErrNotReady, "%s is not running; start it, then retry", mem.VM)
		}
		state, _, err := m.guest.Run(ctx, v.IP, inst.hostKey(mem.VM), probeCmd)
		if err != nil {
			return errorOf(ErrNotReady, "%s does not answer over SSH; try again in a moment", mem.VM)
		}
		if !strings.HasPrefix(strings.TrimSpace(state), "failed") {
			continue
		}
		if _, _, err := m.guest.Run(ctx, v.IP, inst.hostKey(mem.VM), rerunCmd); err != nil {
			return fmt.Errorf("start the install again on %s: %w", mem.VM, err)
		}
	}
	return nil
}

// stepName names a build step in a sentence: "Retrying db from the install."
func stepName(state string) string {
	switch state {
	case StatePulling:
		return "the download"
	case StateCreating:
		return "creating its VMs"
	case StateWaitingIP:
		return "waiting for addresses"
	case StateInstalling:
		return "the install"
	}
	return state
}

// note adds a step of a service's life to the activity feed.
func (m *Manager) note(name, message string) {
	m.events.Append(events.Event{Level: events.LevelInfo, Kind: events.KindServiceProgress, VM: name, Message: message})
}

// stepNote is what the feed says when a service gets through a build step.
func stepNote(inst Instance, step string) string {
	n := len(inst.members())
	switch step {
	case StatePulling:
		return fmt.Sprintf("Downloaded the base image for %s.", inst.Name)
	case StateCreating:
		if n == 1 {
			return fmt.Sprintf("Created %s's VM.", inst.Name)
		}
		return fmt.Sprintf("Created %s's %d VMs.", inst.Name, n)
	case StateWaitingIP:
		if n == 1 {
			return fmt.Sprintf("%s's VM has an address.", inst.Name)
		}
		return fmt.Sprintf("All %d of %s's VMs have an address.", n, inst.Name)
	}
	return ""
}
