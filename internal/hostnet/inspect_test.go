package hostnet

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// fixture builds a root containing sys/class/net and proc/net trees.
type fixture struct {
	t    *testing.T
	root string
}

// newFixture builds a host with IPv6; noIPv6 takes it away.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, root: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(f.root, "proc", "sys", "net", "ipv6", "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) noIPv6() *fixture {
	f.t.Helper()
	if err := os.RemoveAll(filepath.Join(f.root, "proc", "sys", "net", "ipv6")); err != nil {
		f.t.Fatal(err)
	}
	return f
}

// iface also gives the link its per-interface IPv6 conf directory.
func (f *fixture) iface(name string) *fixture {
	f.t.Helper()
	for _, dir := range []string{
		filepath.Join(f.root, "sys", "class", "net", name),
		filepath.Join(f.root, "proc", "sys", "net", "ipv6", "conf", name),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			f.t.Fatal(err)
		}
	}
	return f
}

func (f *fixture) nic(name string) *fixture {
	f.t.Helper()
	f.iface(name)
	if err := os.MkdirAll(filepath.Join(f.root, "sys", "class", "net", name, "device"), 0o755); err != nil {
		f.t.Fatal(err)
	}
	return f
}

// tap records a tun/tap device, which is what libvirt's vnetN ports are.
func (f *fixture) tap(name string) *fixture {
	f.t.Helper()
	f.iface(name)
	if err := os.WriteFile(filepath.Join(f.root, "sys", "class", "net", name, "tun_flags"), []byte("0x1002\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return f
}

func (f *fixture) bridge(name string, ports ...string) *fixture {
	f.t.Helper()
	f.iface(name)
	if err := os.MkdirAll(filepath.Join(f.root, "sys", "class", "net", name, "bridge"), 0o755); err != nil {
		f.t.Fatal(err)
	}
	for _, p := range ports {
		if err := os.MkdirAll(filepath.Join(f.root, "sys", "class", "net", name, "brif", p), 0o755); err != nil {
			f.t.Fatal(err)
		}
	}
	return f
}

// vlan records a VLAN subinterface the way /proc/net/vlan/<name> does.
func (f *fixture) vlan(name, parent string, vid int) *fixture {
	f.t.Helper()
	f.iface(name)
	dir := filepath.Join(f.root, "proc", "net", "vlan")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	body := name + "  VID: " + strconv.Itoa(vid) + "\t REORDER_HDR: 1  dev->priv_flags: 1\nDevice: " + parent + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return f
}

func (f *fixture) route(iface string) *fixture {
	f.t.Helper()
	dir := filepath.Join(f.root, "proc", "net")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	body := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\n" +
		"lo\t0000007F\t00000000\t0001\t0\t0\t0\t000000FF\n" +
		iface + "\t00000000\t0102A8C0\t0003\t0\t0\t100\t00000000\n"
	if err := os.WriteFile(filepath.Join(dir, "route"), []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return f
}

func (f *fixture) manager() *Manager {
	m := New()
	m.root = f.root
	m.addrs = func(string) ([]string, error) { return nil, nil }
	return m
}

func TestNames(t *testing.T) {
	if got := BridgeName(20); got != "nc-vlan20" {
		t.Errorf("BridgeName(20) = %q, want nc-vlan20", got)
	}
	if got := LinkName("eth0", 20); got != "eth0.20" {
		t.Errorf("LinkName = %q, want eth0.20", got)
	}
}

func TestList_ReportsOnlyOurNamespace(t *testing.T) {
	f := newFixture(t)
	f.nic("eth0")
	f.vlan("eth0.20", "eth0", 20).bridge("nc-vlan20", "eth0.20")
	// Neither of these is ours: a foreign VLAN link, and a foreign bridge.
	f.vlan("eth0.99", "eth0", 99)
	f.bridge("br0", "eth0")

	got, err := f.manager().List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List = %+v, want exactly the one nc-vlan link", got)
	}
	if got[0].Bridge != "nc-vlan20" || got[0].Link != "eth0.20" || got[0].Uplink != "eth0" || got[0].VID != 20 {
		t.Fatalf("List[0] = %+v", got[0])
	}
}

func TestDefaultRouteInterface(t *testing.T) {
	f := newFixture(t).route("eth0")
	got, err := f.manager().DefaultRouteInterface()
	if err != nil {
		t.Fatalf("DefaultRouteInterface: %v", err)
	}
	if got != "eth0" {
		t.Errorf("got %q, want eth0", got)
	}
}

func TestDefaultRouteInterface_NoDefaultRouteIsAnError(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(filepath.Join(f.root, "proc", "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "proc", "net", "route"),
		[]byte("Iface\tDestination\tGateway\n lo\t0000007F\t00000000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager().DefaultRouteInterface(); err == nil {
		t.Fatal("want an error when the host has no default route, got nil")
	}
}
