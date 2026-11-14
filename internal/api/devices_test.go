package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/api"
	"github.com/nanocloud-io/nanocloud/internal/config"
)

// devicesResponse mirrors the parts of GET /system/devices this test asserts.
// The device lists stay raw: on test:///default they are empty, so only their
// array shape is checked.
type devicesResponse struct {
	IOMMU struct {
		Enabled bool   `json:"enabled"`
		Message string `json:"message"`
		Remedy  string `json:"remedy"`
	} `json:"iommu"`
	PCI []json.RawMessage `json:"pci"`
	USB []json.RawMessage `json:"usb"`
}

// TestHandleGetDevices_Contract checks the endpoint against the test driver,
// which has no PCI or USB devices: the iommu block must still be filled in and
// the empty lists must be arrays.
func TestHandleGetDevices_Contract(t *testing.T) {
	m := libvirtManager(t)

	srv := api.New(api.Deps{WebFS: minimalWebFS(), Cfg: &config.Config{}, VMManager: m})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/system/devices", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /system/devices: status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	body := rec.Body.String()
	var got devicesResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.PCI == nil {
		t.Errorf("pci decoded as null, want []: %s", body)
	}
	if got.USB == nil {
		t.Errorf("usb decoded as null, want []: %s", body)
	}
	if got.IOMMU.Message == "" {
		t.Errorf("iommu.message is empty; the readiness block must be populated even with no devices: %s", body)
	}
	// This machine's IOMMU state is unknown, but the two fields have to agree:
	// a remedy is required when it is off and pointless when it is on.
	if hasRemedy := got.IOMMU.Remedy != ""; hasRemedy == got.IOMMU.Enabled {
		t.Errorf("iommu.remedy = %q with enabled = %v", got.IOMMU.Remedy, got.IOMMU.Enabled)
	}
}

// TestHandleGetDevices_LibvirtUnreachable pins the one failure mode that is
// not a readiness state: no libvirt, no inventory, 503.
func TestHandleGetDevices_LibvirtUnreachable(t *testing.T) {
	srv := api.New(api.Deps{WebFS: minimalWebFS(), Cfg: &config.Config{}, VMManager: disconnectedManager()})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/system/devices", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /system/devices with libvirt down: status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
