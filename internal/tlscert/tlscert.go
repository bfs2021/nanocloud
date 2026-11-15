// Package tlscert provides the certificate the daemon serves HTTPS with when
// none is configured: self-signed, made on first start, and kept, so its
// fingerprint is the same every time a browser asks the user to trust it.
package tlscert

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	certFile = "cert.pem"
	keyFile  = "key.pem"
	// validity is long because every regeneration is another "trust this
	// certificate?" prompt; the browser limits on lifetime apply to publicly
	// trusted CAs, not to a certificate the user accepts by hand.
	validity = 10 * 365 * 24 * time.Hour
	// renewBefore regenerates a certificate this close to expiring.
	renewBefore = 30 * 24 * time.Hour
)

// Paths returns the self-signed pair's locations in dir.
func Paths(dir string) (cert, key string) {
	return filepath.Join(dir, certFile), filepath.Join(dir, keyFile)
}

// EnsureSelfSigned returns a self-signed certificate and key in dir, making
// them if they are missing, unreadable or about to expire. The names it
// covers are this host's name, localhost, and every address the host has now.
func EnsureSelfSigned(dir string) (certPath, keyPath string, err error) {
	certPath, keyPath = Paths(dir)
	if _, loadErr := tls.LoadX509KeyPair(certPath, keyPath); loadErr == nil {
		if c, readErr := readCert(certPath); readErr == nil && time.Until(c.NotAfter) > renewBefore {
			return certPath, keyPath, nil
		}
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("tlscert: %w", err)
	}
	certPEM, keyPEM, err := generate(time.Now())
	if err != nil {
		return "", "", err
	}
	// The key first: a certificate without its key would read as valid to
	// nothing, but a key replaced under an old certificate would not load.
	if err := writeFile(keyPath, keyPEM, 0o600); err != nil {
		return "", "", err
	}
	if err := writeFile(certPath, certPEM, 0o644); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

// Fingerprint is the SHA-256 of the certificate at path, as colon-separated
// hex — the form browsers show, so a user can compare the two.
func Fingerprint(certPath string) (string, error) {
	c, err := readCert(certPath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(c.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":"), nil
}

// LoopbackConfig trusts only the certificate at certPath, for the CLI's own
// requests to the local daemon. The certificate is its own root, so this works
// whether it is self-signed or CA-issued, and ServerName is a name it carries:
// a certificate the user supplied need not list 127.0.0.1.
func LoopbackConfig(certPath string) (*tls.Config, error) {
	c, err := readCert(certPath)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(c)
	name := "localhost"
	switch {
	case len(c.DNSNames) > 0:
		name = c.DNSNames[0]
	case len(c.IPAddresses) > 0:
		name = c.IPAddresses[0].String()
	}
	return &tls.Config{RootCAs: pool, ServerName: name, MinVersion: tls.VersionTLS12}, nil
}

func generate(now time.Time) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("tlscert: generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("tlscert: serial: %w", err)
	}
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "nano-cloud " + host, Organization: []string{"nano-cloud (self-signed)"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames(host),
		IPAddresses:           hostIPs(),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("tlscert: create certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("tlscert: marshal key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}

func dnsNames(host string) []string {
	names := []string{"localhost"}
	if host != "" && host != "localhost" {
		names = append(names, host)
		if short, _, found := strings.Cut(host, "."); found {
			names = append(names, short)
		} else {
			names = append(names, host+".local")
		}
	}
	return names
}

func hostIPs() []net.IP {
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			ips = append(ips, n.IP)
		}
	}
	return ips
}

func readCert(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("tlscert: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("tlscert: %s holds no PEM certificate", path)
	}
	return x509.ParseCertificate(block.Bytes)
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("tlscert: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("tlscert: %w", err)
	}
	return nil
}

// RedirectPlainHTTP wraps a listener so that the connections it hands out
// speak TLS, while a plain-HTTP request on the same port is answered with a
// redirect to https:// instead of Go's bare "client sent an HTTP request to an
// HTTPS server". Each connection is sniffed on its own goroutine, so a client
// that connects and sends nothing holds up no one else.
func RedirectPlainHTTP(l net.Listener, tlsConfig *tls.Config) net.Listener {
	sl := &sniffListener{Listener: l, tlsConfig: tlsConfig, conns: make(chan net.Conn), errs: make(chan error, 1), done: make(chan struct{})}
	go sl.loop()
	return sl
}

type sniffListener struct {
	net.Listener
	tlsConfig *tls.Config
	conns     chan net.Conn
	errs      chan error
	done      chan struct{}
}

func (l *sniffListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case err := <-l.errs:
		return nil, err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *sniffListener) loop() {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				close(l.done)
				return
			}
			select {
			case l.errs <- err: // http.Server backs off and calls Accept again
			case <-l.done:
				return
			}
			continue
		}
		go l.sniff(c)
	}
}

func (l *sniffListener) sniff(c net.Conn) {
	pc := &peekedConn{Conn: c, r: bufio.NewReader(c)}
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	first, err := pc.r.Peek(1)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		c.Close() //nolint:errcheck // a connection that sent nothing
		return
	}
	// 0x16 opens a TLS handshake record; anything else is not TLS.
	if first[0] != 0x16 {
		redirect(pc)
		return
	}
	select {
	case l.conns <- tls.Server(pc, l.tlsConfig):
	case <-l.done:
		c.Close() //nolint:errcheck // the server is shutting down
	}
}

type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func redirect(c *peekedConn) {
	defer c.Close() //nolint:errcheck // one-shot response
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	// net/http's MaxHeaderBytes does not apply here, so bound the read or one
	// connection could grow the heap until the deadline.
	req, err := http.ReadRequest(bufio.NewReader(io.LimitReader(c.r, 8<<10)))
	if err != nil {
		return
	}
	host := req.Host
	if host == "" {
		host = c.LocalAddr().String()
	}
	resp := &http.Response{
		StatusCode: http.StatusMovedPermanently,
		ProtoMajor: 1, ProtoMinor: 1,
		Header:        http.Header{"Location": {"https://" + host + req.URL.RequestURI()}, "Connection": {"close"}},
		ContentLength: 0,
		Close:         true,
	}
	_ = resp.Write(c)
}
