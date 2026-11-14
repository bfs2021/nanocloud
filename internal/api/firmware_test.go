package api

import (
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

// TestValidateFirmware covers combinations that would produce a VM the user
// cannot use, most of which define fine and then stop in the firmware.
func TestValidateFirmware(t *testing.T) {
	for _, tc := range []struct {
		name    string
		req     vmCreateRequest
		distro  string
		wantErr string // substring; "" means the request must be accepted
	}{
		// BIOS is the default and must stay reachable without saying anything.
		{name: "empty firmware is BIOS", distro: "ubuntu"},
		{
			name:   "explicit bios",
			req:    vmCreateRequest{Firmware: metadata.FirmwareBIOS},
			distro: "ubuntu",
		},
		{
			name:   "uefi",
			req:    vmCreateRequest{Firmware: metadata.FirmwareUEFI},
			distro: "ubuntu",
		},
		{
			name:   "uefi with secure boot on a signed distro",
			req:    vmCreateRequest{Firmware: metadata.FirmwareUEFI, SecureBoot: true},
			distro: "ubuntu",
		},
		// A TPM needs swtpm, not UEFI. Coupling them would deny Windows-style
		// setups on BIOS for no reason.
		{
			name:   "tpm without uefi",
			req:    vmCreateRequest{TPM: true},
			distro: "ubuntu",
		},
		{
			name:    "unknown firmware",
			req:     vmCreateRequest{Firmware: "coreboot"},
			distro:  "ubuntu",
			wantErr: `"firmware" must be`,
		},
		// Secure boot without UEFI is refused, not inferred or ignored.
		{
			name:    "secure boot without uefi",
			req:     vmCreateRequest{SecureBoot: true},
			distro:  "ubuntu",
			wantErr: "requires",
		},
		{
			name:    "secure boot on bios explicitly",
			req:     vmCreateRequest{Firmware: metadata.FirmwareBIOS, SecureBoot: true},
			distro:  "ubuntu",
			wantErr: "requires",
		},
		// No signed shim, so the domain defines and then halts.
		{
			name:    "secure boot on alpine",
			req:     vmCreateRequest{Firmware: metadata.FirmwareUEFI, SecureBoot: true},
			distro:  distroAlpine,
			wantErr: "signed bootloader",
		},
		// Alpine boots UEFI perfectly well; only the signature check is fatal.
		{
			name:   "uefi without secure boot on alpine",
			req:    vmCreateRequest{Firmware: metadata.FirmwareUEFI},
			distro: distroAlpine,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFirmware(tc.req, tc.distro)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("rejected a valid request: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("accepted an invalid request (want error containing %q)", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestValidateFirmware_ExplainsItself checks that each refusal gives its
// reason, since a direct API caller sees nothing else.
func TestValidateFirmware_ExplainsItself(t *testing.T) {
	err := validateFirmware(vmCreateRequest{Firmware: metadata.FirmwareUEFI, SecureBoot: true}, distroAlpine)
	if err == nil {
		t.Fatal("secure boot on Alpine was accepted")
	}
	// It must name the distro and the missing piece.
	for _, want := range []string{"Alpine", "signed bootloader"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
