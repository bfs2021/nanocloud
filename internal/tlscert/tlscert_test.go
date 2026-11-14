package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestEnsureSelfSignedIsKept(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, err := EnsureSelfSigned(dir)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Fingerprint(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = EnsureSelfSigned(dir); err != nil {
		t.Fatal(err)
	}
	if again, _ := Fingerprint(certPath); again != first {
		t.Error("a valid certificate was replaced: every restart would re-prompt the browser")
	}
	if fi, _ := os.Stat(keyPath); fi.Mode().Perm() != 0o600 {
		t.Errorf("key mode = %v, want 0600", fi.Mode().Perm())
	}

	c, err := readCert(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.VerifyHostname("localhost"); err != nil {
		t.Errorf("not valid for localhost: %v", err)
	}
	if err := c.VerifyHostname("127.0.0.1"); err != nil {
		t.Errorf("not valid for 127.0.0.1: %v", err)
	}
}

func TestEnsureSelfSignedRenewsNearExpiry(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := Paths(dir)
	certPEM, keyPEM, err := generate(time.Now().Add(-validity + renewBefore/2))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	old, _ := Fingerprint(certPath)
	if _, _, err := EnsureSelfSigned(dir); err != nil {
		t.Fatal(err)
	}
	if renewed, _ := Fingerprint(certPath); renewed == old {
		t.Error("a certificate about to expire was kept")
	}
}

func serveTLS(t *testing.T) string {
	t.Helper()
	certPath, keyPath, err := EnsureSelfSigned(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			http.Error(w, "r.TLS is nil", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, "secure")
	})}
	go srv.Serve(RedirectPlainHTTP(ln, &tls.Config{Certificates: []tls.Certificate{pair}})) //nolint:errcheck // ends at Close
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

func TestListenerServesTLSAndRedirectsPlainHTTP(t *testing.T) {
	addr := serveTLS(t)
	client := &http.Client{
		Timeout:       5 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // a self-signed test certificate
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	// A silent client must not hold up anyone else.
	idle, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close() //nolint:errcheck

	resp, err := client.Get("https://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "secure" {
		t.Errorf("https = %d %q, want 200 with r.TLS set", resp.StatusCode, body)
	}

	resp, err = client.Get("http://" + addr + "/vms/web?tab=console")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if want := "https://" + addr + "/vms/web?tab=console"; resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != want {
		t.Errorf("plain http = %d Location %q, want 301 %q", resp.StatusCode, resp.Header.Get("Location"), want)
	}
}

func TestFingerprintMatchesTheServedCertificate(t *testing.T) {
	dir := t.TempDir()
	certPath, _, err := EnsureSelfSigned(dir)
	if err != nil {
		t.Fatal(err)
	}
	fp, _ := Fingerprint(certPath)
	if len(strings.Split(fp, ":")) != 32 {
		t.Errorf("fingerprint %q is not 32 colon-separated bytes", fp)
	}
	c, _ := readCert(certPath)
	if c.KeyUsage&x509.KeyUsageDigitalSignature == 0 || c.IsCA {
		t.Errorf("certificate usage: KeyUsage=%v IsCA=%v", c.KeyUsage, c.IsCA)
	}
}

// Headers that never end are dropped once they pass the limit, not read
// into memory until the deadline.
func TestPlainHTTPHeadersAreBounded(t *testing.T) {
	addr := serveTLS(t)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck // test connection
	_, _ = io.WriteString(conn, "GET / HTTP/1.1\r\nX: "+strings.Repeat("a", 64<<10))
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadAll(conn); err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			t.Error("the redirect was still reading headers after 64 KiB")
		}
	}
}
