package api_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/api"
	"github.com/nanocloud-io/nanocloud/internal/config"
	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/services"
)

// newServicesServer is a server on libvirt's test driver with a services
// manager attached. The test driver ships one domain, named "test".
func newServicesServer(t *testing.T) http.Handler {
	t.Helper()
	m := libvirtManager(t)

	dir := t.TempDir()
	mgr, err := services.New(services.NewStore(filepath.Join(dir, "services.json")), nil, filepath.Join(dir, "service_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{StoragePools: []config.StoragePool{{Name: "default", Path: t.TempDir(), Default: true}}}
	return api.New(api.Deps{
		WebFS: minimalWebFS(), Cfg: cfg, VMManager: m,
		MetaStore:  metadata.New(filepath.Join(dir, "metadata.json")),
		ImgManager: cachedImageManager(t),
		Services:   mgr,
	})
}

func TestServiceCatalog_ListsRecipesWithoutTheirScripts(t *testing.T) {
	srv := api.New(api.Deps{WebFS: minimalWebFS(), Cfg: &config.Config{}})
	rec := serve(srv, http.MethodGet, "/services/catalog", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /services/catalog = %d: %s", rec.Code, rec.Body)
	}
	var recipes []struct {
		ID    string `json:"id"`
		Sizes []struct {
			ID string `json:"id"`
		} `json:"sizes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &recipes); err != nil {
		t.Fatal(err)
	}
	if len(recipes) != 6 || len(recipes[0].Sizes) == 0 {
		t.Fatalf("catalog = %+v", recipes)
	}
	// The install scripts and output templates describe every secret.
	for _, leak := range []string{"apt-get", "{{", "install"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("catalog JSON contains %q", leak)
		}
	}
	// The launch drawer iterates params, so a recipe without settings must
	// still send a list.
	if strings.Contains(rec.Body.String(), "null") {
		t.Errorf("catalog JSON has a null where clients expect a list: %s", rec.Body)
	}
}

func TestServices_UnavailableWithoutAManager(t *testing.T) {
	srv := api.New(api.Deps{WebFS: minimalWebFS(), Cfg: &config.Config{}})
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/services"},
		{http.MethodPost, "/services"},
		{http.MethodGet, "/services/db"},
		{http.MethodDelete, "/services/db"},
		{http.MethodGet, "/services/k8s/files/kubeconfig"},
	} {
		if rec := serve(srv, r.method, r.path, "{}"); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503", r.method, r.path, rec.Code)
		}
	}
}

func TestServices_StatusCodes(t *testing.T) {
	srv := newServicesServer(t)
	for name, tc := range map[string]struct {
		method, path, body string
		want               int
		says               string
	}{
		"empty list":       {http.MethodGet, "/services", "", http.StatusOK, "[]"},
		"bad body":         {http.MethodPost, "/services", "{", http.StatusBadRequest, "invalid request body"},
		"bad name":         {http.MethodPost, "/services", `{"name":"Bad Name","recipe":"postgres"}`, http.StatusBadRequest, "lowercase"},
		"unknown recipe":   {http.MethodPost, "/services", `{"name":"x","recipe":"redis"}`, http.StatusBadRequest, `unknown service "redis"`},
		"bad setting":      {http.MethodPost, "/services", `{"name":"x","recipe":"postgres","params":{"user":"a b"}}`, http.StatusBadRequest, "User"},
		"VM name taken":    {http.MethodPost, "/services", `{"name":"test","recipe":"postgres"}`, http.StatusConflict, `a VM named "test" already exists`},
		"get unknown":      {http.MethodGet, "/services/nope", "", http.StatusNotFound, "nope"},
		"delete unknown":   {http.MethodDelete, "/services/nope", "", http.StatusNotFound, "nope"},
		"start unknown":    {http.MethodPost, "/services/nope/start", "", http.StatusNotFound, "nope"},
		"stop unknown":     {http.MethodPost, "/services/nope/stop", "", http.StatusNotFound, "nope"},
		"file of unknown":  {http.MethodGet, "/services/nope/files/kubeconfig", "", http.StatusNotFound, "nope"},
		"share unknown":    {http.MethodPost, "/services/nope/share", `{"shared":true}`, http.StatusNotFound, "nope"},
		"share no value":   {http.MethodPost, "/services/nope/share", `{}`, http.StatusBadRequest, `{"shared": true}`},
		"retry unknown":    {http.MethodPost, "/services/nope/retry", "", http.StatusNotFound, "nope"},
		"log of unknown":   {http.MethodGet, "/services/nope/logs/nope", "", http.StatusNotFound, "nope"},
		"log bad lines":    {http.MethodGet, "/services/nope/logs/nope?lines=x", "", http.StatusBadRequest, "lines"},
		"cluster unknown":  {http.MethodGet, "/services/nope/cluster", "", http.StatusNotFound, "nope"},
		"monitor unknown":  {http.MethodGet, "/services/nope/monitoring", "", http.StatusNotFound, "nope"},
		"workers unknown":  {http.MethodPost, "/services/nope/workers", `{"add":1}`, http.StatusNotFound, "nope"},
		"remove unknown":   {http.MethodDelete, "/services/nope/workers/x", "", http.StatusNotFound, "nope"},
		"apply unknown":    {http.MethodPost, "/services/nope/apply", `{"manifest":"x"}`, http.StatusNotFound, "nope"},
		"publish unknown":  {http.MethodPost, "/services/nope/published", `{"node_port":30080}`, http.StatusNotFound, "nope"},
		"unpublish NaN":    {http.MethodDelete, "/services/nope/published/x", "", http.StatusBadRequest, "number"},
		"catalog is fixed": {http.MethodGet, "/services/catalog", "", http.StatusOK, "kubernetes"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := serve(srv, tc.method, tc.path, tc.body)
			if rec.Code != tc.want || !strings.Contains(rec.Body.String(), tc.says) {
				t.Errorf("%s %s = %d %q, want %d saying %q", tc.method, tc.path, rec.Code, rec.Body, tc.want, tc.says)
			}
			// A message the UI shows as is: no package prefix.
			if strings.Contains(rec.Body.String(), "services:") {
				t.Errorf("message leaks a package prefix: %q", rec.Body)
			}
		})
	}
}

// The service label rides on POST /vms into the spec and back out of GET /vms,
// which is how a VM card says it belongs to a service.
func TestHandleVMCreate_RecordsTheServiceLabel(t *testing.T) {
	needCloneTools(t)
	srv := newTestServerWithProvisioner(t, nil)

	body := `{"name":"db","image_id":"` + testImageID + `","vcpus":1,"memory_mb":512,"disk_gb":1,
	          "service":"db","cloud_init":{"username":"nanocloud","password":"pw",
	            "ssh_public_key":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexample nano-cloud",
	            "write_files":[{"path":"/usr/local/sbin/install","content":"PASSWORD=TopSecret42"}],
	            "runcmd":["/usr/local/sbin/install"]}}`
	if rec := doJSON(t, srv, http.MethodPost, "/vms", body); rec.Code != http.StatusCreated {
		t.Fatalf("POST /vms = %d: %s", rec.Code, rec.Body)
	}
	t.Cleanup(func() { srv.mgr.Destroy("db"); srv.mgr.Delete("db") }) //nolint:errcheck // best-effort cleanup in test

	e, err := srv.meta.Get("db")
	if err != nil {
		t.Fatal(err)
	}
	if e.Service != "db" {
		t.Errorf("spec service = %q, want db", e.Service)
	}
	// The install script holds the service's secrets; metadata.json is
	// world-readable, so it keeps the keys and nothing else.
	if e.CloudInit == nil || len(e.CloudInit.SSHKeys) != 1 || len(e.CloudInit.WriteFiles) != 0 || len(e.CloudInit.RunCmd) != 0 {
		t.Errorf("service VM cloud-init spec = %+v, want only its SSH key", e.CloudInit)
	}

	rec := doJSON(t, srv, http.MethodGet, "/vms", "")
	var vms []struct {
		ID      string `json:"id"`
		Service string `json:"service"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &vms); err != nil {
		t.Fatal(err)
	}
	for _, v := range vms {
		want := ""
		if v.ID == "db" {
			want = "db"
		}
		if v.Service != want {
			t.Errorf("GET /vms %s service = %q, want %q", v.ID, v.Service, want)
		}
	}
}
