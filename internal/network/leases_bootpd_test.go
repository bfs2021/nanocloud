package network

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const bootpdFixture = `{
	name=web
	ip_address=192.168.64.3
	hw_address=1,52:54:0:ab:c:ef
	identifier=1,52:54:0:ab:c:ef
	lease=0x7fffffff
}
{
	name=old
	ip_address=192.168.64.2
	hw_address=1,52:54:0:0:0:1
	identifier=1,52:54:0:0:0:1
	lease=0x5f5e1000
}
{
	name=nomac
	ip_address=192.168.64.9
	lease=0x7fffffff
}
`

func TestParseBootpdLeases(t *testing.T) {
	got, err := parseBootpdLeases(strings.NewReader(bootpdFixture), time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d leases (%+v), want only the unexpired one with a MAC", len(got), got)
	}
	want := DHCPLease{IPAddress: "192.168.64.3", MACAddress: "52:54:00:ab:0c:ef", Hostname: "web", ExpiryTime: 0x7fffffff}
	if got[0] != want {
		t.Fatalf("lease = %+v, want %+v", got[0], want)
	}
}

func TestReadBootpdLeasesMissingFile(t *testing.T) {
	got, err := ReadBootpdLeases(filepath.Join(t.TempDir(), "nope"))
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("ReadBootpdLeases(missing) = %v, %v; want an empty non-nil slice", got, err)
	}
}

func TestBootpdLeasesAsManagerSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dhcpd_leases")
	if err := os.WriteFile(path, []byte(bootpdFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := NewWithLeases(BootpdLeases(path)).Leases()
	if err != nil || len(got) != 1 || got[0].Hostname != "web" {
		t.Fatalf("Manager.Leases() = %+v, %v; want the bootpd lease", got, err)
	}
}

func TestNormalizeMAC(t *testing.T) {
	for in, want := range map[string]string{
		"52:54:0:AB:c:ef":   "52:54:00:ab:0c:ef",
		"52:54:00:ab:0c:ef": "52:54:00:ab:0c:ef",
		" A:B:C:D:E:F ":     "0a:0b:0c:0d:0e:0f",
		"not-a-mac":         "not-a-mac",
	} {
		if got := NormalizeMAC(in); got != want {
			t.Errorf("NormalizeMAC(%q) = %q, want %q", in, got, want)
		}
	}
}
