package install

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/config"
	"github.com/nanocloud-io/nanocloud/internal/tlscert"
)

// endpoint is where the installed daemon answers, read from its config.
type endpoint struct {
	scheme   string
	port     int
	stateDir string
	// selfSigned is whether the daemon makes its own certificate.
	selfSigned bool
}

func installedEndpoint() endpoint {
	e := endpoint{scheme: "https", port: defaultPort, stateDir: dataDir, selfSigned: true}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return e
	}
	if cfg.Port != 0 {
		e.port = cfg.Port
	}
	if cfg.MetadataPath != "" {
		e.stateDir = filepath.Dir(cfg.MetadataPath)
	}
	if !cfg.TLSEnabled() {
		e.scheme, e.selfSigned = "http", false
	} else if cfg.TLS.Cert != "" {
		e.selfSigned = false
	}
	return e
}

// healthURL is the liveness endpoint over loopback.
func (e endpoint) healthURL() string {
	return fmt.Sprintf("%s://127.0.0.1:%d/health", e.scheme, e.port)
}

// browserURL is the address to open from another machine on the network.
func (e endpoint) browserURL() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	return fmt.Sprintf("%s://%s:%d", e.scheme, host, e.port)
}

// printAccess tells the user where to open the UI and, for a self-signed
// certificate, the fingerprint to compare against the browser's warning.
func (e endpoint) printAccess() {
	fmt.Printf("Service is running. Open %s in your browser.\n", e.browserURL())
	if !e.selfSigned {
		return
	}
	certPath, _ := tlscert.Paths(filepath.Join(e.stateDir, "tls"))
	if fp, err := tlscert.Fingerprint(certPath); err == nil {
		fmt.Println("The certificate is self-signed, so the browser will warn once. Its SHA-256 fingerprint is:")
		fmt.Printf("  %s\n", fp)
	}
}

// waitForHealthy polls url until it answers 200. systemd and launchd both
// report the service up as soon as the process exists, so only this tells a
// serving daemon from one that failed to bind or crashed loading config.
func waitForHealthy(url string, attempts int, delay time.Duration) bool {
	client := &http.Client{
		Timeout: 2 * time.Second,
		// Loopback to the daemon's own certificate; this only asks whether
		// it is up.
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // see above
	}
	for i := 0; i < attempts; i++ {
		resp, err := client.Get(url)
		if err == nil {
			healthy := resp.StatusCode == http.StatusOK
			resp.Body.Close() //nolint:errcheck // response already consumed; close is best-effort
			if healthy {
				return true
			}
		}
		time.Sleep(delay)
	}
	return false
}
