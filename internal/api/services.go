package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"

	"github.com/nanocloud-io/nanocloud/internal/services"
)

// The services endpoints are a thin layer over services.Manager. A service is
// built in the background: POST /services answers 202 and the client polls.

// writeServiceError maps a services error to its status. The message is the
// manager's own, written to be read by the person who asked.
func writeServiceError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, services.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, services.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, services.ErrExists), errors.Is(err, services.ErrNotReady):
		status = http.StatusConflict
	case errors.Is(err, services.ErrUnavailable):
		status = http.StatusServiceUnavailable
	}
	http.Error(w, err.Error(), status)
}

// handleServiceCatalog handles GET /services/catalog: what can be launched.
func (s *Server) handleServiceCatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, services.Catalog())
}

// viewCtx is the request's context, marked with the host it reached this
// server at: a shared service's addresses are given with it.
func viewCtx(r *http.Request) context.Context {
	return services.ViewedFrom(r.Context(), r.Host)
}

// handleServiceList handles GET /services.
func (s *Server) handleServiceList(w http.ResponseWriter, r *http.Request) {
	list, err := s.services.List(viewCtx(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, list)
}

// handleServiceCreate handles POST /services.
func (s *Server) handleServiceCreate(w http.ResponseWriter, r *http.Request) {
	var req services.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	v, err := s.services.Create(viewCtx(r), req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusAccepted, v)
}

// handleServiceGet handles GET /services/{name}.
func (s *Server) handleServiceGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.services.Get(viewCtx(r), r.PathValue("name"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, v)
}

// handleServiceDelete handles DELETE /services/{name}: the service and every
// VM it made, disks included.
func (s *Server) handleServiceDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.services.Delete(r.Context(), r.PathValue("name")); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleServiceStart handles POST /services/{name}/start.
func (s *Server) handleServiceStart(w http.ResponseWriter, r *http.Request) {
	if err := s.services.Start(r.Context(), r.PathValue("name")); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleServiceStop handles POST /services/{name}/stop.
func (s *Server) handleServiceStop(w http.ResponseWriter, r *http.Request) {
	if err := s.services.Stop(r.Context(), r.PathValue("name")); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleServiceShare handles POST /services/{name}/share: {"shared": true}
// lets other machines reach the service, false limits it to this host.
func (s *Server) handleServiceShare(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Shared *bool `json:"shared"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Shared == nil {
		http.Error(w, `the body must be {"shared": true} or {"shared": false}`, http.StatusBadRequest)
		return
	}
	v, err := s.services.SetShared(viewCtx(r), r.PathValue("name"), *body.Shared)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, v)
}

// handleServiceRetry handles POST /services/{name}/retry: a failed service
// picks up again at the step it failed in.
func (s *Server) handleServiceRetry(w http.ResponseWriter, r *http.Request) {
	v, err := s.services.Retry(viewCtx(r), r.PathValue("name"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, v)
}

// handleServiceLog handles GET /services/{name}/logs/{vm}?lines=N: the end
// of one of the service's VMs' install log.
func (s *Server) handleServiceLog(w http.ResponseWriter, r *http.Request) {
	lines := 0
	if q := r.URL.Query().Get("lines"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 {
			http.Error(w, "lines must be a positive number", http.StatusBadRequest)
			return
		}
		lines = n
	}
	lv, err := s.services.Log(r.Context(), r.PathValue("name"), r.PathValue("vm"), lines)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	// Install logs can name what a service was set up with.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, lv)
}

// handleServiceMonitoring handles GET /services/{name}/monitoring: what a
// monitoring service watches, its alert rules and its dashboards.
func (s *Server) handleServiceMonitoring(w http.ResponseWriter, r *http.Request) {
	v, err := s.services.Monitoring(r.Context(), r.PathValue("name"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, v)
}

// handleServiceCluster handles GET /services/{name}/cluster: a Kubernetes
// service's nodes and workloads, read a few seconds ago at most.
func (s *Server) handleServiceCluster(w http.ResponseWriter, r *http.Request) {
	c, err := s.services.Cluster(r.Context(), r.PathValue("name"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, c)
}

// handleServiceAddWorkers handles POST /services/{name}/workers {"add": n}.
// The service builds the new machines in the background, as on create.
func (s *Server) handleServiceAddWorkers(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Add int `json:"add"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	v, err := s.services.AddWorkers(viewCtx(r), r.PathValue("name"), body.Add)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusAccepted, v)
}

// handleServiceRemoveWorker handles DELETE /services/{name}/workers/{vm}. The
// worker is drained, removed and deleted in the background.
func (s *Server) handleServiceRemoveWorker(w http.ResponseWriter, r *http.Request) {
	v, err := s.services.RemoveWorker(viewCtx(r), r.PathValue("name"), r.PathValue("vm"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusAccepted, v)
}

// handleServiceApply handles POST /services/{name}/apply {"manifest": "…"}:
// kubectl apply on the control plane. kubectl refusing the manifest is a
// 200 with ok false and its output.
func (s *Server) handleServiceApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Manifest string `json:"manifest"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	res, err := s.services.Apply(r.Context(), r.PathValue("name"), body.Manifest)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, res)
}

// handleServicePublish handles POST /services/{name}/published
// {"node_port": 30080, "label": "default/web", "scheme": "http"}.
func (s *Server) handleServicePublish(w http.ResponseWriter, r *http.Request) {
	var p services.Published
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	v, err := s.services.Publish(viewCtx(r), r.PathValue("name"), p)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, v)
}

// handleServiceUnpublish handles DELETE /services/{name}/published/{port}.
func (s *Server) handleServiceUnpublish(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil {
		http.Error(w, "the port is a number", http.StatusBadRequest)
		return
	}
	v, err := s.services.Unpublish(viewCtx(r), r.PathValue("name"), port)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, v)
}

// handleServiceFile handles GET /services/{name}/files/{file}: a file read
// from the service's VM, such as a kubeconfig, sent as a download.
func (s *Server) handleServiceFile(w http.ResponseWriter, r *http.Request) {
	filename, data, err := s.services.File(viewCtx(r), r.PathValue("name"), r.PathValue("file"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	// Credentials: never kept by a browser or proxy cache.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	w.Write(data) //nolint:errcheck // headers are sent; a failed write is the client's disconnect
}
