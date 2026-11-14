package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

// vmAPI drives the daemon's own HTTP API in-process, so a service's VM goes
// through the same POST /vms as any other, with its validation, rollback and
// metadata.
type vmAPI struct {
	h http.Handler
}

// apiError is a non-2xx answer, carrying the handler's message.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string { return e.Message }

func (a *vmAPI) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	if rec.Code < 200 || rec.Code > 299 {
		return &apiError{Status: rec.Code, Message: strings.TrimSpace(rec.Body.String())}
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			return fmt.Errorf("decode %s %s: %w", method, path, err)
		}
	}
	return nil
}

// catalogImage is the part of GET /images/catalog the manager reads.
type catalogImage struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
}

func (a *vmAPI) catalog(ctx context.Context) ([]catalogImage, error) {
	var out []catalogImage
	err := a.do(ctx, http.MethodGet, "/images/catalog", nil, &out)
	return out, err
}

func (a *vmAPI) pull(ctx context.Context, id string) error {
	return a.do(ctx, http.MethodPost, "/images/pull", map[string]string{"id": id}, nil)
}

type pullStatus struct {
	Done  bool   `json:"done"`
	Error string `json:"error"`
}

func (a *vmAPI) pullStatus(ctx context.Context, id string) (pullStatus, error) {
	var out pullStatus
	err := a.do(ctx, http.MethodGet, "/images/"+url.PathEscape(id)+"/status", nil, &out)
	return out, err
}

// vmView is the part of GET /vms the manager reads.
type vmView struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Phase  string `json:"phase"`
	IP     string `json:"ip"`
	Uptime int64  `json:"uptime_seconds"`
}

func (a *vmAPI) vms(ctx context.Context) (map[string]vmView, error) {
	var list []vmView
	if err := a.do(ctx, http.MethodGet, "/vms", nil, &list); err != nil {
		return nil, err
	}
	out := make(map[string]vmView, len(list))
	for _, v := range list {
		out[v.ID] = v
	}
	return out, nil
}

// vmCreate is the POST /vms body the manager sends.
type vmCreate struct {
	Name      string        `json:"name"`
	ImageID   string        `json:"image_id"`
	VCPUs     int           `json:"vcpus"`
	MemoryMB  int           `json:"memory_mb"`
	DiskGB    int           `json:"disk_gb"`
	CloudInit vmCreateCloud `json:"cloud_init"`
	Service   string        `json:"service"`
}

type vmCreateCloud struct {
	Username     string         `json:"username"`
	Password     string         `json:"password"`
	SSHPublicKey string         `json:"ssh_public_key"`
	GitHubUser   string         `json:"github_user,omitempty"`
	RunCmd       []string       `json:"runcmd"`
	WriteFiles   []vmCreateFile `json:"write_files"`
}

type vmCreateFile struct {
	Path        string `json:"path"`
	Content     string `json:"content"`
	Permissions string `json:"permissions,omitempty"`
}

func (a *vmAPI) createVM(ctx context.Context, req vmCreate) error {
	return a.do(ctx, http.MethodPost, "/vms", req, nil)
}

func (a *vmAPI) startVM(ctx context.Context, name string) error {
	return a.do(ctx, http.MethodPost, "/vms/"+url.PathEscape(name)+"/start", nil, nil)
}

func (a *vmAPI) stopVM(ctx context.Context, name string) error {
	return a.do(ctx, http.MethodPost, "/vms/"+url.PathEscape(name)+"/stop", nil, nil)
}

func (a *vmAPI) deleteVM(ctx context.Context, name string) error {
	return a.do(ctx, http.MethodDelete, "/vms/"+url.PathEscape(name), nil, nil)
}
