package install

import (
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
	// certPath is the certificate the daemon serves, empty over plain HTTP.
	certPath string
}

func installedEndpoint() endpoint {
	e := endpoint{scheme: "https", port: defaultPort, stateDir: dataDir, selfSigned: true}
	if cfg, err := config.Load(cfgPath); err == nil {
		if cfg.Port != 0 {
			e.port = cfg.Port
		}
		if cfg.MetadataPath != "" {
			e.stateDir = filepath.Dir(cfg.MetadataPath)
		}
		if !cfg.TLSEnabled() {
			e.scheme, e.selfSigned = "http", false
			return e
		}
		if cfg.TLS.Cert != "" {
			e.selfSigned, e.certPath = false, cfg.TLS.Cert
			return e
		}
	}
	e.certPath, _ = tlscert.Paths(filepath.Join(e.stateDir, "tls"))
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
	if fp, err := tlscert.Fingerprint(e.certPath); err == nil {
		fmt.Println("The certificate is self-signed, so the browser will warn once. Its SHA-256 fingerprint is:")
		fmt.Printf("  %s\n", fp)
	}
}

// DaemonHealth returns the installed daemon's loopback /health URL and a
// client that trusts only the certificate the daemon serves.
func DaemonHealth() (string, *http.Client, error) {
	e := installedEndpoint()
	client, err := loopbackClient(e.certPath)
	return e.healthURL(), client, err
}

// loopbackClient trusts only the certificate at certPath, or speaks plain
// HTTP when it is empty.
func loopbackClient(certPath string) (*http.Client, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	if certPath == "" {
		return client, nil
	}
	cfg, err := tlscert.LoopbackConfig(certPath)
	if err != nil {
		return nil, err
	}
	client.Transport = &http.Transport{TLSClientConfig: cfg}
	return client, nil
}

// waitForHealthy polls url until it answers 200. systemd and launchd both
// report the service up as soon as the process exists, so only this tells a
// serving daemon from one that failed to bind or crashed loading config.
// The certificate is read on every attempt: a daemon starting for the first
// time writes it after polling has begun.
func waitForHealthy(url, certPath string, attempts int, delay time.Duration) bool {
	for i := 0; i < attempts; i++ {
		if client, err := loopbackClient(certPath); err == nil {
			resp, err := client.Get(url)
			if err == nil {
				healthy := resp.StatusCode == http.StatusOK
				resp.Body.Close() //nolint:errcheck // response already consumed; close is best-effort
				if healthy {
					return true
				}
			}
		}
		time.Sleep(delay)
	}
	return false
}
