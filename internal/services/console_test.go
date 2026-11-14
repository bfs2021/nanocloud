package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLog_ReadsAVMsInstallLog(t *testing.T) {
	h := readyPostgres(t)
	lv, err := h.m.Log(context.Background(), "db", "db", 0)
	if err != nil {
		t.Fatal(err)
	}
	if lv.VM != "db" || lv.State != "ready" || !strings.Contains(lv.Log, "Unable to locate package") {
		t.Errorf("log = %+v", lv)
	}
	h.guest.mu.Lock()
	last := h.guest.cmds[len(h.guest.cmds)-1]
	h.guest.mu.Unlock()
	if last != "tail -n 200 "+logFile+" 2>/dev/null; true" {
		t.Errorf("read the log with %q, want the last 200 lines", last)
	}

	if _, err := h.m.Log(context.Background(), "db", "other", 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("a VM of another service: %v", err)
	}
	if _, err := h.m.Log(context.Background(), "db", "db", 5000); err != nil {
		t.Fatal(err)
	}
	h.guest.mu.Lock()
	last = h.guest.cmds[len(h.guest.cmds)-1]
	h.guest.mu.Unlock()
	if !strings.HasPrefix(last, "tail -n 1000 ") {
		t.Errorf("asked for 5000 lines, read with %q; want at most 1000", last)
	}
}

func TestLog_WaitsForTheVMToHaveAnAddress(t *testing.T) {
	h := newHarness(t)
	h.api.with(func() { h.api.noBoot = true })
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres"}); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, "db", StateWaitingIP)
	if _, err := h.m.Log(context.Background(), "db", "db", 0); !errors.Is(err, ErrNotReady) {
		t.Errorf("before the VM has an address: %v, want ErrNotReady", err)
	}
}

func TestRetry_RunsAFailedInstallAgain(t *testing.T) {
	h := newHarness(t)
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres"}); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, "db", StateInstalling)
	h.guest.set("failed: apt-get update (line 4)")
	h.waitState(t, "db", StateFailed)

	v, err := h.m.Retry(context.Background(), "db")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != StateInstalling || v.Reason != "" || v.FailedIn != "" {
		t.Errorf("after retry: %s (%q, failed in %q)", v.State, v.Reason, v.FailedIn)
	}
	h.guest.mu.Lock()
	reruns := append([]string(nil), h.guest.reruns...)
	h.guest.mu.Unlock()
	if len(reruns) != 1 || reruns[0] != "10.0.0.5" {
		t.Errorf("ran the install again on %v, want the failed VM once", reruns)
	}

	h.guest.setFor("10.0.0.5", "ready")
	h.waitState(t, "db", StateReady)
	inst, _ := h.store.Get("db")
	if inst.RetriedAtUnix == 0 {
		t.Error("the retry was not recorded")
	}
	found := false
	for _, ev := range h.waitEvents(t, 6) {
		found = found || ev.Message == "Retrying db from the install."
	}
	if !found {
		t.Error("the retry is not in the activity feed")
	}
}

// In a cluster only the worker whose install failed runs it again.
func TestRetry_RunsTheInstallAgainOnlyWhereItFailed(t *testing.T) {
	h := newHarness(t)
	h.guest.setFor("10.0.0.5", "ready")
	h.guest.setFor("10.0.0.6", "failed: curl -sfL https://get.k3s.io (line 14)")
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "lab", Recipe: "kubernetes_cluster", Params: map[string]string{"workers": "1"}}); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, "lab", StateFailed)
	if _, err := h.m.Retry(context.Background(), "lab"); err != nil {
		t.Fatal(err)
	}
	h.guest.mu.Lock()
	reruns := append([]string(nil), h.guest.reruns...)
	h.guest.mu.Unlock()
	if len(reruns) != 1 || reruns[0] != "10.0.0.6" {
		t.Errorf("ran the install again on %v, want only the failed worker", reruns)
	}
	h.guest.setFor("10.0.0.6", "ready")
	h.waitState(t, "lab", StateReady)
}

// After a retry, a VM without an address gets the full wait again even if
// it has been up longer.
func TestRetry_GivesAVMWithoutNetworkTheWholeWaitAgain(t *testing.T) {
	h := newHarness(t)
	h.m.noNetworkAfter = time.Minute
	h.api.with(func() { h.api.noBoot = true })
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres"}); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, "db", StateWaitingIP)
	h.api.with(func() {
		*h.api.vms["db"] = vmView{ID: "db", Status: "running", Phase: "no-network", Uptime: 120}
	})
	h.waitState(t, "db", StateFailed)

	if _, err := h.m.Retry(context.Background(), "db"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if v, _ := h.m.Get(context.Background(), "db"); v.State != StateWaitingIP {
		t.Fatalf("gave up again at once: %s (%s)", v.State, v.Reason)
	}
	h.api.with(func() {
		*h.api.vms["db"] = vmView{ID: "db", Status: "running", Phase: "ready", IP: "10.0.0.5", Uptime: 125}
	})
	h.waitState(t, "db", StateInstalling)
}

func TestRetry_OnlyAFailedService(t *testing.T) {
	h := readyPostgres(t)
	if _, err := h.m.Retry(context.Background(), "db"); !errors.Is(err, ErrNotReady) {
		t.Errorf("retry a ready service: %v, want ErrNotReady", err)
	}
	if _, err := h.m.Retry(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("retry an unknown service: %v", err)
	}
}

func TestCreate_NotesTheHandOverToEachWorker(t *testing.T) {
	h := newHarness(t)
	h.guest.set("waiting")
	h.guest.setFor("10.0.0.5", "ready")
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "lab", Recipe: "kubernetes_cluster", Params: map[string]string{"workers": "1"}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		evs, _ := h.events.Since(0, 0)
		for _, ev := range evs {
			if ev.Message == "Handed the control plane's address to lab-agent." && ev.VM == "lab" {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no hand-over note in %+v", evs)
		}
		time.Sleep(time.Millisecond)
	}
}
