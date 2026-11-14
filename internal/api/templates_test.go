package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

type templateBody struct {
	Name          string `json:"name"`
	CreatedAtUnix int64  `json:"created_at_unix"`
	SourceVM      string `json:"source_vm"`
	Spec          struct {
		ImageID     string   `json:"image_id"`
		VCPUs       int      `json:"vcpus"`
		MemoryMB    int      `json:"memory_mb"`
		NetworkMode string   `json:"network_mode"`
		USBDevices  []string `json:"usb_devices"`
		CPU         struct {
			Mode string `json:"mode"`
		} `json:"cpu"`
		CloudInit struct {
			Username string   `json:"username"`
			Packages []string `json:"packages"`
			SSHKeys  []string `json:"ssh_keys"`
		} `json:"cloud_init"`
	} `json:"spec"`
}

// tmplReq issues a template request and returns the raw recorder.
func tmplReq(t *testing.T, f specFixture, method, path, body string, wantStatus int) *httptest.ResponseRecorder {
	t.Helper()

	rec := serve(f.srv, method, path, body)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s: status = %d, want %d (body: %s)", method, path, rec.Code, wantStatus, rec.Body)
	}
	return rec
}

// A template is captured from a VM and comes back shaped like a create request.
func TestTemplates_CaptureListDelete(t *testing.T) {
	const name = "template-source"
	f := newSpecFixture(t, name)

	if err := f.store.Update(name, func(e *metadata.VMEntry) {
		e.ImageID, e.CloudInitUser = "ubuntu-24.04", "alex"
		e.USBDevices = []metadata.USBDevice{{VendorID: "0x1050", ProductID: "0x0407"}}
		e.CloudInit = &metadata.CloudInitSpec{Packages: []string{"nginx"}, SSHKeys: []string{"ssh-ed25519 AAAA"}}
	}); err != nil {
		t.Fatalf("seed the source's spec: %v", err)
	}

	rec := tmplReq(t, f, http.MethodPost, "/templates", `{"name":"web server","from_vm":"`+name+`"}`, http.StatusCreated)
	var created templateBody
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode POST /templates: %v (body: %s)", err, rec.Body)
	}
	if created.Name != "web server" || created.SourceVM != name || created.CreatedAtUnix == 0 {
		t.Errorf("template = %+v, want its name, its source and a creation time", created)
	}
	if created.Spec.ImageID != "ubuntu-24.04" || created.Spec.VCPUs != 1 || created.Spec.MemoryMB != 512 {
		t.Errorf("spec = %+v, want the source's image and resources", created.Spec)
	}
	// Devices come back in the form the create request takes, not the stored one.
	if len(created.Spec.USBDevices) != 1 || created.Spec.USBDevices[0] != "0x1050:0x0407" {
		t.Errorf("usb_devices = %v, want the create request's vendor:product form", created.Spec.USBDevices)
	}
	// host-model is stored as empty and reported as itself.
	if created.Spec.CPU.Mode != metadata.CPUHostModel {
		t.Errorf("cpu.mode = %q, want the resolved default %q", created.Spec.CPU.Mode, metadata.CPUHostModel)
	}
	if created.Spec.CloudInit.Username != "alex" || len(created.Spec.CloudInit.SSHKeys) != 1 ||
		len(created.Spec.CloudInit.Packages) != 1 {
		t.Errorf("cloud_init = %+v, want the user, keys and packages carried over", created.Spec.CloudInit)
	}

	rec = tmplReq(t, f, http.MethodGet, "/templates", "", http.StatusOK)
	var list []templateBody
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode GET /templates: %v (body: %s)", err, rec.Body)
	}
	if len(list) != 1 || list[0].Name != "web server" {
		t.Fatalf("GET /templates = %+v, want the one template", list)
	}

	tmplReq(t, f, http.MethodDelete, "/templates/web%20server", "", http.StatusNoContent)
	rec = tmplReq(t, f, http.MethodGet, "/templates", "", http.StatusOK)
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("after delete, GET /templates = %s, want an empty array", got)
	}
}

func TestTemplates_Refusals(t *testing.T) {
	const name = "template-refusals"
	f := newSpecFixture(t, name)

	tmplReq(t, f, http.MethodPost, "/templates", `{"name":"","from_vm":"`+name+`"}`, http.StatusBadRequest)
	tmplReq(t, f, http.MethodPost, "/templates", `{"name":"ok"}`, http.StatusBadRequest)
	// A VM nano-cloud never created has no spec to capture.
	tmplReq(t, f, http.MethodPost, "/templates", `{"name":"ok","from_vm":"not-a-vm"}`, http.StatusNotFound)

	tmplReq(t, f, http.MethodPost, "/templates", `{"name":"taken","from_vm":"`+name+`"}`, http.StatusCreated)
	// An existing template is not overwritten.
	tmplReq(t, f, http.MethodPost, "/templates", `{"name":"taken","from_vm":"`+name+`"}`, http.StatusConflict)

	tmplReq(t, f, http.MethodDelete, "/templates/never-saved", "", http.StatusNotFound)
}
