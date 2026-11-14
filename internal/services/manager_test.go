package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/events"
)

// fakeAPI stands in for the daemon's own API: an image catalog, a pull that
// finishes on the next look, and VMs that boot and get an address on the
// next poll after they are created.
type fakeAPI struct {
	mu       sync.Mutex
	images   map[string]*catalogImage
	pullErr  string
	vms      map[string]*vmView
	booting  map[string]bool
	created  []vmCreate
	deleted  []string
	started  []string
	stopped  []string
	fail503  int // answer GET /vms with 503 this many times
	createOK bool
	noBoot   bool // created VMs never get an address
	ips      map[string]string
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		images: map[string]*catalogImage{
			"ubuntu-2404-standard": {ID: "ubuntu-2404-standard", Status: "available"},
			"debian-12":            {ID: "debian-12", Status: "available"},
		},
		vms:      map[string]*vmView{},
		booting:  map[string]bool{},
		createOK: true,
		ips:      map[string]string{},
	}
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/images/catalog":
		var out []catalogImage
		for _, img := range f.images {
			if img.Status == "pulling" {
				// Seen pulling once; finished (or failed) by the next look.
				defer func(img *catalogImage) {
					if f.pullErr != "" {
						img.Status = "available"
					} else {
						img.Status = "cached"
					}
				}(img)
			}
			out = append(out, *img)
		}
		json.NewEncoder(w).Encode(out) //nolint:errcheck
	case r.Method == http.MethodPost && path == "/images/pull":
		var body struct{ ID string }
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		img := f.images[body.ID]
		if img.Status != "available" {
			http.Error(w, "already", http.StatusConflict)
			return
		}
		img.Status, img.Progress = "pulling", 40
		w.WriteHeader(http.StatusAccepted)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/status"):
		json.NewEncoder(w).Encode(map[string]any{"done": true, "error": f.pullErr}) //nolint:errcheck
	case r.Method == http.MethodGet && path == "/vms":
		if f.fail503 > 0 {
			f.fail503--
			http.Error(w, "libvirt is not reachable", http.StatusServiceUnavailable)
			return
		}
		out := []vmView{}
		for name, vm := range f.vms {
			out = append(out, *vm)
			if f.booting[name] {
				ip := f.ips[name]
				if ip == "" {
					ip = "10.0.0.5"
				}
				vm.Status, vm.Phase, vm.IP = "running", "ready", ip
				delete(f.booting, name)
			}
		}
		json.NewEncoder(w).Encode(out) //nolint:errcheck
	case r.Method == http.MethodPost && path == "/vms":
		if !f.createOK {
			http.Error(w, "xorriso is not installed on this host", http.StatusUnprocessableEntity)
			return
		}
		var req vmCreate
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		if _, exists := f.vms[req.Name]; exists {
			http.Error(w, "exists", http.StatusConflict)
			return
		}
		f.created = append(f.created, req)
		// Each VM its own address: the first 10.0.0.5, the next .6, ...
		f.ips[req.Name] = fmt.Sprintf("10.0.0.%d", 4+len(f.created))
		f.vms[req.Name] = &vmView{ID: req.Name, Status: "running", Phase: "booting"}
		f.booting[req.Name] = !f.noBoot
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/vms/"):
		name := strings.TrimPrefix(path, "/vms/")
		delete(f.vms, name)
		f.deleted = append(f.deleted, name)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/start"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/vms/"), "/start")
		f.started = append(f.started, name)
		f.vms[name].Status, f.vms[name].Phase = "running", "booting"
		f.booting[name] = true
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/stop"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/vms/"), "/stop")
		f.stopped = append(f.stopped, name)
		f.vms[name].Status, f.vms[name].Phase, f.vms[name].IP = "stopped", "stopped", ""
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (f *fakeAPI) with(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// fakeGuest answers the manager's SSH commands. A VM's state is states[ip]
// when set, state otherwise; a hand-over moves a waiting VM on to installing,
// as its script would.
type fakeGuest struct {
	mu        sync.Mutex
	state     string
	states    map[string]string
	hostKey   string
	files     map[string]string
	cmds      []string
	env       map[string]string
	checkFail bool
	reruns    []string
	// cluster is what kubectl get answers; drainFail and applyFail make
	// those kubectl commands fail.
	cluster   string
	drainFail bool
	applyFail bool
	// inputs are what Send gave each VM, by address and then command;
	// setupFail makes an exporter's setup fail, by its job, and
	// provisionFail the monitoring setup.
	inputs        map[string]map[string][]byte
	setupFail     map[string]bool
	provisionFail bool
	// down makes SSH to an address fail with that error.
	down map[string]error
}

func (g *fakeGuest) Send(ctx context.Context, ip, hostKey, cmd string, input []byte) (string, string, error) {
	g.mu.Lock()
	if g.inputs == nil {
		g.inputs = map[string]map[string][]byte{}
	}
	if g.inputs[ip] == nil {
		g.inputs[ip] = map[string][]byte{}
	}
	g.inputs[ip][cmd] = append([]byte(nil), input...)
	g.mu.Unlock()
	return g.Run(ctx, ip, hostKey, cmd)
}

// input returns what Send last gave the VM at ip with a command containing sub.
func (g *fakeGuest) input(ip, sub string) []byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	for cmd, in := range g.inputs[ip] {
		if strings.Contains(cmd, sub) {
			return in
		}
	}
	return nil
}

func (g *fakeGuest) Run(_ context.Context, ip string, hostKey, cmd string) (string, string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if hostKey != "" && hostKey != g.hostKey {
		return "", g.hostKey, ErrHostKeyChanged
	}
	if err := g.down[ip]; err != nil {
		return "", "", err
	}
	g.cmds = append(g.cmds, cmd)
	switch {
	case strings.Contains(cmd, "kubectl get nodes,deployments"):
		return g.cluster, g.hostKey, nil
	case strings.Contains(cmd, "kubectl drain"):
		if g.drainFail {
			return "", g.hostKey, errors.New("Process exited with status 1: cannot evict pod as it would violate the pod's disruption budget")
		}
		return "node/x drained\n", g.hostKey, nil
	case strings.Contains(cmd, "kubectl uncordon"), strings.Contains(cmd, "kubectl delete node"):
		return "", g.hostKey, nil
	case strings.Contains(cmd, "kubectl apply"):
		if g.applyFail {
			return "error: error parsing STDIN: yaml: line 2: mapping values are not allowed\n", g.hostKey, errors.New("Process exited with status 1")
		}
		return "deployment.apps/hello created\n", g.hostKey, nil
	case strings.Contains(cmd, "nano-cloud metrics: "):
		job := strings.Fields(cmd[strings.Index(cmd, "nano-cloud metrics: ")+len("nano-cloud metrics: "):])[0]
		if g.setupFail[job] {
			return "", g.hostKey, errors.New("Process exited with status 100: E: Unable to locate package prometheus-" + job + "-exporter")
		}
		return "", g.hostKey, nil
	case strings.Contains(cmd, "nano-cloud monitoring setup"):
		if g.provisionFail {
			return "", g.hostKey, errors.New("Process exited with status 1: promtool: command not found")
		}
		return "", g.hostKey, nil
	case strings.Contains(cmd, "nanocloud.d/.targets.tmp"):
		return "", g.hostKey, nil
	case cmd == probeCmd:
		if st, ok := g.states[ip]; ok {
			return st + "\n", g.hostKey, nil
		}
		return g.state + "\n", g.hostKey, nil
	case strings.Contains(cmd, "| base64 -d |"):
		enc := strings.Fields(cmd)[1]
		data, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return "", g.hostKey, err
		}
		if g.env == nil {
			g.env = map[string]string{}
		}
		g.env[ip] = string(data)
		if g.states == nil {
			g.states = map[string]string{}
		}
		g.states[ip] = "installing"
		return "", g.hostKey, nil
	case strings.HasPrefix(cmd, "test "):
		if g.checkFail {
			return "", g.hostKey, errors.New("exit status 1")
		}
		return "", g.hostKey, nil
	case strings.Contains(cmd, "systemd-run"):
		// Retry starting the install again: it is installing once more.
		g.reruns = append(g.reruns, ip)
		if g.states == nil {
			g.states = map[string]string{}
		}
		g.states[ip] = "installing"
		return "", g.hostKey, nil
	case strings.HasPrefix(cmd, "tail "):
		return "E: Unable to locate package postgresql\n", g.hostKey, nil
	case strings.HasPrefix(cmd, "sudo -n cat -- "):
		path := strings.Trim(strings.TrimPrefix(cmd, "sudo -n cat -- "), "'")
		return g.files[path], g.hostKey, nil
	}
	return "", g.hostKey, errors.New("unexpected command " + cmd)
}

func (g *fakeGuest) set(state string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.state = state
}

func (g *fakeGuest) setFor(ip, state string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.states == nil {
		g.states = map[string]string{}
	}
	g.states[ip] = state
}

type harness struct {
	m      *Manager
	api    *fakeAPI
	guest  *fakeGuest
	store  *Store
	events *events.Ring
	cancel context.CancelFunc
	done   chan struct{}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "services.json"))
	return newHarnessWith(t, store)
}

// newHarnessWith starts a manager over store. prepare runs before the
// manager resumes anything, so leftover VMs are in place when it looks.
func newHarnessWith(t *testing.T, store *Store, prepare ...func(*harness)) *harness {
	t.Helper()
	h := &harness{
		api:    newFakeAPI(),
		guest:  &fakeGuest{state: "installing", hostKey: "ssh-ed25519 HOSTKEY"},
		store:  store,
		events: events.New(10),
	}
	h.m = newManager(store, h.events, h.guest, "ssh-ed25519 DAEMONKEY nano-cloud")
	h.m.poll = time.Millisecond
	h.m.dial = func(context.Context, string) error { return nil }
	// The gateway appears to get the ports it asks for but listens on
	// kernel-chosen ones, so tests never hold the real 30000s.
	h.m.portFree = func(int) bool { return true }
	h.m.gw.listen = func(string, string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }
	h.m.Attach(h.api)
	for _, p := range prepare {
		p(h)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel, h.done = cancel, make(chan struct{})
	go func() {
		h.m.Run(ctx)
		close(h.done)
	}()
	t.Cleanup(h.stop)
	return h
}

func (h *harness) stop() {
	h.cancel()
	<-h.done
}

// waitState waits for a service to reach want, failing on any other end state.
func (h *harness) waitState(t *testing.T, name, want string) View {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		v, err := h.m.Get(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		if v.State == want {
			return v
		}
		if (v.State == StateFailed || v.State == StateReady) && v.State != want {
			t.Fatalf("%s ended %s (%s), want %s", name, v.State, v.Reason, want)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s still %s, want %s", name, v.State, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// waitEvents waits for the ring to hold n events. The state is saved just
// before its event is appended, so seeing the state is not enough.
func (h *harness) waitEvents(t *testing.T, n int) []events.Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		evs, _ := h.events.Since(0, 0)
		if len(evs) >= n || time.Now().After(deadline) {
			return evs
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCreate_BuildsAServiceToReady(t *testing.T) {
	h := newHarness(t)
	v, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres", Params: map[string]string{"database": "shop"}})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != StatePulling || v.Size.ID != "medium" {
		t.Fatalf("created view = %+v", v)
	}

	h.waitState(t, "db", StateInstalling)
	h.guest.set("ready")
	v = h.waitState(t, "db", StateReady)

	h.api.with(func() {
		if len(h.api.created) != 1 {
			t.Fatalf("created %d VMs, want 1", len(h.api.created))
		}
		req := h.api.created[0]
		if req.Name != "db" || req.ImageID != "ubuntu-2404-standard" || req.Service != "db" {
			t.Errorf("create request = %+v", req)
		}
		if req.VCPUs != 2 || req.MemoryMB != 2048 || req.DiskGB != 20 {
			t.Errorf("size = %d/%d/%d, want the medium postgres size", req.VCPUs, req.MemoryMB, req.DiskGB)
		}
		ci := req.CloudInit
		if ci.Username != serviceUser || ci.SSHPublicKey != "ssh-ed25519 DAEMONKEY nano-cloud" || ci.Password == "" {
			t.Errorf("cloud-init = %+v", ci)
		}
		if len(ci.RunCmd) != 1 || ci.RunCmd[0] != installPath || len(ci.WriteFiles) != 1 || ci.WriteFiles[0].Path != installPath {
			t.Errorf("install wiring: runcmd %v, files %+v", ci.RunCmd, ci.WriteFiles)
		}
		if !strings.Contains(ci.WriteFiles[0].Content, `CREATE DATABASE "shop"`) {
			t.Errorf("install script does not create the requested database")
		}
	})

	if v.IP != "10.0.0.5" {
		t.Errorf("ip = %q", v.IP)
	}
	inst, _ := h.store.Get("db")
	// Through the gateway, on PostgreSQL's port in the 30000s.
	wantURL := "postgres://app:" + inst.Params["password"] + "@localhost:30432/shop"
	var url OutputValue
	for _, o := range v.Outputs {
		if o.ID == "url" {
			url = o
		}
	}
	if url.Value != wantURL || !url.Secret {
		t.Errorf("url output = %+v, want %q marked secret", url, wantURL)
	}
	want := EndpointView{ID: "db", Label: "PostgreSQL", Port: 5432, HostPort: 30432, Address: "localhost:30432", Direct: "10.0.0.5:5432"}
	if len(v.Endpoints) != 1 || v.Endpoints[0] != want {
		t.Errorf("endpoints = %+v, want %+v", v.Endpoints, want)
	}
	if len(inst.Params["password"]) != 24 {
		t.Errorf("generated password %q is not 24 characters", inst.Params["password"])
	}
	if got := inst.hostKey("db"); got != "ssh-ed25519 HOSTKEY" {
		t.Errorf("host key not recorded on first contact: %q", got)
	}
	// Each step a note in the service's activity, then ready.
	evs := h.waitEvents(t, 4)
	var got []string
	for _, ev := range evs {
		got = append(got, ev.Kind+": "+ev.Message)
	}
	wantEvents := []string{
		"service.progress: Downloaded the base image for db.",
		"service.progress: Created db's VM.",
		"service.progress: db's VM has an address.",
		"service.ready: PostgreSQL db is ready.",
	}
	if strings.Join(got, "\n") != strings.Join(wantEvents, "\n") {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantEvents, "\n"))
	}
}

func TestCreate_ReportsAFailedInstallWithItsLog(t *testing.T) {
	h := newHarness(t)
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres"}); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, "db", StateInstalling)
	h.guest.set("failed: apt-get install -y postgresql (line 12)")
	v := h.waitState(t, "db", StateFailed)
	if !strings.Contains(v.Reason, "apt-get install -y postgresql") || !strings.Contains(v.Reason, "Unable to locate package") {
		t.Errorf("reason = %q, want the failed command and the log tail", v.Reason)
	}
	if v.FailedIn != StateInstalling {
		t.Errorf("failed in %q, want %q", v.FailedIn, StateInstalling)
	}
	evs := h.waitEvents(t, 4)
	if last := evs[len(evs)-1]; len(evs) != 4 || last.Kind != events.KindServiceFailed {
		t.Errorf("events = %+v, want three steps then service.failed", evs)
	}
}

func TestCreate_FailsWhenCloudInitNeverRanTheScript(t *testing.T) {
	h := newHarness(t)
	h.guest.set("cloud-init status: done")
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres"}); err != nil {
		t.Fatal(err)
	}
	v := h.waitState(t, "db", StateFailed)
	if !strings.Contains(v.Reason, "without running the install script") {
		t.Errorf("reason = %q", v.Reason)
	}
}

func TestCreate_WaitsForThePortsBeforeReady(t *testing.T) {
	h := newHarness(t)
	var mu sync.Mutex
	open := false
	h.m.dial = func(context.Context, string) error {
		mu.Lock()
		defer mu.Unlock()
		if !open {
			return errors.New("connection refused")
		}
		return nil
	}
	h.guest.set("ready")
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "k8s", Recipe: "kubernetes"}); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, "k8s", StateInstalling)
	time.Sleep(20 * time.Millisecond)
	if v, _ := h.m.Get(context.Background(), "k8s"); v.State != StateInstalling {
		t.Fatalf("state = %s with the API port closed, want installing", v.State)
	}
	mu.Lock()
	open = true
	mu.Unlock()
	h.waitState(t, "k8s", StateReady)
}

func TestCreate_FailsOnAChangedHostKey(t *testing.T) {
	h := newHarness(t)
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres"}); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, "db", StateInstalling)
	// Recorded on first contact; then the VM answers with another key.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		if inst, _ := h.store.Get("db"); inst.hostKey("db") != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("host key never recorded")
		}
	}
	h.guest.mu.Lock()
	h.guest.hostKey = "ssh-ed25519 IMPOSTOR"
	h.guest.mu.Unlock()
	v := h.waitState(t, "db", StateFailed)
	if !strings.Contains(v.Reason, "host key") {
		t.Errorf("reason = %q", v.Reason)
	}
}

func TestCreate_FailsWhenTheImageCannotBeDownloaded(t *testing.T) {
	h := newHarness(t)
	h.api.with(func() { h.api.pullErr = "checksum mismatch" })
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres"}); err != nil {
		t.Fatal(err)
	}
	v := h.waitState(t, "db", StateFailed)
	if !strings.Contains(v.Reason, "checksum mismatch") {
		t.Errorf("reason = %q", v.Reason)
	}
}

func TestCreate_FailsWhenTheVMCannotBeCreated(t *testing.T) {
	h := newHarness(t)
	h.api.with(func() { h.api.createOK = false })
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres"}); err != nil {
		t.Fatal(err)
	}
	v := h.waitState(t, "db", StateFailed)
	if !strings.Contains(v.Reason, "xorriso") {
		t.Errorf("reason = %q, want the create error", v.Reason)
	}
}

func TestCreate_GivesUpOnAVMWithNoNetwork(t *testing.T) {
	h := newHarness(t)
	h.m.noNetworkAfter = time.Minute
	h.api.with(func() { h.api.noBoot = true })
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres"}); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, "db", StateWaitingIP)
	h.api.with(func() {
		*h.api.vms["db"] = vmView{ID: "db", Status: "running", Phase: "no-network", Uptime: 30}
	})
	time.Sleep(20 * time.Millisecond)
	if v, _ := h.m.Get(context.Background(), "db"); v.State != StateWaitingIP {
		t.Fatalf("gave up after 30s: %s", v.State)
	}
	h.api.with(func() { h.api.vms["db"].Uptime = 120 })
	v := h.waitState(t, "db", StateFailed)
	if !strings.Contains(v.Reason, "no network address") {
		t.Errorf("reason = %q", v.Reason)
	}
}

// A hypervisor that is briefly unreachable is waited out, not a failure.
func TestCreate_WaitsOutAnUnreachableHypervisor(t *testing.T) {
	h := newHarness(t)
	h.guest.set("ready")
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres"}); err != nil {
		t.Fatal(err)
	}
	h.api.with(func() { h.api.fail503 = 5 })
	h.waitState(t, "db", StateReady)
}

func TestCreate_Validation(t *testing.T) {
	h := newHarness(t)
	h.api.with(func() { h.api.vms["web"] = &vmView{ID: "web", Status: "running"} })
	if err := h.store.Create(Instance{Name: "taken", State: StateFailed}); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		req  CreateRequest
		want error
	}{
		"bad name":          {CreateRequest{Name: "My_DB", Recipe: "postgres"}, ErrInvalid},
		"reserved name":     {CreateRequest{Name: "catalog", Recipe: "postgres"}, ErrInvalid},
		"unknown recipe":    {CreateRequest{Name: "x", Recipe: "redis"}, ErrInvalid},
		"unknown size":      {CreateRequest{Name: "x", Recipe: "postgres", Size: "huge"}, ErrInvalid},
		"unknown setting":   {CreateRequest{Name: "x", Recipe: "postgres", Params: map[string]string{"port": "1"}}, ErrInvalid},
		"generated setting": {CreateRequest{Name: "x", Recipe: "postgres", Params: map[string]string{"password": "hunter2"}}, ErrInvalid},
		"bad setting":       {CreateRequest{Name: "x", Recipe: "postgres", Params: map[string]string{"user": "Robert'); DROP"}}, ErrInvalid},
		"bad ssh key":       {CreateRequest{Name: "x", Recipe: "postgres", SSHPublicKey: "not a key"}, ErrInvalid},
		"VM name taken":     {CreateRequest{Name: "web", Recipe: "postgres"}, ErrExists},
		"service taken":     {CreateRequest{Name: "taken", Recipe: "postgres"}, ErrExists},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := h.m.Create(context.Background(), tc.req); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	h.api.with(func() {
		if len(h.api.created) != 0 {
			t.Errorf("an invalid request created a VM: %+v", h.api.created)
		}
	})
}

// A daemon restart mid-build resumes the service from the step it was on.
func TestRun_ResumesAHalfBuiltService(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "services.json"))
	inst, err := newInstance(CreateRequest{Name: "db", Recipe: "postgres"})
	if err != nil {
		t.Fatal(err)
	}
	inst.State, inst.ImageID = StateInstalling, "ubuntu-2404-standard"
	if err := store.Create(inst); err != nil {
		t.Fatal(err)
	}

	h := newHarnessWith(t, store, func(h *harness) {
		h.api.vms["db"] = &vmView{ID: "db", Status: "running", Phase: "ready", IP: "10.0.0.9"}
		h.guest.state = "ready"
	})
	h.waitState(t, "db", StateReady)
	h.api.with(func() {
		if len(h.api.created) != 0 {
			t.Errorf("resume created another VM: %+v", h.api.created)
		}
	})
}

func TestRun_ResumedCreateDoesNotMakeASecondVM(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "services.json"))
	inst, _ := newInstance(CreateRequest{Name: "db", Recipe: "postgres"})
	inst.State, inst.ImageID = StateCreating, "ubuntu-2404-standard"
	if err := store.Create(inst); err != nil {
		t.Fatal(err)
	}
	// The VM exists before the manager resumes, so it must not create another.
	h := newHarnessWith(t, store, func(h *harness) {
		h.api.vms["db"] = &vmView{ID: "db", Status: "running", Phase: "booting"}
		h.api.booting["db"] = true
	})
	h.waitState(t, "db", StateInstalling)
	h.api.with(func() {
		if len(h.api.created) != 0 {
			t.Errorf("the VM made before the restart was made again")
		}
	})
}

func TestDelete_StopsTheWorkerAndDeletesTheVM(t *testing.T) {
	h := newHarness(t)
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres"}); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, "db", StateInstalling)
	if err := h.m.Delete(context.Background(), "db"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Get(context.Background(), "db"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	h.api.with(func() {
		if len(h.api.deleted) != 1 || h.api.deleted[0] != "db" {
			t.Errorf("deleted VMs = %v", h.api.deleted)
		}
	})
	if err := h.m.Delete(context.Background(), "db"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestView_ShowsTheVMsStateOnceReady(t *testing.T) {
	h := newHarness(t)
	h.guest.set("ready")
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres"}); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, "db", StateReady)

	if err := h.m.Stop(context.Background(), "db"); err != nil {
		t.Fatal(err)
	}
	v, _ := h.m.Get(context.Background(), "db")
	if v.State != StateStopped || len(v.Outputs) != 0 {
		t.Errorf("stopped view: state %s, %d outputs", v.State, len(v.Outputs))
	}
	if err := h.m.Start(context.Background(), "db"); err != nil {
		t.Fatal(err)
	}
	if started, _ := h.m.Get(context.Background(), "db"); started.State != StateStarting {
		t.Errorf("just started: %s, want starting", started.State)
	}
	h.waitState(t, "db", StateReady)

	h.api.with(func() { delete(h.api.vms, "db") })
	v, _ = h.m.Get(context.Background(), "db")
	if v.State != StateMissing || !strings.Contains(v.Reason, "no longer exists") {
		t.Errorf("VM gone: %s (%s)", v.State, v.Reason)
	}
}

func TestFile_RewritesTheKubeconfigForTheHost(t *testing.T) {
	h := newHarness(t)
	h.guest.set("ready")
	h.guest.files = map[string]string{
		"/etc/rancher/k3s/k3s.yaml": "clusters:\n- cluster:\n    server: https://127.0.0.1:6443\n",
	}
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "k8s", Recipe: "kubernetes"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.m.File(context.Background(), "k8s", "kubeconfig"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("file before ready: %v, want ErrNotReady", err)
	}
	h.waitState(t, "k8s", StateReady)

	name, data, err := h.m.File(context.Background(), "k8s", "kubeconfig")
	if err != nil {
		t.Fatal(err)
	}
	if name != "k8s.yaml" || !strings.Contains(string(data), "    server: https://localhost:30443\n    tls-server-name: kubernetes\n") {
		t.Errorf("file %s:\n%s", name, data)
	}
	if _, _, err := h.m.File(context.Background(), "k8s", "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown file: %v", err)
	}
}

func TestCreate_AuthorisesTheUsersOwnKey(t *testing.T) {
	h := newHarness(t)
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "db", Recipe: "postgres", SSHPublicKey: testUserKey + "\n", GitHubUser: "octocat"}); err != nil {
		t.Fatal(err)
	}
	h.waitState(t, "db", StateInstalling)
	h.api.with(func() {
		ci := h.api.created[0].CloudInit
		if ci.GitHubUser != "octocat" {
			t.Errorf("github user = %q", ci.GitHubUser)
		}
		if !strings.Contains(ci.WriteFiles[0].Content, testUserKey) {
			t.Error("the user's key is not authorised by the install script")
		}
	})
}

func TestRandomString(t *testing.T) {
	s, err := randomString(40, charsetUpper)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 40 || strings.ToUpper(s) != s {
		t.Errorf("upper = %q", s)
	}
	a, _ := randomString(24, charsetAlnum)
	b, _ := randomString(24, charsetAlnum)
	if a == b {
		t.Error("two generated secrets are the same")
	}
}

// A cluster: the workers wait until the control plane is ready, are handed
// its address, and the service is ready only once every node has joined.
func TestCreate_BuildsAClusterInOrder(t *testing.T) {
	h := newHarness(t)
	h.guest.mu.Lock()
	h.guest.checkFail = true
	h.guest.mu.Unlock()
	h.guest.setFor("10.0.0.5", "installing")
	h.guest.setFor("10.0.0.6", "waiting")
	h.guest.setFor("10.0.0.7", "waiting")

	v, err := h.m.Create(context.Background(), CreateRequest{Name: "lab", Recipe: "kubernetes_cluster"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(v.VMs, ","); got != "lab-server,lab-agent-1,lab-agent-2" {
		t.Fatalf("VMs = %s", got)
	}
	h.waitState(t, "lab", StateInstalling)

	h.api.with(func() {
		if len(h.api.created) != 3 {
			t.Fatalf("created %d VMs, want 3", len(h.api.created))
		}
		server, agent := h.api.created[0].CloudInit.WriteFiles[0].Content, h.api.created[1].CloudInit.WriteFiles[0].Content
		if !strings.Contains(server, "sh -s - server") || strings.Contains(server, "echo waiting") {
			t.Errorf("control-plane script:\n%s", server)
		}
		if !strings.Contains(agent, "sh -s - agent") || !strings.Contains(agent, "until [ -s "+envFile) {
			t.Errorf("worker script does not wait for its env:\n%s", agent)
		}
		for _, c := range h.api.created {
			if c.Service != "lab" || c.VCPUs != 2 || c.MemoryMB != 4096 {
				t.Errorf("VM %s: service %q, size %d/%d", c.Name, c.Service, c.VCPUs, c.MemoryMB)
			}
		}
	})

	// Nothing is handed over before the control plane is ready.
	time.Sleep(20 * time.Millisecond)
	h.guest.mu.Lock()
	handed := len(h.guest.env)
	h.guest.mu.Unlock()
	if handed != 0 {
		t.Fatalf("workers got their env before the control plane was ready")
	}

	h.guest.setFor("10.0.0.5", "ready")
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.guest.mu.Lock()
		handed = len(h.guest.env)
		env := h.guest.env["10.0.0.6"]
		h.guest.mu.Unlock()
		if handed == 2 {
			if env != "K3S_URL='https://10.0.0.5:6443'\n" {
				t.Errorf("worker env = %q", env)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("handed over to %d workers, want 2", handed)
		}
		time.Sleep(time.Millisecond)
	}

	// Workers installed, but the ready check says not every node has joined.
	h.guest.setFor("10.0.0.6", "ready")
	h.guest.setFor("10.0.0.7", "ready")
	time.Sleep(20 * time.Millisecond)
	if got, _ := h.m.Get(context.Background(), "lab"); got.State != StateInstalling {
		t.Fatalf("state = %s before the ready check passed, want installing", got.State)
	}
	h.guest.mu.Lock()
	h.guest.checkFail = false
	var check string
	for _, c := range h.guest.cmds {
		if strings.HasPrefix(c, "test ") {
			check = c
		}
	}
	h.guest.mu.Unlock()
	if !strings.HasSuffix(check, "-ge 3") {
		t.Errorf("ready check = %q, want it to count 3 nodes", check)
	}

	v = h.waitState(t, "lab", StateReady)
	if v.IP != "10.0.0.5" || len(v.Members) != 3 || v.Members[1].RoleLabel != "Worker" || v.Members[2].IP != "10.0.0.7" {
		t.Errorf("view = ip %s, members %+v", v.IP, v.Members)
	}
	inst, _ := h.store.Get("lab")
	if len(inst.HostKeys) != 3 {
		t.Errorf("host keys recorded for %d VMs, want 3", len(inst.HostKeys))
	}
}

func TestCreate_NamesTheWorkerWhoseInstallFailed(t *testing.T) {
	h := newHarness(t)
	h.guest.setFor("10.0.0.5", "ready")
	h.guest.setFor("10.0.0.6", "failed: curl -sfL https://get.k3s.io (line 14)")
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "lab", Recipe: "kubernetes_cluster", Params: map[string]string{"workers": "1"}}); err != nil {
		t.Fatal(err)
	}
	v := h.waitState(t, "lab", StateFailed)
	if !strings.HasPrefix(v.Reason, "On lab-agent, the install failed at curl") {
		t.Errorf("reason = %q", v.Reason)
	}
}

// An install that stops itself with fail() is reported in its own words,
// followed once by the end of its log.
func TestCreate_ReportsAnInstallsOwnReason(t *testing.T) {
	h := newHarness(t)
	h.guest.setFor("10.0.0.5", "ready")
	why := "this worker cannot reach the control plane at https://10.0.0.5:6443: traffic between the VMs does not get through"
	h.guest.setFor("10.0.0.6", "failed because: "+why)
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "lab", Recipe: "kubernetes_cluster", Params: map[string]string{"workers": "1"}}); err != nil {
		t.Fatal(err)
	}
	v := h.waitState(t, "lab", StateFailed)
	if !strings.HasPrefix(v.Reason, "On lab-agent, "+why+"\n") || strings.Count(v.Reason, why) != 1 {
		t.Errorf("reason = %q", v.Reason)
	}
	if v.FailedIn != StateInstalling {
		t.Errorf("failed in %q, want installing so Retry runs the install again", v.FailedIn)
	}
}

// Workers that fail together are each reported, and the console keeps every
// VM's last state until a retry.
func TestCreate_ReportsEveryWorkerWhoseInstallFailed(t *testing.T) {
	h := newHarness(t)
	h.guest.setFor("10.0.0.5", "ready")
	why := "this worker cannot reach the control plane at https://10.0.0.5:6443: traffic between the VMs does not get through"
	h.guest.setFor("10.0.0.6", "failed because: "+why)
	h.guest.setFor("10.0.0.7", "failed because: "+why)
	if _, err := h.m.Create(context.Background(), CreateRequest{Name: "lab", Recipe: "kubernetes_cluster"}); err != nil {
		t.Fatal(err)
	}
	v := h.waitState(t, "lab", StateFailed)
	want := "On lab-agent-1, " + why + "\nE: Unable to locate package postgresql\n\nlab-agent-2 failed the same way."
	if v.Reason != want {
		t.Errorf("reason = %q\nwant %q", v.Reason, want)
	}
	got := map[string]string{}
	for _, mem := range v.Members {
		got[mem.VM] = mem.Install + ": " + mem.Failure
	}
	if got["lab-server"] != "ready: " || got["lab-agent-1"] != "failed: "+why || got["lab-agent-2"] != "failed: "+why {
		t.Errorf("members after the failure = %v", got)
	}

	v, err := h.m.Retry(context.Background(), "lab")
	if err != nil {
		t.Fatal(err)
	}
	for _, mem := range v.Members {
		if mem.Install == "failed" {
			t.Errorf("%s still reads failed after a retry", mem.VM)
		}
	}
}

// Workers that fail for different reasons are each named with their own.
func TestInstallsFailed_GivesEachDifferentReason(t *testing.T) {
	err := installsFailed([]*installFailure{
		{vm: "lab-agent-1", reason: "k3s did not start", many: true},
		{vm: "lab-agent-2", reason: "the install failed at apt-get update (line 4)\nE: no network", many: true},
	})
	want := "on lab-agent-1, k3s did not start.\nOn lab-agent-2, the install failed at apt-get update (line 4)."
	if err.Error() != want {
		t.Errorf("reason = %q\nwant %q", err, want)
	}
}

func TestPlanMembers(t *testing.T) {
	cluster, _ := RecipeByID("kubernetes_cluster")
	single, _ := RecipeByID("postgres")
	for _, tc := range []struct {
		recipe  Recipe
		workers string
		want    string
	}{
		{single, "", "main:db"},
		{cluster, "1", "server:db-server,agent:db-agent"},
		{cluster, "3", "server:db-server,agent:db-agent-1,agent:db-agent-2,agent:db-agent-3"},
	} {
		members, err := planMembers("db", tc.recipe, map[string]string{"workers": tc.workers})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, m := range members {
			got = append(got, m.Role+":"+m.VM)
		}
		if strings.Join(got, ",") != tc.want {
			t.Errorf("%s with %q workers = %v, want %s", tc.recipe.ID, tc.workers, got, tc.want)
		}
	}
	if _, err := newInstance(CreateRequest{Name: "db", Recipe: "kubernetes_cluster", Params: map[string]string{"workers": "9"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("9 workers: %v, want ErrInvalid", err)
	}
}

// A service without Members: its one VM plays the only role, and its legacy
// host key still counts.
func TestInstance_ReadsAnEntryFromBeforeRoles(t *testing.T) {
	inst := Instance{Name: "db", VMs: []string{"db"}, HostKey: "ssh-ed25519 OLD"}
	if m := inst.members(); len(m) != 1 || m[0].Role != mainRole || m[0].VM != "db" {
		t.Errorf("members = %+v", m)
	}
	if inst.hostKey("db") != "ssh-ed25519 OLD" {
		t.Errorf("host key = %q", inst.hostKey("db"))
	}
}
