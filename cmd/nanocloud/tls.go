package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"path/filepath"

	"github.com/nanocloud-io/nanocloud/internal/config"
	"github.com/nanocloud-io/nanocloud/internal/tlscert"
)

// serverTLSConfig is what the daemon serves HTTPS with, and the certificate's
// path: the configured pair, or a self-signed one kept in stateDir/tls. Both
// are zero when the config turns TLS off.
func serverTLSConfig(cfg *config.Config, stateDir string) (*tls.Config, string, error) {
	if !cfg.TLSEnabled() {
		log.Printf("tls: disabled by config — serving plain HTTP")
		return nil, "", nil
	}
	certPath, keyPath := cfg.TLS.Cert, cfg.TLS.Key
	if certPath == "" {
		var err error
		if certPath, keyPath, err = tlscert.EnsureSelfSigned(filepath.Join(stateDir, "tls")); err != nil {
			return nil, "", err
		}
		if fp, err := tlscert.Fingerprint(certPath); err == nil {
			log.Printf("tls: self-signed certificate %s, SHA-256 %s", certPath, fp)
		}
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, "", fmt.Errorf("load %s and %s: %w", certPath, keyPath, err)
	}
	// HTTP/1.1 only: the console WebSockets upgrade an HTTP/1.1 connection,
	// and nothing here gains from HTTP/2.
	return &tls.Config{
		Certificates: []tls.Certificate{pair},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"},
	}, certPath, nil
}
