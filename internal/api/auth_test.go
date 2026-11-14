package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/api"
	"github.com/nanocloud-io/nanocloud/internal/auth"
	"github.com/nanocloud-io/nanocloud/internal/config"
	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/services"
	"github.com/nanocloud-io/nanocloud/internal/template"
)

const testPassword = "correct horse"

func newAuthServer(t *testing.T, password string) (http.Handler, *auth.Store) {
	t.Helper()
	store := auth.New(filepath.Join(t.TempDir(), "auth.json"))
	if password != "" {
		if err := store.SetPassword(password); err != nil {
			t.Fatal(err)
		}
	}
	srv := api.New(api.Deps{
		WebFS: minimalWebFS(), Cfg: &config.Config{},
		Templates: template.New(filepath.Join(t.TempDir(), "templates.json")),
		Auth:      store,
	})
	return srv, store
}

// do sends a request with optional headers ("Name: value", or several
// joined by newlines).
func do(h http.Handler, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	for _, kv := range strings.Split(strings.Join(headers, "\n"), "\n") {
		if k, v, ok := strings.Cut(kv, ": "); ok {
			r.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// login signs in and returns the session as a browser page sends it: the
// cookie and the page's half of the key, as two header lines.
func login(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := do(h, http.MethodPost, "/auth/login", `{"password":"`+testPassword+`"}`)
	var body struct {
		Key string `json:"key"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Key == "" {
		t.Fatalf("login = %d %s", rec.Code, rec.Body)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "nanocloud_session" {
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Errorf("session cookie is not HttpOnly+SameSite=Strict: %+v", c)
			}
			if strings.Contains(c.Value, body.Key) || strings.Contains(body.Key, c.Value) {
				t.Errorf("the cookie and the key are not separate halves: %q %q", c.Value, body.Key)
			}
			return "Cookie: nanocloud_session=" + c.Value + "\nX-Nanocloud-Key: " + body.Key
		}
	}
	t.Fatal("login set no session cookie")
	return ""
}

func TestAuth_RouteClasses(t *testing.T) {
	srv, store := newAuthServer(t, testPassword)
	cookie := login(t, srv)
	token, _, err := store.CreateToken("ci")
	if err != nil {
		t.Fatal(err)
	}
	bearer := "Authorization: Bearer " + token
	cookieOnly, keyOnly, _ := strings.Cut(cookie, "\n")
	key := strings.TrimPrefix(keyOnly, "X-Nanocloud-Key: ")

	for name, tc := range map[string]struct {
		method, path string
		headers      []string
		want         int
	}{
		"health is public":           {http.MethodGet, "/health", nil, http.StatusOK},
		"app shell is public":        {http.MethodGet, "/", nil, http.StatusOK},
		"deep link is public":        {http.MethodGet, "/vms/web/console-page", nil, http.StatusOK},
		"API needs a login":          {http.MethodGet, "/templates", nil, http.StatusUnauthorized},
		"unknown verb needs a login": {http.MethodPost, "/", nil, http.StatusUnauthorized},
		"console needs a login":      {http.MethodGet, "/vms/web/console", nil, http.StatusUnauthorized},
		"bad cookie":                 {http.MethodGet, "/templates", []string{"Cookie: nanocloud_session=nope"}, http.StatusUnauthorized},
		"bad token":                  {http.MethodGet, "/templates", []string{"Authorization: Bearer nc_nope"}, http.StatusUnauthorized},
		"cookie":                     {http.MethodGet, "/templates", []string{cookie}, http.StatusOK},
		"token":                      {http.MethodGet, "/templates", []string{bearer}, http.StatusOK},

		// The cookie reaches every port on the host, guests' services
		// included, so on its own it is nothing.
		"cookie without the key":  {http.MethodGet, "/templates", []string{cookieOnly}, http.StatusUnauthorized},
		"key without the cookie":  {http.MethodGet, "/templates", []string{keyOnly}, http.StatusUnauthorized},
		"key in a GET's query":    {http.MethodGet, "/templates?key=" + key, []string{cookieOnly}, http.StatusOK},
		"key in a DELETE's query": {http.MethodDelete, "/templates/x?key=" + key, []string{cookieOnly}, http.StatusUnauthorized},
		"token lists tokens":      {http.MethodGet, "/auth/tokens", []string{bearer}, http.StatusOK},
		"token mints a token":     {http.MethodPost, "/auth/tokens", []string{bearer}, http.StatusForbidden},
		"token revokes a token":   {http.MethodDelete, "/auth/tokens/x", []string{bearer}, http.StatusForbidden},
		"token sets the password": {http.MethodPost, "/auth/password", []string{bearer}, http.StatusForbidden},
		"session mints a token":   {http.MethodPost, "/auth/tokens", []string{cookie}, http.StatusBadRequest},

		// A cookie rides along with a cross-site request; a token does not.
		"cookie, cross-site fetch": {http.MethodDelete, "/templates/x", []string{cookie, "Sec-Fetch-Site: cross-site"}, http.StatusForbidden},
		"cookie, foreign origin":   {http.MethodDelete, "/templates/x", []string{cookie, "Origin: https://evil.example"}, http.StatusForbidden},
		"cookie, same origin":      {http.MethodDelete, "/templates/x", []string{cookie, "Sec-Fetch-Site: same-origin", "Origin: http://example.com"}, http.StatusNotFound},
		"token, foreign origin":    {http.MethodDelete, "/templates/x", []string{bearer, "Origin: https://evil.example"}, http.StatusNotFound},
		"console, foreign origin":  {http.MethodGet, "/vms/web/console", []string{cookie, "Upgrade: websocket", "Origin: https://evil.example"}, http.StatusForbidden},
	} {
		if rec := do(srv, tc.method, tc.path, "", tc.headers...); rec.Code != tc.want {
			t.Errorf("%s: %s %s = %d, want %d (%s)", name, tc.method, tc.path, rec.Code, tc.want, strings.TrimSpace(rec.Body.String()))
		}
	}
}

func TestAuth_NoPasswordFailsClosed(t *testing.T) {
	srv, _ := newAuthServer(t, "")
	rec := do(srv, http.MethodGet, "/templates", "")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "setup_required") {
		t.Errorf("GET /templates with no password set = %d %s", rec.Code, rec.Body)
	}
	if rec := do(srv, http.MethodPost, "/auth/login", `{"password":""}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("login with no password set = %d, want 401", rec.Code)
	}
	var st struct {
		SetupRequired bool `json:"setup_required"`
	}
	_ = json.Unmarshal(do(srv, http.MethodGet, "/auth/status", "").Body.Bytes(), &st)
	if !st.SetupRequired {
		t.Error("/auth/status does not report setup_required")
	}
}

func TestAuth_LoginLogoutAndStatus(t *testing.T) {
	srv, _ := newAuthServer(t, testPassword)
	if rec := do(srv, http.MethodPost, "/auth/login", `{"password":"wrong"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", rec.Code)
	}
	if rec := do(srv, http.MethodPost, "/auth/login", `{"password":"`+testPassword+`"}`, "Origin: https://evil.example"); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site login = %d, want 403", rec.Code)
	}
	cookie := login(t, srv)

	status := func(h ...string) bool {
		var st struct {
			Authenticated bool `json:"authenticated"`
		}
		_ = json.Unmarshal(do(srv, http.MethodGet, "/auth/status", "", h...).Body.Bytes(), &st)
		return st.Authenticated
	}
	if status() || !status(cookie) {
		t.Fatal("/auth/status does not reflect the session")
	}
	if rec := do(srv, http.MethodPost, "/auth/logout", "", cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", rec.Code)
	}
	if status(cookie) {
		t.Error("session still valid after logout")
	}
}

func TestAuth_LoginIsThrottled(t *testing.T) {
	for name, addr := range map[string]func(i int) string{
		"one address": func(int) string { return "192.0.2.1:1234" },
		// Any host can give itself as many addresses in its /64 as it likes.
		"one IPv6 /64": func(i int) string { return fmt.Sprintf("[fe80::%x]:1234", i+1) },
	} {
		srv, _ := newAuthServer(t, testPassword)
		var last int
		for i := range 7 {
			r := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"password":"wrong"}`))
			r.RemoteAddr = addr(i)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, r)
			last = rec.Code
		}
		if last != http.StatusTooManyRequests {
			t.Errorf("%s: 7th wrong password = %d, want 429", name, last)
		}
	}
}

// A request body that never finishes must not hold a connection forever;
// ReadHeaderTimeout covers only the headers.
func TestAuth_SlowBodyIsCutOff(t *testing.T) {
	defer api.SetBodyTimeout(200 * time.Millisecond)()
	srv, _ := newAuthServer(t, testPassword)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck // test connection
	_, _ = io.WriteString(conn, "POST /auth/login HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\n{\"pass")
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = io.ReadAll(conn)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Error("the server was still waiting for the body after 5s")
	}
}

func TestAuth_TokensAndPassword(t *testing.T) {
	srv, _ := newAuthServer(t, testPassword)
	cookie := login(t, srv)

	rec := do(srv, http.MethodPost, "/auth/tokens", `{"name":"ci"}`, cookie)
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &created) != nil || created.Token == "" {
		t.Fatalf("create token = %d %s", rec.Code, rec.Body)
	}
	if list := do(srv, http.MethodGet, "/auth/tokens", "", cookie).Body.String(); strings.Contains(list, created.Token) || strings.Contains(list, "hash") {
		t.Errorf("token list leaks a secret: %s", list)
	}
	if rec := do(srv, http.MethodDelete, "/auth/tokens/"+created.ID, "", cookie); rec.Code != http.StatusNoContent {
		t.Errorf("revoke = %d", rec.Code)
	}
	if rec := do(srv, http.MethodGet, "/templates", "", "Authorization: Bearer "+created.Token); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked token = %d, want 401", rec.Code)
	}

	if rec := do(srv, http.MethodPost, "/auth/password", `{"current":"wrong","new":"battery staple"}`, cookie); rec.Code != http.StatusForbidden {
		t.Errorf("password change with the wrong current = %d, want 403", rec.Code)
	}
	if rec := do(srv, http.MethodPost, "/auth/password", `{"current":"`+testPassword+`","new":"battery staple"}`, cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("password change = %d %s", rec.Code, rec.Body)
	}
	if rec := do(srv, http.MethodGet, "/templates", "", cookie); rec.Code != http.StatusOK {
		t.Errorf("the session that changed the password was signed out: %d", rec.Code)
	}
}

// The services manager drives this API in-process, and its first step in a
// create is listing VMs. Behind the login it would fail on its own daemon.
func TestAuth_ServicesBypassTheLogin(t *testing.T) {
	m := libvirtManager(t)
	dir := t.TempDir()
	mgr, err := services.New(services.NewStore(filepath.Join(dir, "services.json")), nil, filepath.Join(dir, "service_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	store := auth.New(filepath.Join(dir, "auth.json"))
	if err := store.SetPassword(testPassword); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{StoragePools: []config.StoragePool{{Name: "default", Path: t.TempDir(), Default: true}}}
	srv := api.New(api.Deps{
		WebFS: minimalWebFS(), Cfg: cfg, VMManager: m,
		MetaStore:  metadata.New(filepath.Join(dir, "metadata.json")),
		ImgManager: cachedImageManager(t),
		Services:   mgr,
		Auth:       store,
	})
	rec := do(srv, http.MethodPost, "/services", `{"name":"db","recipe":"postgres","size":"small"}`, login(t, srv))
	if rec.Code != http.StatusAccepted {
		t.Errorf("POST /services behind the login = %d %s", rec.Code, rec.Body)
	}
	_ = do(srv, http.MethodDelete, "/services/db", "", login(t, srv))
}
