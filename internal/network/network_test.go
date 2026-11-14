package network_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/network"
)

// TestListBridges guards that bridges are found by sysfs, not by name: a
// non-bridge named br0 is excluded and a bridge named vmbr0 is included.
func TestListBridges(t *testing.T) {
	root := t.TempDir()
	for name, filtering := range map[string]string{
		"vmbr0":  "1\n", // a real bridge under a name br0-matching never saw
		"br-lan": "0\n", // real, but a VLAN tag on it would be silently dropped
	} {
		dir := filepath.Join(root, "sys", "class", "net", name, "bridge")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "vlan_filtering"), []byte(filtering), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A dummy or bond named br0: no bridge/ directory, so not a bridge.
	if err := os.MkdirAll(filepath.Join(root, "sys", "class", "net", "br0"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := network.ListBridges(root)
	if err != nil {
		t.Fatalf("ListBridges: %v", err)
	}
	want := []network.Bridge{{Name: "br-lan"}, {Name: "vmbr0", VLANFiltering: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListBridges = %+v, want %+v", got, want)
	}
}

// TestListBridges_NoSysfs guards that a host without sysfs reports no bridges
// rather than an error.
func TestListBridges_NoSysfs(t *testing.T) {
	got, err := network.ListBridges(t.TempDir())
	if err != nil || len(got) != 0 {
		t.Errorf("ListBridges (no sysfs) = %v, %v; want empty, nil", got, err)
	}
}

// writeFakeNet builds a sysfs tree under root: bridges maps a bridge name to
// its ports, and each physical interface gets a device/ directory.
func writeFakeNet(t *testing.T, root string, bridges map[string][]string, physical []string) {
	t.Helper()
	base := filepath.Join(root, "sys", "class", "net")
	for br, ports := range bridges {
		if err := os.MkdirAll(filepath.Join(base, br, "bridge"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, br, "bridge", "vlan_filtering"), []byte("0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, p := range ports {
			if err := os.MkdirAll(filepath.Join(base, br, "brif", p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(base, p), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, p := range physical {
		if err := os.MkdirAll(filepath.Join(base, p, "device"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListBridges_ReportsPhysicalUplink(t *testing.T) {
	root := t.TempDir()
	writeFakeNet(t, root,
		map[string][]string{
			"br0":     {"eth0"},       // a real LAN bridge
			"virbr0":  {"virbr0-nic"}, // libvirt's NAT bridge: no physical port
			"docker0": {},             // no ports at all
		},
		[]string{"eth0"}, // only eth0 is a NIC
	)

	got, err := network.ListBridges(root)
	if err != nil {
		t.Fatalf("ListBridges: %v", err)
	}

	want := map[string]string{"br0": "eth0", "virbr0": "", "docker0": ""}
	if len(got) != len(want) {
		t.Fatalf("got %d bridges, want %d: %+v", len(got), len(want), got)
	}
	for _, b := range got {
		if w, ok := want[b.Name]; !ok {
			t.Errorf("unexpected bridge %q", b.Name)
		} else if b.Uplink != w {
			t.Errorf("%s: uplink = %q, want %q", b.Name, b.Uplink, w)
		}
	}
}

func TestWithUplink_ExcludesBridgesWithNoWayOffTheHost(t *testing.T) {
	in := []network.Bridge{
		{Name: "br0", Uplink: "eth0"},
		{Name: "virbr0"},
		{Name: "docker0"},
		{Name: "vmbr0", Uplink: "enp3s0"},
	}
	got := network.WithUplink(in)
	if len(got) != 2 || got[0].Name != "br0" || got[1].Name != "vmbr0" {
		t.Fatalf("WithUplink = %+v, want br0 and vmbr0 only", got)
	}
}

func TestWithUplink_EmptyInputIsNonNil(t *testing.T) {
	// nil means "could not enumerate", so an empty input must stay non-nil.
	if got := network.WithUplink([]network.Bridge{}); got == nil {
		t.Fatal("WithUplink([]) returned nil, which validateNetwork reads as unknown")
	}
}

func TestListInterfaces_ReturnsAtLeastLoopback(t *testing.T) {
	ifaces, err := network.ListInterfaces()
	if err != nil {
		t.Fatalf("ListInterfaces: unexpected error: %v", err)
	}
	found := false
	for _, iface := range ifaces {
		if iface.Name == "lo" || iface.Name == "lo0" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListInterfaces: no loopback interface (lo/lo0) found among %d interfaces", len(ifaces))
	}
}

func TestReadLeases_FileNotExist(t *testing.T) {
	leases, err := network.ReadLeases("/nonexistent/path/leases.status")
	if err != nil {
		t.Fatalf("ReadLeases (missing file): unexpected error: %v", err)
	}
	if len(leases) != 0 {
		t.Errorf("ReadLeases (missing file): got %d leases, want 0", len(leases))
	}
}

// TestReadLeases_ValidJSON guards parsing and expired-lease filtering. Expiry
// times are relative to now, since a fixed timestamp would expire as the
// fixture ages.
func TestReadLeases_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "virbr0.status")

	valid := time.Now().Add(time.Hour).Unix()
	data := []map[string]interface{}{
		{
			"ip-address":  "192.168.122.10",
			"mac-address": "52:54:00:ab:cd:ef",
			"hostname":    "test-vm-1",
			"expiry-time": valid,
		},
		{
			"ip-address":  "192.168.122.11",
			"mac-address": "52:54:00:11:22:33",
			"hostname":    "test-vm-2",
			"expiry-time": valid + 1,
		},
		{
			// Expired: must not be reported.
			"ip-address":  "192.168.122.12",
			"mac-address": "52:54:00:de:ad:00",
			"hostname":    "expired-vm",
			"expiry-time": time.Now().Add(-time.Hour).Unix(),
		},
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal test data: %v", err)
	}
	if writeErr := os.WriteFile(path, raw, 0o644); writeErr != nil {
		t.Fatalf("write leases file: %v", writeErr)
	}

	leases, err := network.ReadLeases(path)
	if err != nil {
		t.Fatalf("ReadLeases: unexpected error: %v", err)
	}
	if len(leases) != 2 {
		t.Fatalf("ReadLeases: got %d leases, want 2 (the expired one dropped): %+v", len(leases), leases)
	}

	if leases[0].IPAddress != "192.168.122.10" {
		t.Errorf("leases[0].IPAddress = %q, want %q", leases[0].IPAddress, "192.168.122.10")
	}
	if leases[0].MACAddress != "52:54:00:ab:cd:ef" {
		t.Errorf("leases[0].MACAddress = %q, want %q", leases[0].MACAddress, "52:54:00:ab:cd:ef")
	}
	if leases[0].Hostname != "test-vm-1" {
		t.Errorf("leases[0].Hostname = %q, want %q", leases[0].Hostname, "test-vm-1")
	}
	if leases[0].ExpiryTime != valid {
		t.Errorf("leases[0].ExpiryTime = %d, want %d", leases[0].ExpiryTime, valid)
	}

	if leases[1].IPAddress != "192.168.122.11" {
		t.Errorf("leases[1].IPAddress = %q, want %q", leases[1].IPAddress, "192.168.122.11")
	}
	if leases[1].Hostname != "test-vm-2" {
		t.Errorf("leases[1].Hostname = %q, want %q", leases[1].Hostname, "test-vm-2")
	}
}

// TestReadLeases_EmptyFile covers the zero-byte file libvirt may write
// initially.
func TestReadLeases_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "virbr0.status")

	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatalf("write empty file: %v", err)
	}

	leases, err := network.ReadLeases(path)
	if err != nil {
		t.Fatalf("ReadLeases (empty file): unexpected error: %v", err)
	}
	if len(leases) != 0 {
		t.Errorf("ReadLeases (empty file): got %d leases, want 0", len(leases))
	}
}

func TestReadLeases_EmptyArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "virbr0.status")

	if err := os.WriteFile(path, []byte("[]"), 0o644); err != nil {
		t.Fatalf("write [] file: %v", err)
	}

	leases, err := network.ReadLeases(path)
	if err != nil {
		t.Fatalf("ReadLeases (empty array): unexpected error: %v", err)
	}
	if len(leases) != 0 {
		t.Errorf("ReadLeases (empty array): got %d leases, want 0", len(leases))
	}
}
