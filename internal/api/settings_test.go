package api_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/api"
	"github.com/nanocloud-io/nanocloud/internal/config"
	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/reconcile"
	"github.com/nanocloud-io/nanocloud/internal/settings"
	"github.com/nanocloud-io/nanocloud/internal/storage"
	"github.com/nanocloud-io/nanocloud/internal/update"
)

type settingsFixture struct {
	srv  http.Handler
	loop *reconcile.Loop
	meta *metadata.Store
	dir  string
}

func newSettingsFixture(t *testing.T) settingsFixture {
	t.Helper()
	dir := t.TempDir()
	st, err := settings.Open(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	meta := metadata.New(filepath.Join(dir, "metadata.json"))
	loop := reconcile.New(nil, meta, nil, nil)
	cfg := &config.Config{StoragePools: []config.StoragePool{
		{Name: "default", Path: filepath.Join(dir, "vm-disks"), Default: true},
	}}
	srv := api.New(api.Deps{WebFS: minimalWebFS(), Cfg: cfg, MetaStore: meta, Reconcile: loop, Settings: st,
		File: api.FileSettings{ConfigPath: "/etc/nano-cloud/config.yaml"}})
	return settingsFixture{srv: srv, loop: loop, meta: meta, dir: dir}
}

type settingsBody struct {
	SelfHealing bool `json:"self_healing"`
	Pools       []struct {
		Name      string `json:"name"`
		Default   bool   `json:"default"`
		Removable bool   `json:"removable"`
	} `json:"pools"`
	File struct {
		ConfigPath string `json:"config_path"`
	} `json:"file"`
}

func decodeSettings(t *testing.T, body string) settingsBody {
	t.Helper()
	var b settingsBody
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return b
}

func defaultPool(b settingsBody) string {
	for _, p := range b.Pools {
		if p.Default {
			return p.Name
		}
	}
	return ""
}

func TestSettings_SelfHealingSwitchesTheLoop(t *testing.T) {
	f := newSettingsFixture(t)
	rec := serve(f.srv, http.MethodGet, "/settings", "")
	if b := decodeSettings(t, rec.Body.String()); rec.Code != http.StatusOK || !b.SelfHealing || b.File.ConfigPath == "" {
		t.Fatalf("GET /settings = %d %s", rec.Code, rec.Body)
	}

	rec = serve(f.srv, http.MethodPatch, "/settings", `{"self_healing": false}`)
	if rec.Code != http.StatusOK || decodeSettings(t, rec.Body.String()).SelfHealing || f.loop.Enabled() {
		t.Fatalf("PATCH off = %d %s, loop enabled %v", rec.Code, rec.Body, f.loop.Enabled())
	}
	// null hands the choice back to config.yaml, which leaves it on.
	rec = serve(f.srv, http.MethodPatch, "/settings", `{"self_healing": null}`)
	if rec.Code != http.StatusOK || !f.loop.Enabled() {
		t.Fatalf("PATCH null = %d %s, loop enabled %v", rec.Code, rec.Body, f.loop.Enabled())
	}

	for body, want := range map[string]int{
		`{"self_healing": "yes"}`:  http.StatusBadRequest,
		`{"port": 9090}`:           http.StatusBadRequest, // only config.yaml changes the port
		`{"default_pool": "nope"}`: http.StatusBadRequest,
	} {
		if rec = serve(f.srv, http.MethodPatch, "/settings", body); rec.Code != want {
			t.Errorf("PATCH %s = %d %s, want %d", body, rec.Code, rec.Body, want)
		}
	}
}

func TestSettings_AddedPoolIsUsableAndRemovable(t *testing.T) {
	f := newSettingsFixture(t)
	big := filepath.Join(f.dir, "big")

	rec := serve(f.srv, http.MethodPost, "/settings/pools", `{"name":"big","path":"`+big+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", rec.Code, rec.Body)
	}
	if fi, err := os.Stat(big); err != nil || !fi.IsDir() {
		t.Errorf("the pool directory was not made: %v", err)
	}
	rec = serve(f.srv, http.MethodPatch, "/settings", `{"default_pool":"big"}`)
	if b := decodeSettings(t, rec.Body.String()); rec.Code != http.StatusOK || defaultPool(b) != "big" {
		t.Fatalf("make default = %d %s", rec.Code, rec.Body)
	}
	// The wizard and VM create read pools from here, not from config.yaml.
	if rec = serve(f.srv, http.MethodGet, "/storage/pools", ""); !strings.Contains(rec.Body.String(), `"name":"big","path":"`+big+`","default":true`) {
		t.Errorf("GET /storage/pools does not offer big as the default: %s", rec.Body)
	}

	for body, want := range map[string]int{
		`{"name":"big","path":"/elsewhere"}`:                             http.StatusConflict,
		`{"name":"again","path":"` + big + `/"}`:                         http.StatusConflict, // same path, spelled differently
		`{"name":"rel","path":"relative/dir"}`:                           http.StatusBadRequest,
		`{"name":"root","path":"/"}`:                                     http.StatusBadRequest,
		`{"name":"../x","path":"/srv/x"}`:                                http.StatusBadRequest,
		`{"name":"ro","path":"` + filepath.Join(f.dir, "ro", "x") + `"}`: http.StatusUnprocessableEntity,
	} {
		if strings.Contains(body, `"ro"`) {
			if err := os.Mkdir(filepath.Join(f.dir, "ro"), 0o500); err != nil {
				t.Fatal(err)
			}
		}
		if rec = serve(f.srv, http.MethodPost, "/settings/pools", body); rec.Code != want {
			t.Errorf("add %s = %d %s, want %d", body, rec.Code, rec.Body, want)
		}
	}

	if err := f.meta.Upsert("web", func(e *metadata.VMEntry) { e.PoolPath = big }); err != nil {
		t.Fatal(err)
	}
	if rec = serve(f.srv, http.MethodDelete, "/settings/pools/big", ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "web") {
		t.Errorf("removing a pool holding %s = %d %s, want 409 naming web", storage.DiskPathFor(big, "web"), rec.Code, rec.Body)
	}
	if err := f.meta.Delete("web"); err != nil {
		t.Fatal(err)
	}
	rec = serve(f.srv, http.MethodDelete, "/settings/pools/big", "")
	if b := decodeSettings(t, rec.Body.String()); rec.Code != http.StatusOK || len(b.Pools) != 1 || defaultPool(b) != "default" {
		t.Errorf("remove = %d %s, want one pool left and the default back on config.yaml's", rec.Code, rec.Body)
	}
	if rec = serve(f.srv, http.MethodDelete, "/settings/pools/default", ""); rec.Code != http.StatusConflict {
		t.Errorf("removing config.yaml's pool = %d, want 409", rec.Code)
	}
}

// Turning the check off in Settings stops the daemon asking GitHub even when
// config.yaml leaves it on.
func TestSettings_UpdateCheckSwitch(t *testing.T) {
	st, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := api.New(api.Deps{WebFS: minimalWebFS(), Cfg: &config.Config{}, Settings: st, Updates: update.NewChecker("v0.1.0")})

	rec := serve(srv, http.MethodPatch, "/settings", `{"update_check": false}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"update_check":false`) {
		t.Fatalf("PATCH update_check = %d %s", rec.Code, rec.Body)
	}
	var status update.Status
	if err := json.Unmarshal(serve(srv, http.MethodGet, "/system/update", "").Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Disabled || !status.CheckedAt.IsZero() {
		t.Errorf("GET /system/update after switching the check off = %+v, want disabled and never checked", status)
	}
}
