package hostnet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// recorder captures argv instead of running it.
type recorder struct{ calls [][]string }

func (r *recorder) run(args ...string) error {
	r.calls = append(r.calls, args)
	return nil
}

func (r *recorder) disable(iface string) error {
	r.calls = append(r.calls, []string{"sysctl", iface, "disable_ipv6=1"})
	return nil
}

// attach routes both of m's write seams into one ordered log, so a test can
// assert the sysctl lands between a link's add and its up.
func (r *recorder) attach(m *Manager) { m.run, m.disableIPv6 = r.run, r.disable }

func (r *recorder) joined() []string {
	out := make([]string, 0, len(r.calls))
	for _, c := range r.calls {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

// enslavedTo records an existing /sys/class/net/<iface>/master symlink, so a
// test can build a link that already belongs to someone else's bridge.
func (f *fixture) enslavedTo(iface, bridge string) *fixture {
	f.t.Helper()
	if err := os.Symlink(bridge, filepath.Join(f.root, "sys", "class", "net", iface, "master")); err != nil {
		f.t.Fatal(err)
	}
	return f
}

// assertNeverTouchesUplinkOrFiltering checks the package's safety invariant.
// It must hold on every path that can enslave the link, including resume and
// the idempotent no-op, which can emit "master" without an "add".
func assertNeverTouchesUplinkOrFiltering(t *testing.T, calls [][]string) {
	t.Helper()
	for _, c := range calls {
		joined := strings.Join(c, " ")
		if strings.Contains(joined, "vlan_filtering") {
			t.Errorf("Ensure set vlan_filtering: %v", c)
		}
		// The uplink may appear only as the parent of an add, never as the
		// subject of a set.
		if len(c) >= 3 && c[0] == "link" && c[1] == "set" && c[2] == "eth0" {
			t.Errorf("Ensure wrote to the uplink: %v", c)
		}
		if strings.Contains(joined, "addr add") {
			t.Errorf("Ensure assigned an address: %v", c)
		}
		if c[0] == "sysctl" && c[1] == "eth0" {
			t.Errorf("Ensure changed the uplink's IPv6: %v", c)
		}
		if strings.Contains(joined, "master") && strings.Contains(joined, " eth0 ") {
			t.Errorf("Ensure enslaved a physical NIC: %v", c)
		}
	}
}

func TestEnsure_BuildsBothObjectsInOrder(t *testing.T) {
	f := newFixture(t)
	f.nic("eth0")
	m := f.manager()
	rec := &recorder{}
	rec.attach(m)

	got, err := m.Ensure("eth0", 20)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got != "nc-vlan20" {
		t.Fatalf("Ensure returned %q, want nc-vlan20", got)
	}

	want := []string{
		"link add link eth0 name eth0.20 type vlan id 20",
		"sysctl eth0.20 disable_ipv6=1",
		"link set eth0.20 up",
		"link add name nc-vlan20 type bridge",
		"sysctl nc-vlan20 disable_ipv6=1",
		"link set eth0.20 master nc-vlan20",
		"link set nc-vlan20 up",
	}
	gotCalls := rec.joined()
	if len(gotCalls) != len(want) {
		t.Fatalf("ran %d commands, want %d:\n%v", len(gotCalls), len(want), gotCalls)
	}
	for i := range want {
		if gotCalls[i] != want[i] {
			t.Errorf("command %d = %q, want %q", i, gotCalls[i], want[i])
		}
	}
	assertNeverTouchesUplinkOrFiltering(t, rec.calls)
}

func TestEnsure_IsIdempotentOnAHostAlreadyBuilt(t *testing.T) {
	f := newFixture(t)
	f.nic("eth0")
	f.vlan("eth0.20", "eth0", 20).bridge("nc-vlan20", "eth0.20")
	m := f.manager()
	rec := &recorder{}
	rec.attach(m)

	if _, err := m.Ensure("eth0", 20); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// The "set … up" calls are unconditional; nothing may be created twice.
	for _, c := range rec.joined() {
		if strings.Contains(c, "link add") {
			t.Errorf("Ensure re-created something on an already-built host: %q", c)
		}
	}
	assertNeverTouchesUplinkOrFiltering(t, rec.calls)
}

func TestEnsure_ResumesAHalfBuiltHost(t *testing.T) {
	// A crash between the two adds must be recoverable.
	f := newFixture(t)
	f.nic("eth0")
	f.vlan("eth0.20", "eth0", 20)
	m := f.manager()
	rec := &recorder{}
	rec.attach(m)

	if _, err := m.Ensure("eth0", 20); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	joined := strings.Join(rec.joined(), "\n")
	if strings.Contains(joined, "link add link eth0") {
		t.Error("re-created the existing subinterface")
	}
	if !strings.Contains(joined, "link add name nc-vlan20 type bridge") {
		t.Error("did not create the missing bridge")
	}
	assertNeverTouchesUplinkOrFiltering(t, rec.calls)
}

func TestEnsure_Refusals(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*fixture)
		uplink string
		vid    int
		want   string // substring of the error
	}{
		{
			name:   "a bridge wearing our name that is not a bridge",
			setup:  func(f *fixture) { f.nic("eth0").iface("nc-vlan20") },
			uplink: "eth0", vid: 20,
			want: "not a bridge",
		},
		{
			name: "our bridge holding a port we did not put there",
			setup: func(f *fixture) {
				f.nic("eth0").nic("eth1")
				f.vlan("eth0.20", "eth0", 20).bridge("nc-vlan20", "eth0.20", "eth1")
			},
			uplink: "eth0", vid: 20,
			want: "unexpected port",
		},
		{
			name: "a subinterface wearing our name with the wrong parent",
			setup: func(f *fixture) {
				f.nic("eth0").nic("eth1")
				f.vlan("eth0.20", "eth1", 20)
			},
			uplink: "eth0", vid: 20,
			want: "not a VLAN 20 link on eth0",
		},
		{
			name:   "an uplink this host does not have",
			setup:  func(f *fixture) {},
			uplink: "eth9", vid: 20,
			want: "no interface named",
		},
		{
			name:   "a tag outside 802.1Q",
			setup:  func(f *fixture) { f.nic("eth0") },
			uplink: "eth0", vid: 4095,
			want: "must be 1-4094",
		},
		{
			name:   "an uplink inside our own namespace",
			setup:  func(f *fixture) { f.iface("nc-vlan20") },
			uplink: "nc-vlan20", vid: 30,
			want: "cannot be a nano-cloud VLAN bridge",
		},
		{
			name:   "a composed name longer than the kernel allows",
			setup:  func(f *fixture) { f.nic("enp0s31f6extra") },
			uplink: "enp0s31f6extra", vid: 4094,
			want: "too long",
		},
		{
			name: "an existing subinterface already enslaved to a foreign bridge",
			setup: func(f *fixture) {
				// An operator's own eth0.20, attached to their own bridge.
				f.nic("eth0")
				f.vlan("eth0.20", "eth0", 20)
				f.enslavedTo("eth0.20", "br0")
			},
			uplink: "eth0", vid: 20,
			want: "already enslaved to",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			tt.setup(f)
			m := f.manager()
			rec := &recorder{}
			rec.attach(m)

			_, err := m.Ensure(tt.uplink, tt.vid)
			if err == nil {
				t.Fatalf("want a refusal containing %q, got nil", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
			if len(rec.calls) != 0 {
				t.Errorf("a refusal ran %d commands; it must run none: %v", len(rec.calls), rec.joined())
			}
		})
	}
}

func TestEnsure_RefusesABridgeHoldingAnAddress(t *testing.T) {
	// An address on nc-vlan20 means something else uses it as a host
	// interface.
	f := newFixture(t)
	f.nic("eth0")
	f.vlan("eth0.20", "eth0", 20).bridge("nc-vlan20", "eth0.20")
	m := f.manager()
	rec := &recorder{}
	rec.attach(m)
	m.addrs = func(iface string) ([]string, error) {
		if iface == "nc-vlan20" {
			return []string{"fe80::1/64", "2001:db8:20::1/64"}, nil
		}
		return nil, nil
	}

	// The kernel's fe80 is listed first; the refusal must name the configured
	// address behind it.
	if _, err := m.Ensure("eth0", 20); err == nil || !strings.Contains(err.Error(), "holds address 2001:db8:20::1/64") {
		t.Fatalf("want a refusal naming the routable address, got %v", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("a refusal ran commands: %v", rec.joined())
	}
}

func TestEnsure_RefusesAnExistingLinkHoldingAnAddress(t *testing.T) {
	// An address on an unbridged eth0.20 means it is the operator's own VLAN
	// interface.
	f := newFixture(t)
	f.nic("eth0")
	f.vlan("eth0.20", "eth0", 20)
	m := f.manager()
	rec := &recorder{}
	rec.attach(m)
	m.addrs = func(iface string) ([]string, error) {
		if iface == "eth0.20" {
			return []string{"169.254.7.7/16", "10.20.0.1/24"}, nil
		}
		return nil, nil
	}

	if _, err := m.Ensure("eth0", 20); err == nil || !strings.Contains(err.Error(), "holds address 10.20.0.1/24") {
		t.Fatalf("want a refusal naming the address, got %v", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("a refusal ran commands: %v", rec.joined())
	}
}

func TestEnsure_RefusesWhenAddressLookupFails(t *testing.T) {
	// A lookup error must not read as "no addresses".
	f := newFixture(t)
	f.nic("eth0")
	f.vlan("eth0.20", "eth0", 20).bridge("nc-vlan20", "eth0.20")
	m := f.manager()
	rec := &recorder{}
	rec.attach(m)
	m.addrs = func(iface string) ([]string, error) {
		if iface == "nc-vlan20" {
			return nil, errors.New("netlink: connection refused")
		}
		return nil, nil
	}

	if _, err := m.Ensure("eth0", 20); err == nil {
		t.Fatal("want a refusal when the address lookup fails, got nil")
	}
	if len(rec.calls) != 0 {
		t.Errorf("a refusal ran commands: %v", rec.joined())
	}
}

func TestEnsure_RefusesWhenBridgePortsAreUnreadable(t *testing.T) {
	// An unreadable brif must not read as "zero ports". Mode bits do not stop
	// root, so the test skips when run as root.
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0000 does not deny access, so the failure cannot be staged")
	}
	f := newFixture(t)
	f.nic("eth0")
	f.vlan("eth0.20", "eth0", 20).bridge("nc-vlan20", "eth0.20")
	brif := filepath.Join(f.root, "sys", "class", "net", "nc-vlan20", "brif")
	if err := os.Chmod(brif, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(brif, 0o755) })
	m := f.manager()
	rec := &recorder{}
	rec.attach(m)

	if _, err := m.Ensure("eth0", 20); err == nil {
		t.Fatal("want a refusal when bridge ports cannot be read, got nil")
	}
	if len(rec.calls) != 0 {
		t.Errorf("a refusal ran commands: %v", rec.joined())
	}
}

func TestEnsure_RefusesWhenTheMasterLinkIsUnreadable(t *testing.T) {
	// A Readlink failure must not read as "no master". Staged as ENOTDIR so it
	// also reproduces as root.
	f := newFixture(t)
	f.nic("eth0")
	f.vlan("eth0.20", "eth0", 20)
	dir := filepath.Join(f.root, "sys", "class", "net", "eth0.20")
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	m := f.manager()
	rec := &recorder{}
	rec.attach(m)

	if _, err := m.Ensure("eth0", 20); err == nil {
		t.Fatal("want a refusal when the master symlink cannot be read, got nil")
	}
	if len(rec.calls) != 0 {
		t.Errorf("a refusal ran commands: %v", rec.joined())
	}
}

func TestEnsure_NeverWritesTheUplinkOrVLANFiltering(t *testing.T) {
	f := newFixture(t)
	f.nic("eth0")
	m := f.manager()
	rec := &recorder{}
	rec.attach(m)

	if _, err := m.Ensure("eth0", 20); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	assertNeverTouchesUplinkOrFiltering(t, rec.calls)
}

// kernel simulates what each command builds, so a second Ensure finds the
// first one's pair. Like a real kernel, it gives a link a link-local address
// when it comes up unless IPv6 was turned off first.
type kernel struct {
	f     *fixture
	addrs map[string][]string
	off   map[string]bool
	up    map[string]bool
}

func newKernel(f *fixture, m *Manager) *kernel {
	k := &kernel{f: f, addrs: map[string][]string{}, off: map[string]bool{}, up: map[string]bool{}}
	m.run = k.run
	m.disableIPv6 = func(iface string) error {
		k.off[iface] = true
		delete(k.addrs, iface)
		return nil
	}
	m.addrs = func(iface string) ([]string, error) { return k.addrs[iface], nil }
	return k
}

func (k *kernel) run(args ...string) error {
	switch {
	case len(args) == 10 && args[1] == "add" && args[8] == "id": // link add link P name L type vlan id N
		vid, err := strconv.Atoi(args[9])
		if err != nil {
			k.f.t.Fatal(err)
		}
		k.f.vlan(args[5], args[3], vid)
	case len(args) == 6 && args[1] == "add" && args[5] == "bridge":
		k.f.bridge(args[3])
	case len(args) == 4 && args[1] == "set" && args[3] == "up":
		if !k.up[args[2]] && !k.off[args[2]] {
			k.addrs[args[2]] = append(k.addrs[args[2]], "fe80::5054:ff:fe00:1/64")
		}
		k.up[args[2]] = true
	case len(args) == 5 && args[3] == "master":
		k.f.bridge(args[4], args[2]).enslavedTo(args[2], args[4])
	default:
		k.f.t.Errorf("kernel fake: unexpected ip %v", args)
	}
	return nil
}

// A second Ensure on the same VLAN must accept the first VM's tap and the
// pair's link-locals, and the host must hold no address on the guest VLAN.
func TestEnsure_ASecondCallOnTheSameVLANSucceeds(t *testing.T) {
	f := newFixture(t)
	f.nic("eth0")
	m := f.manager()
	k := newKernel(f, m)

	for i := 1; i <= 2; i++ {
		if _, err := m.Ensure("eth0", 20); err != nil {
			t.Fatalf("Ensure #%d: %v", i, err)
		}
		vnet := fmt.Sprintf("vnet%d", i) // the VM this was for, now started
		f.tap(vnet).bridge("nc-vlan20", vnet)
		for iface, as := range k.addrs {
			if len(as) > 0 {
				t.Errorf("after Ensure #%d %s holds %v — the host is reachable on the guest VLAN", i, iface, as)
			}
		}
	}
}

// A pair holding only link-locals is adopted and has its IPv6 turned off.
func TestEnsure_AdoptsAPairHoldingOnlyLinkLocalAddresses(t *testing.T) {
	f := newFixture(t)
	f.nic("eth0")
	f.vlan("eth0.20", "eth0", 20).bridge("nc-vlan20", "eth0.20")
	m := f.manager()
	rec := &recorder{}
	rec.attach(m)
	m.addrs = func(string) ([]string, error) {
		return []string{"fe80::5054:ff:fe00:1/64", "169.254.3.4/16"}, nil
	}

	if _, err := m.Ensure("eth0", 20); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	joined := strings.Join(rec.joined(), "\n")
	for _, want := range []string{"sysctl eth0.20 disable_ipv6=1", "sysctl nc-vlan20 disable_ipv6=1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("adoption did not run %q:\n%s", want, joined)
		}
	}
	assertNeverTouchesUplinkOrFiltering(t, rec.calls)
}

// ipv6.disable=1 leaves no conf tree; writing into it would fail every VLAN.
func TestEnsure_LeavesIPv6AloneOnAKernelWithoutIt(t *testing.T) {
	f := newFixture(t)
	f.nic("eth0").noIPv6()
	m := f.manager()
	rec := &recorder{}
	rec.attach(m)

	if _, err := m.Ensure("eth0", 20); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for _, c := range rec.calls {
		if c[0] == "sysctl" {
			t.Errorf("wrote an IPv6 sysctl on a kernel without IPv6: %v", c)
		}
	}
}

func TestWriteDisableIPv6_WritesThePerLinkKnob(t *testing.T) {
	f := newFixture(t)
	f.iface("eth0.20")
	if err := f.manager().disableIPv6("eth0.20"); err != nil {
		t.Fatalf("disableIPv6: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(f.root, "proc", "sys", "net", "ipv6", "conf", "eth0.20", "disable_ipv6"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "1" {
		t.Errorf("disable_ipv6 = %q, want 1", got)
	}
}
