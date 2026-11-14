package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/nanocloud-io/nanocloud/internal/auth"
)

// A browser session's secret is split in two: the cookie carries one half and
// the page sends the other as sessionKeyHeader, or as ?key= where it cannot set
// a header. Browsers send cookies to every port on the host, including ports
// forwarded into guests, so the cookie alone must not sign anyone in.
const (
	sessionCookie    = "nanocloud_session"
	sessionKeyHeader = "X-Nanocloud-Key"
)

// maxPasswordChecks bounds the bcrypt comparisons running at once, so a flood
// of logins from many addresses costs a few cores, not all of them.
const maxPasswordChecks = 4

// authorize lets a request through or answers it. Requests reach the mux
// only when it returns true.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) bool {
	if s.public(r) {
		return true
	}
	secret, viaCookie := requestSecret(r)
	if viaCookie && crossSite(r) {
		http.Error(w, "cross-site request refused", http.StatusForbidden)
		return false
	}
	kind, err := s.auth.Authenticate(secret)
	if err != nil {
		writeAuthError(w, err)
		return false
	}
	if kind != auth.KindSession && s.sessionOnly(r) {
		http.Error(w, "managing tokens and the password needs a browser session, not an API token", http.StatusForbidden)
		return false
	}
	return true
}

// sessionOnly reports whether a request manages the credentials themselves.
// A token may not: one that leaked could otherwise mint its own replacements
// or guess the password with no limiter in the way.
func (s *Server) sessionOnly(r *http.Request) bool {
	_, pattern := s.mux.Handler(r)
	switch pattern {
	case "POST /auth/password", "POST /auth/tokens", "DELETE /auth/tokens/{id}":
		return true
	}
	return false
}

// public reports whether a request needs no login: the liveness probe, the
// login endpoints, and the app shell itself (any GET the SPA catch-all would
// serve), so a signed-out browser can load the page that asks for a login.
func (s *Server) public(r *http.Request) bool {
	switch r.Method + " " + r.URL.Path {
	case "GET /health", "HEAD /health", "GET /auth/status", "POST /auth/login":
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	_, pattern := s.mux.Handler(r)
	return pattern == "/"
}

// requestSecret returns the request's bearer token, or else its session
// secret put back together from the cookie and the page's key, and whether it
// came from the cookie.
func requestSecret(r *http.Request) (string, bool) {
	if h := r.Header.Get("Authorization"); h != "" {
		token, _ := strings.CutPrefix(h, "Bearer ")
		return strings.TrimSpace(token), false
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	key := r.Header.Get(sessionKeyHeader)
	if key == "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		key = r.URL.Query().Get("key")
	}
	if c.Value == "" || key == "" {
		return "", true
	}
	return c.Value + key, true
}

// crossSite reports whether a state-changing or console request came from
// another site. The browser may attach the cookie anyway, so the request
// itself is refused. A request with neither header is not from a browser.
func crossSite(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			return false
		}
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site != "same-origin" && site != "none"
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		return err != nil || u.Host != r.Host
	}
	return false
}

func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrNoPassword):
		writeJSONStatus(w, http.StatusUnauthorized, map[string]any{
			"error":          "no password has been set: run `sudo nanocloud passwd` on the host",
			"setup_required": true,
		})
	case errors.Is(err, auth.ErrBadCredentials):
		writeJSONStatus(w, http.StatusUnauthorized, map[string]string{"error": "login required"})
	default:
		log.Printf("auth: %v", err)
		http.Error(w, "authentication unavailable", http.StatusInternalServerError)
	}
}

// handleAuthStatus handles GET /auth/status, which the frontend asks first.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	set, err := s.auth.PasswordSet()
	if err != nil {
		writeAuthError(w, err)
		return
	}
	secret, _ := requestSecret(r)
	kind, err := s.auth.Authenticate(secret)
	writeJSON(w, map[string]any{
		"authenticated":  err == nil,
		"via":            kind,
		"setup_required": !set,
	})
}

// handleLogin handles POST /auth/login, body {"password": "..."}.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if crossSite(r) {
		http.Error(w, "cross-site request refused", http.StatusForbidden)
		return
	}
	client := clientAddr(r)
	if wait := s.limiter.Attempt(client); wait > 0 {
		w.Header().Set("Retry-After", fmt.Sprint(int(math.Ceil(wait.Seconds()))))
		http.Error(w, "too many failed attempts; try again shortly", http.StatusTooManyRequests)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	select {
	case s.passwordChecks <- struct{}{}:
	case <-r.Context().Done():
		return
	}
	secret, err := s.auth.Login(req.Password)
	<-s.passwordChecks
	if err != nil {
		if errors.Is(err, auth.ErrBadCredentials) {
			http.Error(w, "wrong password", http.StatusUnauthorized)
			return
		}
		writeAuthError(w, err)
		return
	}
	s.limiter.Succeed(client)
	half := len(secret) / 2
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    secret[:half],
		Path:     "/",
		MaxAge:   int(auth.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, map[string]string{"key": secret[half:]})
}

// handleLogout handles POST /auth/logout.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if secret, viaCookie := requestSecret(r); viaCookie {
		if err := s.auth.Logout(secret); err != nil {
			log.Printf("auth: logout: %v", err)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

// handleChangePassword handles POST /auth/password, body {"current", "new"}.
// Every other session ends; the caller's own stays signed in.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	keep, viaCookie := requestSecret(r)
	if !viaCookie {
		keep = ""
	}
	if err := s.auth.ChangePassword(req.Current, req.New, keep); err != nil {
		if errors.Is(err, auth.ErrBadCredentials) {
			http.Error(w, "the current password is wrong", http.StatusForbidden)
			return
		}
		http.Error(w, strings.TrimPrefix(err.Error(), "auth: "), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTokenList handles GET /auth/tokens.
func (s *Server) handleTokenList(w http.ResponseWriter, _ *http.Request) {
	tokens, err := s.auth.Tokens()
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, tokens)
}

// handleTokenCreate handles POST /auth/tokens, body {"name"}. The secret
// appears only in this response.
func (s *Server) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	secret, tok, err := s.auth.CreateToken(req.Name)
	if err != nil {
		http.Error(w, strings.TrimPrefix(err.Error(), "auth: "), http.StatusBadRequest)
		return
	}
	writeJSONStatus(w, http.StatusCreated, struct {
		auth.Token
		Secret string `json:"token"`
	}{tok, secret})
}

// handleTokenRevoke handles DELETE /auth/tokens/{id}.
func (s *Server) handleTokenRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.RevokeToken(r.PathValue("id")); err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			http.Error(w, "no such token", http.StatusNotFound)
			return
		}
		writeAuthError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// clientAddr is the address the login limiter counts failures against. It
// is the peer address only: trusting X-Forwarded-For would let a guesser pick
// a fresh address for every attempt. An IPv6 client counts as its /64, since
// any host can give itself as many addresses within one as it likes.
func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
	}
	return host
}
