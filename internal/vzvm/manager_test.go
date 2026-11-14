package vzvm

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalocean/go-libvirt"

	"github.com/nanocloud-io/nanocloud/internal/cloudinit"
	"github.com/nanocloud-io/nanocloud/internal/hypervisor"
	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/network"
	"github.com/nanocloud-io/nanocloud/internal/storage"
	"github.com/nanocloud-io/nanocloud/internal/vm"
)

// These tests run the real helper code (as goroutines, via InProcess) and
// the real daemon-side Manager, talking over real unix sockets, against a
// fake Machine standing in for Virtualization.framework.

type fakeMachine struct {
	spec MachineSpec

	mu         sync.Mutex
	state      MachineState
	ch         chan MachineState
	startErr   error
	ignoreStop bool // a guest that does not answer the power button
	requests   int
	pauses     int
}

func (f *fakeMachine) set(s MachineState) {
	f.mu.Lock()
	f.state = s
	f.mu.Unlock()
	f.ch <- s
}

func (f *fakeMachine) Start() error {
	if f.startErr != nil {
		f.set(MachineError)
		return f.startErr
	}
	f.set(MachineStarting)
	f.set(MachineRunning)
	return nil
}

func (f *fakeMachine) RequestStop() error {
	f.mu.Lock()
	f.requests++
	ignore := f.ignoreStop
	f.mu.Unlock()
	if !ignore {
		go func() { f.set(MachineStopping); f.set(MachineStopped) }()
	}
	return nil
}

func (f *fakeMachine) Stop() error { go f.set(MachineStopped); return nil }

func (f *fakeMachine) State() MachineState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *fakeMachine) StateChanges() <-chan MachineState { return f.ch }

func (f *fakeMachine) Pause() error {
	f.mu.Lock()
	f.pauses++
	f.mu.Unlock()
	f.set(MachinePaused)
	return nil
}

func (f *fakeMachine) Resume() error { f.set(MachineRunning); return nil }

func (f *fakeMachine) counts() (requests, pauses int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests, f.pauses
}

type harness struct {
	t     *testing.T
	m     *Manager
	dir   string
	run   string
	procs *InProcess

	mu       sync.Mutex
	machines []*fakeMachine
	startErr error
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	// Unix socket paths are limited to ~104 bytes; t.TempDir() can exceed it.
	run, err := os.MkdirTemp("", "vz")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, dir: t.TempDir(), run: run}
	h.procs = &InProcess{Launch: h.launch}
	h.m = h.newManager()
	t.Cleanup(func() {
		_ = h.m.Close()
		h.procs.Close()
		_ = os.RemoveAll(run)
	})
	return h
}

func (h *harness) launch(s MachineSpec) (Machine, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f := &fakeMachine{spec: s, ch: make(chan MachineState, 64), startErr: h.startErr}
	h.machines = append(h.machines, f)
	return f, nil
}

// newManager is a daemon, as after a restart: same run directory, same helpers.
func (h *harness) newManager() *Manager {
	return New(Config{Spawn: h.procs.Spawn, RunDir: h.run})
}

func (h *harness) machineCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.machines)
}

func (h *harness) last() *fakeMachine {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.machines[len(h.machines)-1]
}

// entry defines a VM whose disk exists, and returns its spec.
func (h *harness) define(id string, mutate ...func(*metadata.VMEntry)) metadata.VMEntry {
	h.t.Helper()
	e := metadata.VMEntry{ID: id, PoolPath: h.dir, VCPUs: 2, MemoryMB: 1024}
	for _, f := range mutate {
		f(&e)
	}
	disk := filepath.Join(h.dir, id+".raw")
	iso := filepath.Join(h.dir, id+"-cidata.iso")
	for _, p := range []string{disk, iso} {
		if _, err := os.Stat(p); err == nil {
			continue
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := h.m.DefineFromSpec(e, disk, iso); err != nil {
		h.t.Fatalf("DefineFromSpec(%s): %v", id, err)
	}
	return e
}

func observe(t *testing.T, m *Manager, id string) vm.ObservedDomain {
	t.Helper()
	obs, err := m.Observe()
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range obs {
		if o.Name == id {
			return o
		}
	}
	t.Fatalf("%s not observed", id)
	return vm.ObservedDomain{}
}

// eventually polls cond for up to five seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitStopped waits for a VM to be reported stopped and returns the reason.
func waitStopped(t *testing.T, m *Manager, id string) libvirt.DomainShutoffReason {
	t.Helper()
	var o vm.ObservedDomain
	eventually(t, id+" to stop", func() bool {
		o = observe(t, m, id)
		return o.State == libvirt.DomainShutoff
	})
	return libvirt.DomainShutoffReason(o.Reason)
}

func waitRunning(t *testing.T, m *Manager, id string) {
	t.Helper()
	eventually(t, id+" to run", func() bool { return observe(t, m, id).State == libvirt.DomainRunning })
}

func TestDefinedVMObservesAsStopped(t *testing.T) {
	h := newHarness(t)
	h.define("web")
	o := observe(t, h.m, "web")
	// A fresh boot of the Mac leaves every VM like this; on-failure restarts it.
	if o.State != libvirt.DomainShutoff || libvirt.DomainShutoffReason(o.Reason) != libvirt.DomainShutoffUnknown {
		t.Fatalf("observed %+v, want shutoff/unknown", o)
	}
	v, err := h.m.Get("web")
	if err != nil || v.State != "stopped" || v.ID != -1 || v.VCPUs != 2 {
		t.Fatalf("Get = %+v, %v", v, err)
	}
}

func TestDefineRefusesWhatVZCannotBuild(t *testing.T) {
	h := newHarness(t)
	if err := h.m.DefineFromSpec(metadata.VMEntry{ID: "x"}, "d", "i"); !errors.Is(err, vm.ErrIncompleteSpec) {
		t.Errorf("incomplete spec: %v, want ErrIncompleteSpec", err)
	}
	for name, mut := range map[string]func(*metadata.VMEntry){
		"bridge":   func(e *metadata.VMEntry) { e.NetworkMode = metadata.NetworkBridge },
		"tpm":      func(e *metadata.VMEntry) { e.TPM = true },
		"cpuset":   func(e *metadata.VMEntry) { e.CPUSet = "0-1" },
		"cpu mode": func(e *metadata.VMEntry) { e.CPUMode = metadata.CPUHostPassthrough },
		"pci":      func(e *metadata.VMEntry) { e.PCIDevices = []metadata.PCIDevice{{Address: "0000:01:00.0"}} },
	} {
		e := metadata.VMEntry{ID: name, VCPUs: 1, MemoryMB: 512}
		mut(&e)
		if err := h.m.DefineFromSpec(e, "d", "i"); !errors.Is(err, hypervisor.ErrUnsupported) {
			t.Errorf("%s: %v, want ErrUnsupported", name, err)
		}
	}
}

func TestStartBootsAHelperWithTheSpec(t *testing.T) {
	h := newHarness(t)
	h.define("web", func(e *metadata.VMEntry) {
		e.Disks = []metadata.Disk{{Target: "vdb", Path: filepath.Join(h.dir, "web-vdb.raw")}}
	})
	if err := h.m.Start("web"); err != nil {
		t.Fatal(err)
	}
	spec := h.last().spec
	if spec.CPUs != 2 || spec.MemoryBytes != 1024<<20 || spec.SerialIn == nil || spec.SerialOut == nil {
		t.Errorf("spec = %+v", spec)
	}
	want := []Disk{
		{Path: filepath.Join(h.dir, "web.raw")},
		{Path: filepath.Join(h.dir, "web-vdb.raw")},
		{Path: filepath.Join(h.dir, "web-cidata.iso"), ReadOnly: true},
	}
	if len(spec.Disks) != len(want) {
		t.Fatalf("disks = %+v, want %+v", spec.Disks, want)
	}
	for i := range want {
		if spec.Disks[i] != want[i] {
			t.Errorf("disk %d = %+v, want %+v", i, spec.Disks[i], want[i])
		}
	}
	id, _ := h.m.Identity("web")
	hw, err := spec.HardwareAddr()
	if err != nil || spec.MAC != id.MAC || hw[0]&0x02 == 0 || hw[0]&0x01 != 0 {
		t.Errorf("MAC %s (identity %s): want the recorded, locally administered unicast address", spec.MAC, id.MAC)
	}
	if spec.NVRAMPath != filepath.Join(h.dir, "web.nvram") || spec.MachineIDPath != filepath.Join(h.dir, "web.machineid") {
		t.Errorf("aux paths %s %s", spec.NVRAMPath, spec.MachineIDPath)
	}
	waitRunning(t, h.m, "web")
	if v, _ := h.m.Get("web"); v.ID <= 0 || v.State != "running" {
		t.Errorf("Get = %+v, want a running VM with an id", v)
	}
	if err := h.m.Start("web"); err == nil {
		t.Error("second Start succeeded")
	}
}

func TestStopReasons(t *testing.T) {
	cases := []struct {
		name string
		stop func(h *harness, f *fakeMachine)
		want libvirt.DomainShutoffReason
	}{
		{"graceful stop", func(h *harness, _ *fakeMachine) {
			if err := h.m.Stop("web"); err != nil {
				t.Fatal(err)
			}
		}, libvirt.DomainShutoffShutdown},
		{"guest poweroff", func(_ *harness, f *fakeMachine) { f.set(MachineStopped) }, libvirt.DomainShutoffShutdown},
		{"framework error", func(_ *harness, f *fakeMachine) { f.set(MachineError) }, libvirt.DomainShutoffCrashed},
		{"destroy", func(h *harness, _ *fakeMachine) {
			if err := h.m.Destroy("web"); err != nil {
				t.Fatal(err)
			}
		}, libvirt.DomainShutoffDestroyed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.define("web")
			if err := h.m.Start("web"); err != nil {
				t.Fatal(err)
			}
			waitRunning(t, h.m, "web")
			c.stop(h, h.last())
			if got := waitStopped(t, h.m, "web"); got != c.want {
				t.Fatalf("reason = %v, want %v", got, c.want)
			}
			// It can be booted again, by a fresh helper.
			if err := h.m.Start("web"); err != nil {
				t.Fatalf("restart: %v", err)
			}
			waitRunning(t, h.m, "web")
		})
	}
}

func TestStopAndDestroyOfAStoppedVM(t *testing.T) {
	h := newHarness(t)
	h.define("web")
	if err := h.m.Stop("web"); !errors.Is(err, vm.ErrNotRunning) {
		t.Errorf("Stop: %v, want ErrNotRunning", err)
	}
	if err := h.m.Destroy("web"); !errors.Is(err, vm.ErrNotRunning) {
		t.Errorf("Destroy: %v, want ErrNotRunning", err)
	}
}

func TestDestroyWaitsUntilStopped(t *testing.T) {
	h := newHarness(t)
	h.define("web")
	if err := h.m.Start("web"); err != nil {
		t.Fatal(err)
	}
	if err := h.m.Destroy("web"); err != nil {
		t.Fatal(err)
	}
	// No waiting: Destroy returned, so the stop is already recorded.
	if o := observe(t, h.m, "web"); o.State != libvirt.DomainShutoff {
		t.Fatalf("observed %+v right after Destroy, want shutoff", o)
	}
}

func TestFailedStartAndFailedCreate(t *testing.T) {
	h := newHarness(t)
	h.startErr = errors.New("VZErrorDomain: invalid configuration")
	h.define("web")
	if err := h.m.Start("web"); err == nil || !strings.Contains(err.Error(), "invalid configuration") {
		t.Fatalf("Start = %v, want the framework's error", err)
	}
	if o := observe(t, h.m, "web"); libvirt.DomainShutoffReason(o.Reason) != libvirt.DomainShutoffFailed {
		t.Errorf("reason = %v, want failed", o.Reason)
	}
	if st, ok := readStatus(filepath.Join(h.run, "web.status")); !ok || st.Reason != reasonFailed {
		t.Errorf("status file = %+v, %v; want failed", st, ok)
	}

	disk := filepath.Join(h.dir, "new.raw")
	if err := os.WriteFile(disk, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.m.Create(vm.VMCreateParams{Name: "new", DiskPath: disk, VCPUs: 1, MemoryMB: 512}); err == nil {
		t.Fatal("Create succeeded with a machine that will not start")
	}
	if ok, _ := h.m.Exists("new"); ok {
		t.Error("a failed Create left its VM defined")
	}
}

func TestStartWithoutDisk(t *testing.T) {
	h := newHarness(t)
	e := metadata.VMEntry{ID: "web", VCPUs: 1, MemoryMB: 512}
	if err := h.m.DefineFromSpec(e, filepath.Join(h.dir, "missing.raw"), ""); err != nil {
		t.Fatal(err)
	}
	if err := h.m.Start("web"); err == nil || !strings.Contains(err.Error(), "boot disk") {
		t.Fatalf("Start = %v, want a boot disk error", err)
	}
	if h.machineCount() != 0 {
		t.Error("a helper was started for a VM with no disk")
	}
}

func TestDeleteStopsThenForgets(t *testing.T) {
	h := newHarness(t)
	h.define("web")
	for _, aux := range []string{"web.nvram", "web.machineid"} {
		if err := os.WriteFile(filepath.Join(h.dir, aux), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.m.Start("web"); err != nil {
		t.Fatal(err)
	}
	if err := h.m.CreateSnapshot("web", "s1", ""); err != nil {
		t.Fatal(err)
	}
	if err := h.m.Delete("web"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := h.m.Exists("web"); ok {
		t.Error("still defined")
	}
	for _, aux := range []string{"web.nvram", "web.machineid", "web.raw.snapshots"} {
		if _, err := os.Stat(filepath.Join(h.dir, aux)); !os.IsNotExist(err) {
			t.Errorf("%s survived Delete", aux)
		}
	}
	if _, err := os.Stat(filepath.Join(h.dir, "web.raw")); err != nil {
		t.Error("Delete removed the disk, which belongs to the caller")
	}
	if err := h.m.Delete("web"); err != nil {
		t.Errorf("deleting an unknown VM: %v, want success", err)
	}
}

func TestIdentityIsStableAndRedefineKeepsIt(t *testing.T) {
	h := newHarness(t)
	e := h.define("web", func(e *metadata.VMEntry) { e.MAC = "52:54:0:AB:c:ef" })
	id, _ := h.m.Identity("web")
	if id.MAC != "52:54:00:ab:0c:ef" || id.UUID == "" {
		t.Fatalf("identity = %+v", id)
	}
	e.MAC = "52:54:00:00:00:01"
	if err := h.m.RedefineFromSpec(e, filepath.Join(h.dir, "web.raw"), ""); err == nil {
		t.Error("RedefineFromSpec changed the MAC")
	}
	e.MAC = ""
	e.VCPUs = 4
	if err := h.m.RedefineFromSpec(e, filepath.Join(h.dir, "web.raw"), ""); err != nil {
		t.Fatal(err)
	}
	if again, _ := h.m.Identity("web"); again != id {
		t.Errorf("identity changed across redefine: %+v -> %+v", id, again)
	}
}

func TestActiveHardwareIsTheRunningMachine(t *testing.T) {
	h := newHarness(t)
	e := h.define("web")
	if err := h.m.Start("web"); err != nil {
		t.Fatal(err)
	}
	e.Disks = []metadata.Disk{{Target: "vdb", Path: filepath.Join(h.dir, "web-vdb.raw")}}
	if err := h.m.RedefineFromSpec(e, filepath.Join(h.dir, "web.raw"), ""); err != nil {
		t.Fatal(err)
	}
	live, _ := h.m.ActiveHardware("web")
	if len(live.DiskTargets) != 1 {
		t.Fatalf("running machine reports disks %v, want only the boot disk until restart", live.DiskTargets)
	}
	if err := h.m.Destroy("web"); err != nil {
		t.Fatal(err)
	}
	stopped, _ := h.m.ActiveHardware("web")
	if len(stopped.DiskTargets) != 2 {
		t.Fatalf("stopped VM reports disks %v, want the new definition", stopped.DiskTargets)
	}
}

type staticLeases []network.DHCPLease

func (s staticLeases) Read() ([]network.DHCPLease, error) { return s, nil }

// fakeSSH stands in for probing guests' SSH ports: answering says whether
// they answer, and probed counts the probes.
type fakeSSH struct {
	answering, probed atomic.Int32
}

func useFakeSSH(t *testing.T) *fakeSSH {
	t.Helper()
	f := &fakeSSH{}
	savedProbe, savedInterval := probeSSH, sshProbeInterval
	probeSSH = func(string) bool { f.probed.Add(1); return f.answering.Load() == 1 }
	sshProbeInterval = 10 * time.Millisecond
	t.Cleanup(func() { probeSSH, sshProbeInterval = savedProbe, savedInterval })
	return f
}

func readyMarker(ip string) string {
	return cloudinit.OSCPrefix + cloudinit.ReadyCommand + ";ip=" + ip + ";ci=done\x07"
}

// A guest that does not report its address is found in the leases and
// confirmed once it answers SSH.
func TestPrimaryIPLeaseIsConfirmedBySSH(t *testing.T) {
	h := newHarness(t)
	ssh := useFakeSSH(t)
	h.define("web", func(e *metadata.VMEntry) { e.MAC = "52:54:00:ab:0c:ef" })
	h.m.cfg.Leases = staticLeases{
		{IPAddress: "192.168.64.9", MACAddress: "52:54:00:00:00:01"},
		{IPAddress: "192.168.64.3", MACAddress: "52:54:0:ab:c:ef"},
	}

	if ip, _ := h.m.PrimaryIP("web"); ip != "" {
		t.Errorf("stopped VM has IP %q", ip)
	}
	if err := h.m.Start("web"); err != nil {
		t.Fatal(err)
	}
	if ip, confirmed := h.m.PrimaryIP("web"); ip != "192.168.64.3" || confirmed {
		t.Errorf("PrimaryIP = %q, %v; want the lease, unconfirmed", ip, confirmed)
	}
	eventually(t, "a probe", func() bool { return ssh.probed.Load() > 0 })
	if _, confirmed := h.m.PrimaryIP("web"); confirmed {
		t.Error("confirmed although SSH did not answer")
	}

	ssh.answering.Store(1)
	eventually(t, "the lease to be confirmed", func() bool {
		ip, confirmed := h.m.PrimaryIP("web")
		return ip == "192.168.64.3" && confirmed
	})
	probes := ssh.probed.Load()
	time.Sleep(50 * time.Millisecond)
	if _, confirmed := h.m.PrimaryIP("web"); !confirmed || ssh.probed.Load() != probes {
		t.Error("a confirmed address was probed again, or lost its confirmation")
	}
}

// A guest that reports its address on the console is ready at that address at
// once, without leases or probing.
func TestPrimaryIPFromTheGuestsReport(t *testing.T) {
	h := newHarness(t)
	ssh := useFakeSSH(t)
	h.define("web")
	if err := h.m.Start("web"); err != nil {
		t.Fatal(err)
	}
	if ip, _ := h.m.PrimaryIP("web"); ip != "" {
		t.Errorf("IP %q before the guest reported one", ip)
	}
	if _, err := h.last().spec.SerialOut.Write([]byte("Ubuntu 24.04\r\n" + readyMarker("192.168.64.7") + "web login: ")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the reported address", func() bool {
		ip, confirmed := h.m.PrimaryIP("web")
		return ip == "192.168.64.7" && confirmed
	})
	if ssh.probed.Load() != 0 {
		t.Error("a reported address was probed")
	}
}

// The report belongs to one boot: after a guest reboot the VM is not ready
// until the new boot reports, and the daemon learns it across a restart.
func TestGuestReportIsPerBootAndSurvivesDaemonRestart(t *testing.T) {
	h := newHarness(t)
	useFakeSSH(t)
	e := h.define("web")
	if err := h.m.Start("web"); err != nil {
		t.Fatal(err)
	}
	first := h.last()
	if _, err := first.spec.SerialOut.Write([]byte(readyMarker("192.168.64.7"))); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the first report", func() bool { ip, _ := h.m.PrimaryIP("web"); return ip == "192.168.64.7" })

	if _, err := first.spec.SerialOut.Write([]byte(cloudinit.RebootMarker)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	first.set(MachineStopped)
	eventually(t, "a second boot", func() bool { return h.machineCount() == 2 })
	waitRunning(t, h.m, "web")
	if ip, _ := h.m.PrimaryIP("web"); ip != "" {
		t.Errorf("the second boot inherited the first boot's address %q", ip)
	}
	if _, err := h.last().spec.SerialOut.Write([]byte(readyMarker("192.168.64.8"))); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the second report", func() bool { ip, _ := h.m.PrimaryIP("web"); return ip == "192.168.64.8" })

	if err := h.m.Close(); err != nil {
		t.Fatal(err)
	}
	m2 := h.newManager()
	defer m2.Close() //nolint:errcheck // test cleanup
	m2.AdoptSpecs(map[string]metadata.VMEntry{"web": e})
	waitRunning(t, m2, "web")
	if ip, confirmed := m2.PrimaryIP("web"); ip != "192.168.64.8" || !confirmed {
		t.Errorf("after a daemon restart PrimaryIP = %q, %v", ip, confirmed)
	}
}

// readUntil reads from r until it has seen want.
func readUntil(t *testing.T, r io.Reader, want string) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 256)
		for !strings.Contains(b.String(), want) {
			n, err := r.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		got <- b.String()
	}()
	select {
	case s := <-got:
		if !strings.Contains(s, want) {
			t.Fatalf("read %q, want it to contain %q", s, want)
		}
		return s
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out reading %q", want)
		return ""
	}
}

func TestSerialConsoleThroughTheHelper(t *testing.T) {
	h := newHarness(t)
	h.define("web")
	if _, err := h.m.OpenSerial("web"); !errors.Is(err, vm.ErrNotRunning) {
		t.Fatalf("OpenSerial on a stopped VM: %v", err)
	}
	if err := h.m.Start("web"); err != nil {
		t.Fatal(err)
	}
	guest := h.last().spec
	if _, err := guest.SerialOut.Write([]byte("booting...\nweb login: ")); err != nil {
		t.Fatal(err)
	}
	// Give the helper's pump the bytes before the viewer attaches, so they
	// arrive as replay rather than live.
	eventually(t, "output in the replay buffer", func() bool {
		c, err := h.m.OpenSerial("web")
		if err != nil {
			return false
		}
		defer c.Close() //nolint:errcheck // test cleanup
		_ = c.(interface{ SetReadDeadline(time.Time) error }).SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		buf := make([]byte, 64)
		n, _ := c.Read(buf)
		return strings.Contains(string(buf[:n]), "login: ")
	})

	c, err := h.m.OpenSerial("web")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close() //nolint:errcheck // test cleanup
	readUntil(t, c, "login: ")
	if _, err := guest.SerialOut.Write([]byte("Password: ")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "Password: ")
	if _, err := c.Write([]byte("root\n")); err != nil {
		t.Fatal(err)
	}
	in := make([]byte, 5)
	if _, err := io.ReadFull(guest.SerialIn, in); err != nil || string(in) != "root\n" {
		t.Errorf("guest read %q, %v", in, err)
	}

	// When the VM stops, the viewer reads EOF.
	if err := h.m.Destroy("web"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(c); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("read after stop: %v, want a clean EOF", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("console stayed open after the VM stopped")
	}
}

func TestAdoptSpecs(t *testing.T) {
	h := newHarness(t)
	h.m.AdoptSpecs(map[string]metadata.VMEntry{
		"web":    {PoolPath: h.dir, VCPUs: 1, MemoryMB: 512},
		"nopool": {ID: "nopool", VCPUs: 1, MemoryMB: 512},
	})
	if ok, _ := h.m.Exists("web"); !ok {
		t.Error("web not adopted")
	}
	if ok, _ := h.m.Exists("nopool"); ok {
		t.Error("an entry with no pool was adopted")
	}
}

func TestDaemonRestartKeepsVMsRunning(t *testing.T) {
	h := newHarness(t)
	e := h.define("web", func(e *metadata.VMEntry) { e.MAC = "52:54:00:ab:0c:ef" })
	if err := h.m.Start("web"); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, h.m, "web")
	guest := h.last().spec

	// The daemon exits — upgrade, crash, launchctl kickstart.
	if err := h.m.Close(); err != nil {
		t.Fatal(err)
	}
	if h.last().State() != MachineRunning {
		t.Fatal("closing the daemon stopped the VM")
	}

	// A new daemon picks it back up.
	e.VCPUs = 4 // edited while the daemon was down; applies from the next boot
	m2 := h.newManager()
	defer m2.Close() //nolint:errcheck // test cleanup
	m2.AdoptSpecs(map[string]metadata.VMEntry{"web": e})
	waitRunning(t, m2, "web")
	if hw, _ := m2.ActiveHardware("web"); hw.CPU != vm.CPUShapeOf(metadata.VMEntry{VCPUs: 2}) {
		t.Errorf("active hardware %+v, want the running boot's", hw)
	}
	c, err := m2.OpenSerial("web")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guest.SerialOut.Write([]byte("still here")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "still here")
	c.Close() //nolint:errcheck // test cleanup

	if err := m2.Destroy("web"); err != nil {
		t.Fatal(err)
	}
	if got := waitStopped(t, m2, "web"); got != libvirt.DomainShutoffDestroyed {
		t.Errorf("reason = %v, want destroyed", got)
	}
	if h.machineCount() != 1 {
		t.Errorf("%d machines booted, want the one that survived", h.machineCount())
	}
}

func TestVMStoppedWhileTheDaemonWasDown(t *testing.T) {
	h := newHarness(t)
	e := h.define("web")
	if err := h.m.Start("web"); err != nil {
		t.Fatal(err)
	}
	_ = h.m.Close()
	h.last().set(MachineStopped) // the guest powers off with nobody watching
	eventually(t, "the status file", func() bool {
		_, ok := readStatus(filepath.Join(h.run, "web.status"))
		return ok
	})

	m2 := h.newManager()
	defer m2.Close() //nolint:errcheck // test cleanup
	m2.AdoptSpecs(map[string]metadata.VMEntry{"web": e})
	o := observe(t, m2, "web")
	// A clean in-guest poweroff: reconcile honours it rather than restarting.
	if o.State != libvirt.DomainShutoff || libvirt.DomainShutoffReason(o.Reason) != libvirt.DomainShutoffShutdown {
		t.Fatalf("observed %+v, want shutoff/shutdown", o)
	}
}

func TestMacShutdownStopsGuestsAndBringsThemBack(t *testing.T) {
	old := hostStopGrace
	hostStopGrace = 200 * time.Millisecond
	defer func() { hostStopGrace = old }()

	h := newHarness(t)
	h.define("polite")
	h.define("stubborn")
	for _, id := range []string{"polite", "stubborn"} {
		if err := h.m.Start(id); err != nil {
			t.Fatal(err)
		}
	}
	h.mu.Lock()
	polite, stubborn := h.machines[0], h.machines[1]
	h.mu.Unlock()
	stubborn.mu.Lock()
	stubborn.ignoreStop = true
	stubborn.mu.Unlock()

	h.procs.Close() // every helper gets SIGTERM, as when the Mac shuts down

	for _, f := range []*fakeMachine{polite, stubborn} {
		if req, _ := f.counts(); req != 1 {
			t.Errorf("power-button requests = %d, want 1", req)
		}
	}
	for _, id := range []string{"polite", "stubborn"} {
		// Unknown, not "shutdown": the guest did not choose to stop, so
		// on-failure brings it back when the Mac does.
		if got := waitStopped(t, h.m, id); got != libvirt.DomainShutoffUnknown {
			t.Errorf("%s reason = %v, want unknown", id, got)
		}
	}
	if stubborn.State() != MachineStopped {
		t.Error("the stubborn guest was not forced off")
	}
}

func TestGuestRebootBootsTheVMAgain(t *testing.T) {
	h := newHarness(t)
	h.define("web")
	if err := h.m.Start("web"); err != nil {
		t.Fatal(err)
	}
	first := h.last()
	c, err := h.m.OpenSerial("web")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close() //nolint:errcheck // test cleanup

	// The guest's reboot unit prints the marker, then the framework stops it.
	if _, err := first.spec.SerialOut.Write([]byte("Rebooting.\r\n" + cloudinit.RebootMarker)); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, cloudinit.RebootMarker)
	first.set(MachineStopped)

	eventually(t, "a second boot", func() bool { return h.machineCount() == 2 })
	waitRunning(t, h.m, "web")
	second := h.last()
	if second.spec.SerialOut == first.spec.SerialOut {
		t.Error("the second boot reused the first boot's console pipes")
	}
	// The console viewer stayed attached across the reboot.
	if _, err := second.spec.SerialOut.Write([]byte("web login: ")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "web login: ")
	if v, _ := h.m.Get("web"); v.State != "running" {
		t.Errorf("state after reboot = %q", v.State)
	}
}

func TestPoweroffIsNotAReboot(t *testing.T) {
	h := newHarness(t)
	h.define("web")
	if err := h.m.Start("web"); err != nil {
		t.Fatal(err)
	}
	h.last().set(MachineStopped)
	if got := waitStopped(t, h.m, "web"); got != libvirt.DomainShutoffShutdown {
		t.Errorf("reason = %v, want shutdown", got)
	}
	if h.machineCount() != 1 {
		t.Error("a poweroff was treated as a reboot")
	}
}

func TestStopRequestWinsOverARebootMarker(t *testing.T) {
	h := newHarness(t)
	h.define("web")
	if err := h.m.Start("web"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.last().spec.SerialOut.Write([]byte(cloudinit.RebootMarker)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := h.m.Stop("web"); err != nil { // the user pressed Stop
		t.Fatal(err)
	}
	if got := waitStopped(t, h.m, "web"); got != libvirt.DomainShutoffShutdown {
		t.Errorf("reason = %v, want shutdown", got)
	}
	if h.machineCount() != 1 {
		t.Error("a requested stop turned into a reboot")
	}
}

func TestRebootMarkerSplitAcrossReads(t *testing.T) {
	hub := newSerialHub()
	half := len(cloudinit.RebootMarker) / 2
	hub.broadcast([]byte("noise " + cloudinit.RebootMarker[:half]))
	hub.broadcast([]byte(cloudinit.RebootMarker[half:] + " more"))
	if !hub.rebootRequested(time.Minute) {
		t.Fatal("marker split across two reads was missed")
	}
	if hub.rebootRequested(time.Minute) {
		t.Fatal("the announcement was not consumed")
	}
	hub.broadcast([]byte(cloudinit.RebootMarker))
	time.Sleep(20 * time.Millisecond)
	if hub.rebootRequested(time.Millisecond) {
		t.Fatal("a stale announcement counted")
	}
}

func TestCapabilities(t *testing.T) {
	c := New(Config{}).Capabilities()
	if c.Backend != hypervisor.BackendVZ || !c.SerialConsole || !c.Snapshots || c.VNCConsole || c.Bridge || c.VLAN || c.VMStats {
		t.Fatalf("capabilities = %+v", c)
	}
}

func writeAt(t *testing.T, path string, off int64, data string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // test cleanup
	if _, err := f.WriteAt([]byte(data), off); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSnapshotOfAStoppedVM(t *testing.T) {
	h := newHarness(t)
	vdb := filepath.Join(h.dir, "web-vdb.raw")
	if err := os.WriteFile(vdb, []byte("data-v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.define("web", func(e *metadata.VMEntry) { e.Disks = []metadata.Disk{{Target: "vdb", Path: vdb}} })
	boot := filepath.Join(h.dir, "web.raw")
	nvram := filepath.Join(h.dir, "web.nvram")
	if err := os.WriteFile(boot, []byte("boot-v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nvram, []byte("efi-v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := h.m.CreateSnapshot("web", "before-upgrade", "clean install"); err != nil {
		t.Fatal(err)
	}
	snaps, err := h.m.ListSnapshots("web")
	if err != nil || len(snaps) != 1 || snaps[0].Name != "before-upgrade" || snaps[0].State != "shutoff" || snaps[0].Description != "clean install" {
		t.Fatalf("ListSnapshots = %+v, %v", snaps, err)
	}
	// The API's resize refusal relies on InspectDisk counting these clones.
	for _, d := range []string{boot, vdb} {
		if _, err := os.Stat(filepath.Join(storage.SnapshotDir(d), "before-upgrade"+storage.DiskExt)); err != nil {
			t.Errorf("no snapshot clone beside %s: %v", filepath.Base(d), err)
		}
		if info, _ := storage.InspectDisk(d); isDarwin() && info.Snapshots != 1 {
			t.Errorf("InspectDisk(%s).Snapshots = %d, want 1", filepath.Base(d), info.Snapshots)
		}
	}
	if err := h.m.CreateSnapshot("web", "before-upgrade", ""); err == nil {
		t.Error("a second snapshot with the same name was taken")
	}
	if err := h.m.CreateSnapshot("web", "../escape", ""); err == nil {
		t.Error("a snapshot name with a path in it was accepted")
	}

	writeAt(t, boot, 0, "boot-v2")
	writeAt(t, vdb, 0, "data-v2")
	writeAt(t, nvram, 0, "efi-v2")
	if err := h.m.RestoreSnapshot("web", "before-upgrade"); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{boot: "boot-v1", vdb: "data-v1", nvram: "efi-v1"} {
		if got := readFile(t, path); got != want {
			t.Errorf("%s = %q after restore, want %q", filepath.Base(path), got, want)
		}
	}
	// Restoring a snapshot of a stopped VM leaves it stopped, as libvirt does.
	if o := observe(t, h.m, "web"); o.State != libvirt.DomainShutoff || libvirt.DomainShutoffReason(o.Reason) != libvirt.DomainShutoffFromSnapshot {
		t.Errorf("after restore: %+v, want shutoff/from-snapshot", o)
	}
	if h.machineCount() != 0 {
		t.Error("restoring a stopped snapshot booted the VM")
	}

	if err := h.m.DeleteSnapshot("web", "before-upgrade"); err != nil {
		t.Fatal(err)
	}
	if snaps, _ := h.m.ListSnapshots("web"); len(snaps) != 0 {
		t.Errorf("snapshots after delete: %+v", snaps)
	}
	if left, _ := filepath.Glob(filepath.Join(storage.SnapshotDir(boot), "*")); len(left) != 0 {
		t.Errorf("files left after deleting the only snapshot: %v", left)
	}
	if err := h.m.RestoreSnapshot("web", "before-upgrade"); err == nil {
		t.Error("restored a deleted snapshot")
	}
}

// The name arrives from the URL path, where a {snap} segment may carry an
// escaped slash, and is joined straight into a file path.
func TestSnapshotNameCannotLeaveTheSnapshotDir(t *testing.T) {
	h := newHarness(t)
	h.define("web")
	boot := filepath.Join(h.dir, "web.raw")
	planted := filepath.Join(filepath.Dir(storage.SnapshotDir(boot)), "planted.json")
	if err := os.WriteFile(planted, []byte(`{"name":"x","disks":["`+boot+`"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.m.DeleteSnapshot("web", "../planted"); err == nil {
		t.Error("DeleteSnapshot accepted a name with a path in it")
	}
	if err := h.m.RestoreSnapshot("web", "../planted"); err == nil {
		t.Error("RestoreSnapshot accepted a name with a path in it")
	}
	if _, err := os.Stat(planted); err != nil {
		t.Errorf("file outside the snapshot dir was touched: %v", err)
	}
}

func TestSnapshotOfARunningVM(t *testing.T) {
	h := newHarness(t)
	h.define("web")
	boot := filepath.Join(h.dir, "web.raw")
	if err := os.WriteFile(boot, []byte("boot-v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.m.Start("web"); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, h.m, "web")
	first := h.last()

	if err := h.m.CreateSnapshot("web", "live", ""); err != nil {
		t.Fatal(err)
	}
	if _, pauses := first.counts(); pauses != 1 {
		t.Errorf("the VM was paused %d times for the snapshot, want 1", pauses)
	}
	waitRunning(t, h.m, "web") // and resumed
	if snaps, _ := h.m.ListSnapshots("web"); len(snaps) != 1 || snaps[0].State != "running" {
		t.Fatalf("snapshots = %+v", snaps)
	}

	writeAt(t, boot, 0, "boot-v2")
	if err := h.m.RestoreSnapshot("web", "live"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, boot); got != "boot-v1" {
		t.Errorf("boot disk = %q after restore", got)
	}
	// A snapshot taken running comes back running, on a fresh boot.
	if h.machineCount() != 2 || first.State() != MachineStopped {
		t.Errorf("machines = %d, first state %v; want the old one stopped and a new one booted", h.machineCount(), first.State())
	}
	waitRunning(t, h.m, "web")
}

func TestDeletingADiskRemovesItsSnapshots(t *testing.T) {
	h := newHarness(t)
	vdb := filepath.Join(h.dir, "web-vdb.raw")
	if err := os.WriteFile(vdb, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.define("web", func(e *metadata.VMEntry) { e.Disks = []metadata.Disk{{Target: "vdb", Path: vdb}} })
	if err := h.m.CreateSnapshot("web", "s1", ""); err != nil {
		t.Fatal(err)
	}
	if err := storage.DeleteDisk(vdb); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(storage.SnapshotDir(vdb)); !os.IsNotExist(err) && isDarwin() {
		t.Error("a deleted disk's snapshots stayed behind")
	}
}

// isDarwin: the storage hooks behind snapshots (counting, sidecar removal)
// only act on macOS, where disks are raw.
func isDarwin() bool { return runtime.GOOS == "darwin" }
