package api_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/api"
	"github.com/nanocloud-io/nanocloud/internal/config"
	"github.com/nanocloud-io/nanocloud/internal/template"
)

// The padding is legal JSON whitespace, so without the cap the body decodes
// and the handler rejects it for a different reason.
func TestRequestBodyIsCapped(t *testing.T) {
	cfg := &config.Config{StoragePools: []config.StoragePool{{Name: "default", Path: os.TempDir(), Default: true}}}
	srv := api.New(api.Deps{WebFS: minimalWebFS(), Cfg: cfg, Templates: template.New(filepath.Join(t.TempDir(), "templates.json"))})

	body := `{"name":"ok"` + strings.Repeat(" ", 2<<20) + `}`
	rec := serve(srv, http.MethodPost, "/templates", body)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid request body") {
		t.Errorf("2 MiB body: %d %q, want 400 invalid request body", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	rec = serve(srv, http.MethodPost, "/templates", `{"name":"ok"}`)
	if strings.Contains(rec.Body.String(), "invalid request body") {
		t.Errorf("small body was refused: %d %q", rec.Code, rec.Body.String())
	}
}
