package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/api"
	"github.com/nanocloud-io/nanocloud/internal/config"
	"github.com/nanocloud-io/nanocloud/internal/update"
	"github.com/nanocloud-io/nanocloud/internal/version"
)

// `nanocloud update` reads the running version from /health, the one
// endpoint that needs no login.
func TestHealthReportsTheVersion(t *testing.T) {
	rec := serve(api.New(api.Deps{WebFS: minimalWebFS(), Cfg: &config.Config{}}), http.MethodGet, "/health", "")
	var body struct{ Status, Version string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Status != "ok" || body.Version != version.Version {
		t.Errorf("GET /health = %s (%v)", rec.Body, err)
	}
}

// With the check turned off the daemon must not ask GitHub at all, so the
// answer comes back without a Latest and marked disabled.
func TestUpdateCheckCanBeTurnedOff(t *testing.T) {
	off := false
	cfg := &config.Config{UpdateCheck: &off}
	srv := api.New(api.Deps{WebFS: minimalWebFS(), Cfg: cfg, Updates: update.NewChecker("v0.1.0")})
	var st update.Status
	if err := json.Unmarshal(serve(srv, http.MethodGet, "/system/update", "").Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Disabled || st.Latest != nil || !st.CheckedAt.IsZero() {
		t.Errorf("GET /system/update with update_check: false = %+v", st)
	}
}
