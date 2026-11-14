package api_test

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/api"
	"github.com/nanocloud-io/nanocloud/internal/config"
)

type depCheck struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Required bool   `json:"required"`
	Message  string `json:"message"`
	Remedy   string `json:"remedy"`
}

// getDeps fetches GET /system/deps, failing unless it is a 200 JSON response.
func getDeps(t *testing.T) []depCheck {
	t.Helper()
	cfg := &config.Config{
		StoragePools: []config.StoragePool{{Name: "default", Path: os.TempDir(), Default: true}},
	}
	rec := serve(api.New(api.Deps{WebFS: minimalWebFS(), Cfg: cfg}), http.MethodGet, "/system/deps", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /system/deps: status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body struct {
		Checks []depCheck `json:"checks"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body.Checks
}

// TestHandleGetDeps_Structure checks that every check has a name and a
// message; ok and required vary by host.
func TestHandleGetDeps_Structure(t *testing.T) {
	checks := getDeps(t)
	if len(checks) == 0 {
		t.Fatal("checks array is empty, expected at least one entry")
	}
	for i, c := range checks {
		if c.Name == "" {
			t.Errorf("checks[%d].name is empty", i)
		}
		if c.Message == "" {
			t.Errorf("checks[%d].message is empty (name=%q)", i, c.Name)
		}
	}
}

// TestHandleGetDeps_KnownEntries checks that every dependency documented for
// this platform is reported.
func TestHandleGetDeps_KnownEntries(t *testing.T) {
	required := map[string]bool{}
	for _, c := range getDeps(t) {
		required[c.Name] = c.Required
	}
	names := []string{"xorriso", "ip", "virsh"}
	if runtime.GOOS == "darwin" {
		names = []string{"macos", "virtualization", "entitlement", "hdiutil"}
	}
	for _, name := range names {
		if req, found := required[name]; !found {
			t.Errorf("check %q missing from response", name)
		} else if !req {
			t.Errorf("check %q: required = false, want true", name)
		}
	}
}

// TestHandleInstallDepRouteRemoved checks that the API cannot install a
// dependency: POST /system/deps/{name}/install falls through to the SPA.
func TestHandleInstallDepRouteRemoved(t *testing.T) {
	cfg := &config.Config{
		StoragePools: []config.StoragePool{{Name: "default", Path: os.TempDir(), Default: true}},
	}
	rec := serve(api.New(api.Deps{WebFS: minimalWebFS(), Cfg: cfg}), http.MethodPost, "/system/deps/xorriso/install", "")

	// No handler answers, so there is no JSON error body.
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err == nil && body.Error != "" {
		t.Errorf("expected the install endpoint to be gone, but got JSON error body: %q", body.Error)
	}
}
