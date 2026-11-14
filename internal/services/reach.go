package services

import (
	"errors"
	"log"
	"strings"
	"time"
)

// While a service builds, the manager polls each VM's install over SSH. The
// last result, or why SSH failed, is kept for the console so a stuck build
// shows its cause rather than just "Installing". A failed build keeps its
// last looks until the service is retried or deleted.

// memberLook is the last look at one VM of a service being built.
type memberLook struct {
	// state is what the VM's install reported: waiting, installing, ready
	// or failed, with failure saying why; empty while it cannot be asked.
	state   string
	failure string
	// unreachable says why it could not be asked, since when.
	unreachable string
	since       time.Time
}

// observe records a look at a VM's install: its state, or the error that
// prevented reading it. Each new error reason is logged once.
func (m *Manager) observe(service, vm, ip, state string, err error) {
	m.reachMu.Lock()
	defer m.reachMu.Unlock()
	looks := m.reach[service]
	if looks == nil {
		looks = map[string]memberLook{}
		m.reach[service] = looks
	}
	prev := looks[vm]
	if err == nil {
		looks[vm] = memberLook{state: state}
		if prev.unreachable != "" {
			log.Printf("services: %s: %s answers over SSH again", service, vm)
		}
		return
	}
	why := sshReason(err)
	look := memberLook{unreachable: why, since: prev.since}
	if prev.unreachable == "" {
		look.since = time.Now()
	}
	looks[vm] = look
	if why != prev.unreachable {
		log.Printf("services: %s: cannot ask %s at %s how its install is doing: %v", service, vm, ip, err)
	}
}

// lookFailed records that a VM's install failed, and why.
func (m *Manager) lookFailed(service, vm, why string) {
	m.reachMu.Lock()
	defer m.reachMu.Unlock()
	if m.reach[service] == nil {
		m.reach[service] = map[string]memberLook{}
	}
	m.reach[service][vm] = memberLook{state: "failed", failure: why}
}

// looksAt returns the last looks at a service's VMs.
func (m *Manager) looksAt(service string) map[string]memberLook {
	m.reachMu.Lock()
	defer m.reachMu.Unlock()
	out := map[string]memberLook{}
	for vm, l := range m.reach[service] {
		out[vm] = l
	}
	return out
}

// forgetLooks drops the looks at a service whose build is over.
func (m *Manager) forgetLooks(service string) {
	m.reachMu.Lock()
	defer m.reachMu.Unlock()
	delete(m.reach, service)
}

// sshReason says why SSH to a VM failed, for a person: the network, the
// SSH server, or the key. The library's error follows in brackets.
func sshReason(err error) string {
	raw := err.Error()
	var why string
	switch {
	case errors.Is(err, ErrHostKeyChanged):
		why = "it presented a different SSH host key"
	case strings.Contains(raw, "connection refused"):
		why = "nothing listens on its SSH port"
	case strings.Contains(raw, "unable to authenticate"):
		why = "it does not accept nano-cloud's SSH key"
	case strings.Contains(raw, "no route to host"), strings.Contains(raw, "host is down"), strings.Contains(raw, "network is unreachable"):
		why = "its address cannot be reached from this host"
	case strings.Contains(raw, "i/o timeout"), strings.Contains(raw, "deadline exceeded"):
		why = "SSH does not answer at its address"
	case strings.Contains(raw, "handshake failed"):
		why = "SSH starts but does not finish connecting"
	default:
		why = "SSH fails"
	}
	return why + " (" + truncate(raw, 160) + ")"
}
