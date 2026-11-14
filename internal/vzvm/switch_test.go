package vzvm

import (
	"bytes"
	"context"
	"errors"
	"net"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/vswitch"
)

// nullUplink is a host side that never sends and swallows what it is given.
type nullUplink struct {
	closed chan struct{}
	once   sync.Once
}

func (u *nullUplink) ReadFrame([]byte) (int, error) { <-u.closed; return 0, net.ErrClosed }
func (u *nullUplink) WriteFrame([]byte) error       { return nil }
func (u *nullUplink) Close() error                  { u.once.Do(func() { close(u.closed) }); return nil }

// switchHarness is a harness whose Manager links VMs into a real switch,
// running in this process.
func switchHarness(t *testing.T) (h *harness, sock string, ensured *atomic.Int32) {
	t.Helper()
	h = newHarness(t)
	sock = filepath.Join(h.run, "sw.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = vswitch.Run(ctx, sock, &nullUplink{closed: make(chan struct{})}, 1514)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	eventually(t, "the switch to answer", func() bool { _, err := vswitch.Ping(sock); return err == nil })

	ensured = new(atomic.Int32)
	_ = h.m.Close()
	h.m = New(Config{Spawn: h.procs.Spawn, RunDir: h.run, Switch: sock, EnsureSwitch: func() error {
		ensured.Add(1)
		return nil
	}})
	return h, sock, ensured
}

func switchPorts(sock string) []string {
	r, err := vswitch.Ping(sock)
	if err != nil {
		return nil
	}
	var names []string
	for _, p := range r.Ports {
		names = append(names, p.Name+" "+p.MAC)
	}
	sort.Strings(names)
	return names
}

func TestVMsAreLinkedIntoTheSwitch(t *testing.T) {
	h, sock, ensured := switchHarness(t)
	h.define("a")
	h.define("b")
	var nics = map[string]net.Conn{}
	for _, id := range []string{"a", "b"} {
		if err := h.m.Start(id); err != nil {
			t.Fatal(err)
		}
		spec := h.last().spec
		if spec.Network == nil {
			t.Fatalf("%s booted without a link into the switch", id)
		}
		c, err := net.FileConn(spec.Network) // the guest's NIC, as the test drives it
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() }) //nolint:errcheck // test cleanup
		nics[id] = c
	}
	if ensured.Load() < 2 {
		t.Errorf("EnsureSwitch called %d times, want before each boot", ensured.Load())
	}

	idA, _ := h.m.Identity("a")
	idB, _ := h.m.Identity("b")
	want := []string{"a " + idA.MAC, "b " + idB.MAC}
	eventually(t, "both VMs on the switch under their own MACs", func() bool {
		return strings.Join(switchPorts(sock), ",") == strings.Join(want, ",")
	})

	macA, _ := net.ParseMAC(idA.MAC)
	macB, _ := net.ParseMAC(idB.MAC)
	frame := append(append(append([]byte{}, macB...), macA...), 0x08, 0x00, 'h', 'i')
	if _, err := nics["a"].Write(frame); err != nil {
		t.Fatal(err)
	}
	_ = nics["b"].SetReadDeadline(time.Now().Add(2 * time.Second))
	got := make([]byte, 2048)
	n, err := nics["b"].Read(got)
	if err != nil || !bytes.Equal(got[:n], frame) {
		t.Fatalf("b read %x, %v; want a's frame", got[:n], err)
	}

	if err := h.m.Destroy("a"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a's port to go when its VM stops", func() bool {
		return strings.Join(switchPorts(sock), ",") == "b "+idB.MAC
	})
}

func TestStartFailsWithoutTheSwitch(t *testing.T) {
	h := newHarness(t)
	_ = h.m.Close()
	h.m = New(Config{Spawn: h.procs.Spawn, RunDir: h.run, Switch: filepath.Join(h.run, "sw.sock"),
		EnsureSwitch: func() error { return errors.New("vmnet: permission denied") }})
	h.define("web")
	err := h.m.Start("web")
	if err == nil || !strings.Contains(err.Error(), "network switch: vmnet: permission denied") {
		t.Fatalf("Start = %v, want it to name the switch and why", err)
	}
	if h.machineCount() != 0 {
		t.Error("a machine was booted with no network to link it to")
	}
}

func TestSwitchStatusWithoutSwitch(t *testing.T) {
	h := newHarness(t)
	if _, err := h.m.SwitchStatus(); !errors.Is(err, vswitch.ErrNoSwitch) {
		t.Fatalf("SwitchStatus = %v, want ErrNoSwitch on a NAT-only manager", err)
	}
}
