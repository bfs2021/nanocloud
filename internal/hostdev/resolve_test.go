package hostdev_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/hostdev"
)

// addressesOf and usbIDsOf flatten a resolution to the identities under test.
func addressesOf(devs []hostdev.PCIDevice) []string {
	out := make([]string, 0, len(devs))
	for _, d := range devs {
		out = append(out, d.Address)
	}
	return out
}

func usbIDsOf(devs []hostdev.USBDevice) []string {
	out := make([]string, 0, len(devs))
	for _, d := range devs {
		out = append(out, d.VendorID+":"+d.ProductID)
	}
	return out
}

// TestResolve covers the refuse/expand/warn contract: a refusal is an error, a
// caveat is a warning, and a picked device is never silently dropped.
func TestResolve(t *testing.T) {
	tests := []struct {
		name string
		// fixtures and sysRoot build the host this selection is checked against.
		fixtures []string
		sysRoot  string
		sel      hostdev.Selection

		wantPCI     []string
		wantUSB     []string
		wantWarning string // substring; "" means no warnings at all
		wantErr     string // substring; "" means success
	}{
		{
			// One pick, two devices.
			name:     "picking a GPU brings its audio function with it",
			fixtures: []string{"gpu", "gpu-audio"},
			sysRoot:  sysfsRoot,
			sel:      hostdev.Selection{PCI: []string{"0000:01:00.0"}},
			wantPCI:  []string{"0000:01:00.0", "0000:01:00.1"},
			// 01:00.0 is the fixture tree's boot_vga card.
			wantWarning: "driving this host's console",
		},
		{
			// The endpoint moves and the root port above it stays on the
			// host; naming it as a hostdev would fail the domain start.
			name:     "a bridge in the group stays with the host",
			fixtures: []string{"bridge", "nic"},
			sysRoot:  sysfsRoot,
			sel:      hostdev.Selection{PCI: []string{"0000:03:00.0"}},
			wantPCI:  []string{"0000:03:00.0"},
		},
		{
			name:     "picking both members of a group yields each once",
			fixtures: []string{"gpu", "gpu-audio"},
			sysRoot:  sysfsRoot,
			sel:      hostdev.Selection{PCI: []string{"0000:01:00.0", "0000:01:00.1"}},
			wantPCI:  []string{"0000:01:00.0", "0000:01:00.1"},
			// Selecting the audio function explicitly does not add a second
			// copy of the boot_vga note for the card it came with.
			wantWarning: "driving this host's console",
		},
		{
			name:     "a device the host does not have is refused by address",
			fixtures: []string{"gpu", "gpu-audio"},
			sysRoot:  sysfsRoot,
			sel:      hostdev.Selection{PCI: []string{"0000:99:00.0"}},
			wantErr:  "no PCI device at 0000:99:00.0",
		},
		{
			// The refusal carries the inventory's reason.
			name:     "an unusable device is refused with the inventory's reason",
			fixtures: []string{"sata", "onboard-audio"},
			sysRoot:  sysfsRoot,
			sel:      hostdev.Selection{PCI: []string{"0000:00:1f.3"}},
			wantErr:  "carries the host's own disks",
		},
		{
			// IOMMU off is the one refusal with a fix the user can type.
			name:     "PCI on a host with IOMMU off is refused with the kernel remedy",
			fixtures: []string{"gpu-no-iommu"},
			sysRoot:  "testdata/does-not-exist",
			sel:      hostdev.Selection{PCI: []string{"0000:01:00.0"}},
			wantErr:  "intel_iommu=on",
		},
		{
			// USB needs no IOMMU, so the same host still resolves one.
			name:     "USB is unaffected by IOMMU being off",
			fixtures: []string{"usb"},
			sysRoot:  "testdata/does-not-exist",
			sel:      hostdev.Selection{USB: []string{"0x1050:0x0407"}},
			wantUSB:  []string{"0x1050:0x0407"},
		},
		{
			name:     "an unplugged USB device is refused",
			fixtures: []string{"usb"},
			sysRoot:  sysfsRoot,
			sel:      hostdev.Selection{USB: []string{"0xdead:0xbeef"}},
			wantErr:  "no USB device 0xdead:0xbeef",
		},
		{
			name:     "an empty selection resolves to nothing on any host",
			fixtures: []string{"gpu"},
			sysRoot:  "testdata/does-not-exist",
			sel:      hostdev.Selection{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := hostdev.Build(fixtures(t, tt.fixtures...), tt.sysRoot)

			got, err := hostdev.Resolve(rep, tt.sel)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Resolve: want error containing %q, got nil", tt.wantErr)
				}
				if !errors.Is(err, hostdev.ErrUnavailable) {
					t.Errorf("Resolve error = %v, want it to wrap ErrUnavailable", err)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Resolve error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			if addrs := addressesOf(got.PCI); !equalStrings(addrs, tt.wantPCI) {
				t.Errorf("PCI = %v, want %v", addrs, tt.wantPCI)
			}
			if ids := usbIDsOf(got.USB); !equalStrings(ids, tt.wantUSB) {
				t.Errorf("USB = %v, want %v", ids, tt.wantUSB)
			}

			switch {
			case tt.wantWarning == "" && len(got.Warnings) > 0:
				t.Errorf("Warnings = %v, want none", got.Warnings)
			case tt.wantWarning != "" && !strings.Contains(strings.Join(got.Warnings, "\n"), tt.wantWarning):
				t.Errorf("Warnings = %v, want one containing %q", got.Warnings, tt.wantWarning)
			}
		})
	}
}

// equalStrings treats nil and empty as the same.
func equalStrings(a, b []string) bool {
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
