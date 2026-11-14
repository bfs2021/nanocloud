package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// member returns a service's view of one of its VMs.
func (h *harness) member(t *testing.T, name, vm string) MemberView {
	t.Helper()
	v, err := h.m.Get(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	for _, mv := range v.Members {
		if mv.VM == vm {
			return mv
		}
	}
	t.Fatalf("%s has no member %s", name, vm)
	return MemberView{}
}

func (h *harness) eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// An unreachable control plane is shown in the console with the machine,
// the reason and since when.
func TestBuild_SaysWhichMachineCannotBeAskedAndWhy(t *testing.T) {
	h := newHarness(t)
	h.guest.mu.Lock()
	h.guest.down = map[string]error{"10.0.0.5": errors.New("dial tcp 10.0.0.5:22: i/o timeout")}
	h.guest.states = map[string]string{"10.0.0.6": "waiting", "10.0.0.7": "waiting"}
	h.guest.mu.Unlock()
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "lab", Recipe: "kubernetes_cluster", Params: map[string]string{"workers": "2"}}); err != nil {
		t.Fatal(err)
	}

	h.eventually(t, "the server shown unreachable", func() bool { return h.member(t, "lab", "lab-server").Unreachable != "" })
	server := h.member(t, "lab", "lab-server")
	if !strings.HasPrefix(server.Unreachable, "SSH does not answer at its address (dial tcp 10.0.0.5:22: i/o timeout)") || server.UnreachableSince == 0 || server.Install != "" {
		t.Errorf("server = %+v", server)
	}
	h.eventually(t, "the workers seen waiting", func() bool { return h.member(t, "lab", "lab-agent-1").Install == "waiting" })
	if w := h.member(t, "lab", "lab-agent-1"); w.WaitsFor != "Control plane" || w.Unreachable != "" {
		t.Errorf("worker = %+v", w)
	}
	if _, err := h.m.Log(context.Background(), "lab", "lab-server", 10); err == nil || !strings.Contains(err.Error(), "SSH does not answer at its address") {
		t.Errorf("log of the unreachable server: %v", err)
	}

	// It answers again: the reason goes, and the build carries on to ready.
	h.guest.mu.Lock()
	h.guest.down = nil
	h.guest.states["10.0.0.5"] = "ready"
	h.guest.mu.Unlock()
	h.eventually(t, "the server read as installed", func() bool { return h.member(t, "lab", "lab-server").Install == "ready" })
	if s := h.member(t, "lab", "lab-server"); s.Unreachable != "" || s.UnreachableSince != 0 {
		t.Errorf("server still shown unreachable: %+v", s)
	}
	h.eventually(t, "the workers handed the address", func() bool { return h.member(t, "lab", "lab-agent-2").Install == "installing" })
	h.guest.setFor("10.0.0.6", "ready")
	h.guest.setFor("10.0.0.7", "ready")
	h.waitState(t, "lab", StateReady)
	if s := h.member(t, "lab", "lab-server"); s.Install != "" || s.WaitsFor != "" {
		t.Errorf("a ready service still shows its build: %+v", s)
	}
}

func TestSSHReason(t *testing.T) {
	for raw, want := range map[string]string{
		"dial tcp 192.168.64.10:22: connect: connection refused":                                       "nothing listens on its SSH port",
		"ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey]":       "it does not accept nano-cloud's SSH key",
		"dial tcp 192.168.64.10:22: connect: no route to host":                                         "its address cannot be reached from this host",
		"dial tcp 192.168.64.10:22: i/o timeout":                                                       "SSH does not answer at its address",
		"ssh: handshake failed: read tcp 192.168.64.1:50123->192.168.64.10:22: read: connection reset": "SSH starts but does not finish connecting",
	} {
		if got := sshReason(errors.New(raw)); !strings.HasPrefix(got, want+" (") {
			t.Errorf("sshReason(%q) = %q, want %q first", raw, got, want)
		}
	}
}
