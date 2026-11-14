package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nanocloud-io/nanocloud/internal/api"
	"github.com/nanocloud-io/nanocloud/internal/config"
	"github.com/nanocloud-io/nanocloud/internal/hypervisor"
	"github.com/nanocloud-io/nanocloud/internal/vswitch"
)

// These tests drive the API through a backend shaped like the macOS one: its
// own Capabilities, a serial console instead of VNC, and none of the optional
// libvirt-only interfaces. Methods the fake does not override panic through
// the nil embedded interface, which catches a call the API should not make.

type vzLikeBackend struct {
	hypervisor.Backend

	mu     sync.Mutex
	serial *fakeSerial
}

func (*vzLikeBackend) Capabilities() hypervisor.Capabilities {
	return hypervisor.Capabilities{Backend: hypervisor.BackendVZ, SerialConsole: true, HostStats: true}
}

func (*vzLikeBackend) HostCPUs() int               { return 8 }
func (*vzLikeBackend) Exists(string) (bool, error) { return false, nil }
func (b *vzLikeBackend) OpenSerial(name string) (io.ReadWriteCloser, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if name != "web" {
		return nil, io.ErrClosedPipe
	}
	b.serial = newFakeSerial("Ubuntu 24.04 LTS web ttyAMA0\nweb login: ")
	return b.serial, nil
}

// fakeSerial replays a banner, then records what the browser types.
type fakeSerial struct {
	r      *io.PipeReader
	w      *io.PipeWriter
	mu     sync.Mutex
	typed  bytes.Buffer
	closed chan struct{}
}

func newFakeSerial(banner string) *fakeSerial {
	r, w := io.Pipe()
	s := &fakeSerial{r: r, w: w, closed: make(chan struct{})}
	go func() { _, _ = w.Write([]byte(banner)) }()
	return s
}

func (s *fakeSerial) Read(p []byte) (int, error) { return s.r.Read(p) }
func (s *fakeSerial) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.typed.Write(p)
}
func (s *fakeSerial) Close() error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	_ = s.w.Close()
	return s.r.Close()
}
func (s *fakeSerial) Typed() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.typed.String()
}

func vzServer(t *testing.T) (*api.Server, *vzLikeBackend) {
	t.Helper()
	b := &vzLikeBackend{}
	cfg := &config.Config{StoragePools: []config.StoragePool{{Name: "default", Path: t.TempDir(), Default: true}}}
	return api.New(api.Deps{WebFS: minimalWebFS(), Cfg: cfg, VMManager: b}), b
}

func TestSystemReportsLibvirtCapabilities(t *testing.T) {
	cfg := &config.Config{StoragePools: []config.StoragePool{{Name: "default", Path: os.TempDir(), Default: true}}}
	srv := api.New(api.Deps{WebFS: minimalWebFS(), Cfg: cfg, VMManager: disconnectedManager()})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/system", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /system = %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Backend      string                  `json:"backend"`
		Capabilities hypervisor.Capabilities `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Backend != "libvirt" || body.Capabilities != hypervisor.LibvirtCapabilities() {
		t.Fatalf("GET /system backend=%q capabilities=%+v, want the libvirt set", body.Backend, body.Capabilities)
	}
}

func TestSystemReportsBackendCapabilities(t *testing.T) {
	srv, _ := vzServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/system", nil))
	if !strings.Contains(rec.Body.String(), `"backend":"vz"`) || !strings.Contains(rec.Body.String(), `"serial_console":true`) {
		t.Fatalf("GET /system = %s, want backend vz with a serial console", rec.Body)
	}
}

func TestSnapshotsUnsupportedIs501(t *testing.T) {
	srv, _ := vzServer(t)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/vms/web/snapshots", nil),
		httptest.NewRequest(http.MethodPost, "/vms/web/snapshots", strings.NewReader(`{"name":"s1"}`)),
		httptest.NewRequest(http.MethodDelete, "/vms/web/snapshots/s1", nil),
		httptest.NewRequest(http.MethodPost, "/vms/web/snapshots/s1/restore", nil),
	} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s = %d, want 501: %s", req.Method, req.URL.Path, rec.Code, rec.Body)
		}
	}
}

func TestDevicesWithoutPassthroughBackend(t *testing.T) {
	srv, _ := vzServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/system/devices", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /system/devices = %d: %s", rec.Code, rec.Body)
	}
	var rep struct {
		IOMMU struct {
			Enabled bool   `json:"enabled"`
			Message string `json:"message"`
			Remedy  string `json:"remedy"`
		} `json:"iommu"`
		PCI []json.RawMessage `json:"pci"`
		USB []json.RawMessage `json:"usb"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.IOMMU.Enabled || rep.IOMMU.Remedy != "" || !strings.Contains(rep.IOMMU.Message, "vz") {
		t.Errorf("iommu = %+v, want disabled, no kernel remedy, naming the backend", rep.IOMMU)
	}
	if rep.PCI == nil || rep.USB == nil || len(rep.PCI)+len(rep.USB) != 0 {
		t.Errorf("devices = pci %v usb %v, want two empty lists", rep.PCI, rep.USB)
	}
}

func TestCreateRefusesUnsupportedFeatures(t *testing.T) {
	cases := map[string]string{
		"tpm":         `"tpm":true`,
		"secure boot": `"firmware":"uefi","secure_boot":true`,
		"cpu pinning": `"cpu":{"cpu_set":"0-1"}`,
		"cpu model":   `"cpu":{"mode":"host-passthrough"}`,
		"topology":    `"cpu":{"topology":{"sockets":1,"cores":2,"threads":1}}`,
		"passthrough": `"pci_devices":["0000:01:00.0"]`,
		"bridge":      `"network":{"mode":"bridge"}`,
		"legacy mode": `"network_mode":"bridge"`,
		"vlan":        `"network":{"mode":"bridge","vlan_id":20}`,
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			// Reaching the backend's Create would panic on the nil embedded
			// interface, so a 422 here also proves nothing was attempted.
			srv, _ := vzServer(t)
			body := `{"name":"web","image_id":"ubuntu-2404-standard","vcpus":2,"memory_mb":2048,"disk_gb":20,` + extra + `}`
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/vms", strings.NewReader(body)))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("POST /vms with %s = %d, want 422: %s", name, rec.Code, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), "vz backend does not support") {
				t.Errorf("body = %q, want it to name the backend and the feature", rec.Body)
			}
		})
	}
}

func TestSerialConsoleProxy(t *testing.T) {
	srv, b := vzServer(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/vms/web/console", nil)
	if err != nil {
		t.Fatalf("dial console: %v", err)
	}
	defer c.CloseNow() //nolint:errcheck

	var got strings.Builder
	for !strings.Contains(got.String(), "login: ") {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read console: %v (so far %q)", err, got.String())
		}
		got.Write(data)
	}

	if err := c.Write(ctx, websocket.MessageBinary, []byte("root\n")); err != nil {
		t.Fatalf("write console: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		b.mu.Lock()
		s := b.serial
		b.mu.Unlock()
		if s != nil && s.Typed() == "root\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("keystrokes never reached the guest's serial port")
		}
		time.Sleep(10 * time.Millisecond)
	}

	_ = c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-b.serial.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("serial stream not closed after the browser disconnected")
	}
}

func TestSerialConsoleNotRunningIs409(t *testing.T) {
	srv, _ := vzServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/vms/other/console", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("GET console of a stopped VM = %d, want 409", rec.Code)
	}
}

// switchBackend is a vz-like backend whose VMs are on nano-cloud's switch.
type switchBackend struct {
	vzLikeBackend
	rep vswitch.Report
	err error
}

func (b *switchBackend) SwitchStatus() (vswitch.Report, error) { return b.rep, b.err }

func TestNetworkSwitch(t *testing.T) {
	cfg := &config.Config{StoragePools: []config.StoragePool{{Name: "default", Path: os.TempDir(), Default: true}}}
	get := func(t *testing.T, backend hypervisor.Backend) *httptest.ResponseRecorder {
		t.Helper()
		srv := api.New(api.Deps{WebFS: minimalWebFS(), Cfg: cfg, VMManager: backend})
		return serve(srv, http.MethodGet, "/network/switch", "")
	}
	report := vswitch.Report{Version: "v1", Status: vswitch.Status{Ports: []vswitch.PortStatus{{Name: "web", MAC: "52:54:00:00:00:01"}}}}

	t.Run("reports the switch and its ports", func(t *testing.T) {
		rec := get(t, &switchBackend{rep: report})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"web"`) {
			t.Fatalf("= %d %s, want 200 naming port web", rec.Code, rec.Body)
		}
	})
	t.Run("is 503 when the switch is not answering", func(t *testing.T) {
		if rec := get(t, &switchBackend{err: io.ErrUnexpectedEOF}); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("= %d %s, want 503", rec.Code, rec.Body)
		}
	})
	t.Run("is 404 on a Mac set to NAT", func(t *testing.T) {
		if rec := get(t, &switchBackend{err: vswitch.ErrNoSwitch}); rec.Code != http.StatusNotFound {
			t.Fatalf("= %d %s, want 404", rec.Code, rec.Body)
		}
	})
	t.Run("is 404 on libvirt", func(t *testing.T) {
		if rec := get(t, disconnectedManager()); rec.Code != http.StatusNotFound {
			t.Fatalf("= %d %s, want 404", rec.Code, rec.Body)
		}
	})
}
