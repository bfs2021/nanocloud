package api

import (
	"net/http"

	"github.com/nanocloud-io/nanocloud/internal/install"
)

// depsResponse is the JSON shape returned by GET /system/deps.
type depsResponse struct {
	Checks []install.Check `json:"checks"`
}

// handleGetDeps handles GET /system/deps with the same host-readiness checks
// that `nanocloud install` and `nanocloud doctor` print.
func (s *Server) handleGetDeps(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, depsResponse{Checks: install.RunChecks(s.cfg.LibvirtURI)})
}
