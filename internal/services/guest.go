package services

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// serviceUser is the login the daemon creates on every service VM, and the
// one it reaches the VM as.
const serviceUser = "nanocloud"

// LoadOrCreateKey returns the daemon's SSH identity for service VMs, creating
// it on first use. Only VMs the services manager creates trust it.
func LoadOrCreateKey(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		signer, perr := ssh.ParsePrivateKey(data)
		if perr != nil {
			return nil, fmt.Errorf("services: parse key %s: %w", path, perr)
		}
		return signer, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("services: read key: %w", err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("services: generate key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "nano-cloud services")
	if err != nil {
		return nil, fmt.Errorf("services: encode key: %w", err)
	}
	// Write and rename, so a crash cannot leave a half-written key that fails
	// to parse on every later start.
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return nil, fmt.Errorf("services: write key: %w", err)
	}
	if _, err := tmp.Write(pem.EncodeToMemory(block)); err != nil {
		tmp.Close()           //nolint:errcheck
		os.Remove(tmp.Name()) //nolint:errcheck
		return nil, fmt.Errorf("services: write key: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name()) //nolint:errcheck
		return nil, fmt.Errorf("services: write key: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name()) //nolint:errcheck
		return nil, fmt.Errorf("services: write key: %w", err)
	}
	return ssh.NewSignerFromKey(priv)
}

// authorizedKey renders a public key as one authorized_keys line.
func authorizedKey(k ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

// ErrHostKeyChanged means a service VM presented a different SSH host key
// from the one recorded when the daemon first reached it.
var ErrHostKeyChanged = errors.New("services: the VM's SSH host key has changed")

// Guest runs commands on service VMs.
type Guest interface {
	// Run runs cmd on the VM at ip as the service user and returns its
	// standard output. hostKey is the key recorded for the VM, empty on first
	// contact; seen is the key the VM presented, for the caller to record.
	Run(ctx context.Context, ip, hostKey, cmd string) (out string, seen string, err error)
	// Send is Run with input on stdin, for files too large for the command
	// line.
	Send(ctx context.Context, ip, hostKey, cmd string, input []byte) (out string, seen string, err error)
}

type sshGuest struct {
	signer ssh.Signer
	// port is the SSH port; 22 except in tests.
	port string
}

func (g *sshGuest) Run(ctx context.Context, ip, hostKey, cmd string) (string, string, error) {
	return g.Send(ctx, ip, hostKey, cmd, nil)
}

func (g *sshGuest) Send(ctx context.Context, ip, hostKey, cmd string, input []byte) (string, string, error) {
	var seen string
	config := &ssh.ClientConfig{
		User: serviceUser,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(g.signer)},
		// Trust on first use: once recorded, a different host key is refused
		// rather than handed a command to run as root.
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			seen = authorizedKey(key)
			if hostKey != "" && seen != hostKey {
				return ErrHostKeyChanged
			}
			return nil
		},
		Timeout: 10 * time.Second,
	}

	addr := net.JoinHostPort(ip, g.port)
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", "", err
	}
	// One deadline bounds the handshake and the command: the context's if it
	// has one (draining a node takes long), otherwise 30 s.
	deadline := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	conn.SetDeadline(deadline) //nolint:errcheck // a failure shows up as a read error

	c, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		conn.Close() //nolint:errcheck
		return "", seen, err
	}
	client := ssh.NewClient(c, chans, reqs)
	defer client.Close() //nolint:errcheck

	session, err := client.NewSession()
	if err != nil {
		return "", seen, err
	}
	defer session.Close() //nolint:errcheck

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	if input != nil {
		session.Stdin = bytes.NewReader(input)
	}
	if err := session.Run(cmd); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.String(), seen, fmt.Errorf("%w: %s", err, msg)
		}
		return stdout.String(), seen, err
	}
	return stdout.String(), seen, nil
}
