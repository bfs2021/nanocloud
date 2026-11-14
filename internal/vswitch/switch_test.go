package vswitch

import (
	"bytes"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakePort is a Port backed by channels: in is what the port sends into the
// switch, out collects what the switch delivers to it.
type fakePort struct {
	in     chan []byte
	ready  chan struct{} // the switch is waiting for this port's next frame
	primed bool          // a ready signal was already taken; only send touches it
	busy   atomic.Bool

	mu     sync.Mutex
	out    [][]byte
	closed chan struct{}
	once   sync.Once
}

func newFakePort() *fakePort {
	return &fakePort{in: make(chan []byte), ready: make(chan struct{}), closed: make(chan struct{})}
}

func (f *fakePort) ReadFrame(p []byte) (int, error) {
	select {
	case f.ready <- struct{}{}:
	case <-f.closed:
		return 0, net.ErrClosed
	}
	select {
	case b := <-f.in:
		return copy(p, b), nil
	case <-f.closed:
		return 0, net.ErrClosed
	}
}

func (f *fakePort) WriteFrame(p []byte) error {
	if f.busy.Load() {
		return ErrBusy
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.out = append(f.out, append([]byte(nil), p...))
	return nil
}

func (f *fakePort) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

// send pushes a frame into the switch and returns once the switch has
// forwarded it, which is when its pump comes back for the next frame.
func (f *fakePort) send(t *testing.T, frame []byte) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	waitReady := func() {
		select {
		case <-f.ready:
		case <-timeout:
			t.Fatal("the switch is not reading from this port")
		}
	}
	if !f.primed { // after a send, the switch is already waiting for the next frame
		waitReady()
		f.primed = true
	}
	select {
	case f.in <- frame:
	case <-timeout:
		t.Fatal("the switch did not take the frame")
	}
	waitReady()
}

func (f *fakePort) got() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.out
}

var (
	broadcast = MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	gateway   = MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01} // something beyond the uplink
)

func frame(dst, src MAC, payload string) []byte {
	return append(append(append([]byte{}, dst[:]...), src[:]...), append([]byte{0x08, 0x00}, payload...)...)
}

type rig struct {
	sw     *Switch
	uplink *fakePort
	vms    map[string]*fakePort
	macs   map[string]MAC
	detach map[string]func()
}

// newRig wires a switch with an uplink and VMs a, b and c.
func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{sw: New(1514), uplink: newFakePort(), vms: map[string]*fakePort{}, macs: map[string]MAC{}, detach: map[string]func(){}}
	go r.sw.SetUplink(r.uplink)            //nolint:errcheck // ends when the test closes the port
	t.Cleanup(func() { r.uplink.Close() }) //nolint:errcheck // fake
	for r.sw.Status().Uplink == nil {
		time.Sleep(time.Millisecond)
	}
	for i, name := range []string{"a", "b", "c"} {
		m := MAC{0x52, 0x54, 0x00, 0, 0, byte(i + 1)}
		p := newFakePort()
		detach, err := r.sw.Attach(name, m, p)
		if err != nil {
			t.Fatal(err)
		}
		r.detach[name] = detach
		t.Cleanup(func() { p.Close() }) //nolint:errcheck // fake
		r.vms[name], r.macs[name] = p, m
	}
	return r
}

// delivered names which ports received exactly f.
func (r *rig) delivered(f []byte) []string {
	var to []string
	check := func(name string, p *fakePort) {
		for _, g := range p.got() {
			if bytes.Equal(g, f) {
				to = append(to, name)
			}
		}
	}
	for _, name := range []string{"a", "b", "c"} {
		check(name, r.vms[name])
	}
	check("uplink", r.uplink)
	return to
}

func TestForwarding(t *testing.T) {
	tests := []struct {
		name     string
		from     string // a VM, or "uplink"
		dst, src func(r *rig) MAC
		want     []string
	}{
		{"VM to VM goes only to that VM", "a", vm("b"), vm("a"), []string{"b"}},
		{"VM broadcast reaches every other VM and the host", "a", fixed(broadcast), vm("a"), []string{"b", "c", "uplink"}},
		{"VM multicast floods like broadcast", "b", fixed(MAC{0x01, 0x00, 0x5e, 0, 0, 0xfb}), vm("b"), []string{"a", "c", "uplink"}},
		{"VM to an unknown MAC goes to the host", "a", fixed(gateway), vm("a"), []string{"uplink"}},
		{"host to a VM goes only to that VM", "uplink", vm("c"), fixed(gateway), []string{"c"}},
		{"host broadcast reaches every VM, never back", "uplink", fixed(broadcast), fixed(gateway), []string{"a", "b", "c"}},
		{"host to an unknown MAC goes nowhere", "uplink", fixed(MAC{0x02, 0, 0, 0, 0, 9}), fixed(gateway), nil},
		{"a VM sending to itself goes nowhere", "a", vm("a"), vm("a"), nil},
		{"a VM spoofing another VM's MAC is dropped", "a", vm("c"), vm("b"), nil},
		{"a VM spoofing an outside MAC is dropped", "a", fixed(broadcast), fixed(gateway), nil},
		{"the host claiming a VM's MAC is dropped", "uplink", vm("b"), vm("a"), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			f := frame(tt.dst(r), tt.src(r), tt.name)
			from := r.uplink
			if tt.from != "uplink" {
				from = r.vms[tt.from]
			}
			from.send(t, f)
			if got := r.delivered(f); !equal(got, tt.want) {
				t.Errorf("delivered to %v, want %v", got, tt.want)
			}
		})
	}
}

func vm(name string) func(*rig) MAC { return func(r *rig) MAC { return r.macs[name] } }
func fixed(m MAC) func(*rig) MAC    { return func(*rig) MAC { return m } }

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCounters(t *testing.T) {
	r := newRig(t)
	ok := frame(r.macs["b"], r.macs["a"], "hello")
	r.vms["a"].send(t, ok)
	r.vms["a"].send(t, frame(r.macs["b"], r.macs["c"], "spoof"))
	r.vms["a"].send(t, []byte{1, 2, 3})
	r.vms["a"].send(t, make([]byte, 1515))
	r.vms["b"].busy.Store(true)
	r.vms["c"].send(t, frame(r.macs["b"], r.macs["c"], "to a busy port"))

	st := r.sw.Status()
	byName := map[string]Counters{}
	for _, p := range st.Ports {
		byName[p.Name] = p.Counters
	}
	a, b := byName["a"], byName["b"]
	if a.RxFrames != 1 || a.RxBytes != uint64(len(ok)) || a.Spoofed != 1 || a.Malformed != 2 {
		t.Errorf("a = %+v, want 1 frame received, 1 spoofed, 2 malformed", a)
	}
	if b.TxFrames != 1 || b.Dropped != 1 {
		t.Errorf("b = %+v, want 1 frame delivered and 1 dropped", b)
	}
	if st.Uplink == nil || st.Uplink.Name != "uplink" {
		t.Errorf("uplink = %+v", st.Uplink)
	}
	if got := []string{st.Ports[0].Name, st.Ports[1].Name, st.Ports[2].Name}; !equal(got, []string{"a", "b", "c"}) {
		t.Errorf("ports in order %v, want sorted by name", got)
	}
}

func TestAttach(t *testing.T) {
	t.Run("a MAC held by another VM is refused", func(t *testing.T) {
		r := newRig(t)
		if _, err := r.sw.Attach("intruder", r.macs["a"], newFakePort()); err == nil {
			t.Fatal("attached a second VM with a's MAC")
		}
		f := frame(r.macs["a"], r.macs["b"], "still a's")
		r.vms["b"].send(t, f)
		if got := r.delivered(f); !equal(got, []string{"a"}) {
			t.Errorf("a's traffic went to %v", got)
		}
	})

	t.Run("reattaching replaces and closes the old port", func(t *testing.T) {
		r := newRig(t)
		old := r.vms["a"]
		fresh := newFakePort()
		t.Cleanup(func() { fresh.Close() }) //nolint:errcheck // fake
		if _, err := r.sw.Attach("a", r.macs["a"], fresh); err != nil {
			t.Fatal(err)
		}
		select {
		case <-old.closed:
		case <-time.After(2 * time.Second):
			t.Fatal("the replaced port was not closed")
		}
		f := frame(r.macs["a"], r.macs["b"], "to the new port")
		r.vms["b"].send(t, f)
		if len(fresh.got()) != 1 || len(old.got()) != 0 {
			t.Errorf("new port got %d frames, old got %d; want 1 and 0", len(fresh.got()), len(old.got()))
		}
	})

	t.Run("a port that fails is detached", func(t *testing.T) {
		r := newRig(t)
		r.vms["c"].Close() //nolint:errcheck // fake
		deadline := time.Now().Add(2 * time.Second)
		for len(r.sw.Status().Ports) != 2 {
			if time.Now().After(deadline) {
				t.Fatal("the closed port is still attached")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if _, err := r.sw.Attach("other", r.macs["c"], newFakePort()); err != nil {
			t.Errorf("the closed port's MAC is still held: %v", err)
		}
	})

	t.Run("detach frees the name and MAC", func(t *testing.T) {
		r := newRig(t)
		r.detach["b"]()
		select {
		case <-r.vms["b"].closed:
		case <-time.After(2 * time.Second):
			t.Fatal("the detached port was not closed")
		}
		f := frame(r.macs["b"], r.macs["a"], "to a detached VM")
		r.vms["a"].send(t, f)
		if got := r.delivered(f); !equal(got, []string{"uplink"}) {
			t.Errorf("delivered to %v, want only the uplink", got)
		}
	})
}

func TestParseVMMAC(t *testing.T) {
	for _, s := range []string{"52:54:00:ab:cd:ef", "02:00:00:00:00:01"} {
		if _, err := ParseVMMAC(s); err != nil {
			t.Errorf("ParseVMMAC(%q): %v", s, err)
		}
	}
	for _, s := range []string{"", "nonsense", "ff:ff:ff:ff:ff:ff", "01:00:5e:00:00:fb", "00:00:00:00:00:00", "00:00:00:00:00:00:00:01"} {
		if _, err := ParseVMMAC(s); err == nil {
			t.Errorf("ParseVMMAC(%q) accepted it", s)
		}
	}
}
