package services

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/events"
)

func TestPortCandidates_StartWithTheEndpointsOwnLastDigits(t *testing.T) {
	got := portCandidates(30000, 10000, 5432)
	if len(got) != 10000 {
		t.Fatalf("%d candidates, want every port in the range once", len(got))
	}
	for i, want := range []int{30432, 31432, 32432, 33432, 34432, 35432, 36432, 37432, 38432, 39432, 30000, 30001} {
		if got[i] != want {
			t.Fatalf("candidates start %v, want 30432, 31432, … then the rest in order", got[:12])
		}
	}
	if got := portCandidates(30000, 10000, 6443)[0]; got != 30443 {
		t.Errorf("Kubernetes API first tries %d, want 30443", got)
	}
}

func TestAllocatePorts_SkipsTakenAndBusyPorts(t *testing.T) {
	m := newManager(NewStore(filepath.Join(t.TempDir(), "s.json")), events.New(1), &fakeGuest{}, "key")
	m.portFree = func(p int) bool { return p != 31432 }
	recipe, _ := RecipeByID("postgres")
	taken := map[int]bool{30432: true}
	ports := m.allocatePorts(recipe.Endpoints, nil, taken)
	if ports["db"] != 32432 || !taken[32432] {
		t.Errorf("ports = %v, taken %v; want 32432: 30432 is another service's, 31432 another program's", ports, taken)
	}
	// An endpoint that has a port keeps it.
	if again := m.allocatePorts(recipe.Endpoints, ports, taken); again["db"] != 32432 {
		t.Errorf("re-allocated %v", again)
	}
}

// A service stored before the gateway existed gets its ports at startup.
func TestRun_GivesPortsToServicesStoredBeforeTheGateway(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "services.json"))
	if err := store.Create(Instance{Name: "db", Recipe: "postgres", State: StateReady, VMs: []string{"db"}, Params: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	h := newHarnessWith(t, store)
	deadline := time.Now().Add(5 * time.Second)
	for {
		inst, _ := store.Get("db")
		if inst.Ports["db"] == 30432 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ports = %v, want db on 30432", inst.Ports)
		}
		time.Sleep(time.Millisecond)
	}
	waitListening(t, h, "db", "db")
}

func waitListening(t *testing.T, h *harness, service, endpoint string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ok, _ := h.m.gw.status(service, endpoint); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the gateway is not listening for %s/%s", service, endpoint)
		}
		time.Sleep(time.Millisecond)
	}
}

func readyPostgres(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.guest.set("ready")
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres"}); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, "db", StateReady)
	return h
}

func TestView_AddressesReadAsTheViewerReachesThem(t *testing.T) {
	h := readyPostgres(t)
	address := func(host string) (string, string) {
		t.Helper()
		v, err := h.m.Get(ViewedFrom(context.Background(), host), "db")
		if err != nil {
			t.Fatal(err)
		}
		var url string
		for _, o := range v.Outputs {
			if o.ID == "host" {
				url = o.Value
			}
		}
		return v.Endpoints[0].Address, url
	}

	// Not shared: localhost, for anyone; the UI tells a remote viewer why.
	if addr, out := address("mac-studio.local:8080"); addr != "localhost:30432" || out != "localhost:30432" {
		t.Errorf("unshared, from another machine: %s / %s", addr, out)
	}

	v, err := h.m.SetShared(context.Background(), "db", true)
	if err != nil || !v.Shared {
		t.Fatalf("share: %+v, %v", v, err)
	}
	h.m.gw.mu.Lock()
	shared := h.m.gw.open["db/db"].b.shared
	h.m.gw.mu.Unlock()
	if !shared {
		t.Error("sharing did not reopen the port on every interface")
	}
	for host, want := range map[string]string{
		"mac-studio.local:8080": "mac-studio.local:30432",
		"192.168.1.20:8080":     "192.168.1.20:30432",
		"[fd00::7]:8080":        "[fd00::7]:30432",
		"localhost:8080":        "localhost:30432",
		"127.0.0.1:8080":        "localhost:30432",
		"[::1]:8080":            "localhost:30432",
		"evil;rm -rf:8080":      "localhost:30432",
		"":                      "localhost:30432",
	} {
		if addr, out := address(host); addr != want || out != want {
			t.Errorf("shared, viewed at %q: endpoint %s, output %s; want %s", host, addr, out, want)
		}
	}
}

func TestResolveEndpoint_FindsTheVMNowOrSaysWhy(t *testing.T) {
	h := readyPostgres(t)
	h.m.vmCacheTTL = 0
	ctx := context.Background()
	if target, err := h.m.resolveEndpoint(ctx, "db", "db"); err != nil || target != "10.0.0.5:5432" {
		t.Fatalf("resolve = %q, %v", target, err)
	}
	if err := h.m.Stop(ctx, "db"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.resolveEndpoint(ctx, "db", "db"); err == nil || err.Error() != "db is stopped" {
		t.Errorf("stopped: %v", err)
	}
	h.api.with(func() { delete(h.api.vms, "db") })
	if _, err := h.m.resolveEndpoint(ctx, "db", "db"); err == nil || err.Error() != "db's VM no longer exists" {
		t.Errorf("VM deleted: %v", err)
	}
	if _, err := h.m.resolveEndpoint(ctx, "gone", "db"); err == nil || err.Error() != "gone no longer exists" {
		t.Errorf("unknown service: %v", err)
	}
}

func TestDelete_ClosesTheServicesPorts(t *testing.T) {
	h := readyPostgres(t)
	waitListening(t, h, "db", "db")
	if err := h.m.Delete(context.Background(), "db"); err != nil {
		t.Fatal(err)
	}
	if ok, problem := h.m.gw.status("db", "db"); ok || problem != "" {
		t.Errorf("after delete: listening %v, %q", ok, problem)
	}
}

// Two services of a kind get the next thousand.
func TestCreate_GivesEachServiceItsOwnPorts(t *testing.T) {
	h := readyPostgres(t)
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db2", Recipe: "postgres"}); err != nil {
		t.Fatal(err)
	}
	inst, _ := h.store.Get("db2")
	if inst.Ports["db"] != 31432 {
		t.Errorf("second database on %v, want 31432", inst.Ports)
	}
}
