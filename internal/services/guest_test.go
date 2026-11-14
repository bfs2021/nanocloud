package services

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

// testUserKey is a person's public key, as pasted into the create form.
var testUserKey = func() string {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		panic(err)
	}
	return authorizedKey(k) + " dev@laptop"
}()

func newSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLoadOrCreateKey_CreatesOnceAndKeepsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service_ed25519")
	first, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("key mode = %o, want 600", mode)
	}
	second, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if authorizedKey(first.PublicKey()) != authorizedKey(second.PublicKey()) {
		t.Error("a second load made a new key; every service VM would lock the daemon out")
	}
}

// sshServer is a minimal SSH server: it accepts one client key and answers
// every exec request with handle's output.
func sshServer(t *testing.T, host ssh.Signer, client ssh.PublicKey, handle func(cmd string) (string, uint32)) string {
	t.Helper()
	return sshServerIn(t, host, client, func(cmd string, _ []byte) (string, uint32) { return handle(cmd) })
}

// sshServerIn is sshServer handing handle the command's standard input too.
func sshServerIn(t *testing.T, host ssh.Signer, client ssh.PublicKey, handle func(cmd string, in []byte) (string, uint32)) string {
	t.Helper()
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if meta.User() == serviceUser && authorizedKey(key) == authorizedKey(client) {
				return nil, nil
			}
			return nil, errors.New("unknown key")
		},
	}
	config.AddHostKey(host)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() }) //nolint:errcheck // best-effort cleanup in test
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveConn(conn, config, handle)
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

func serveConn(conn net.Conn, config *ssh.ServerConfig, handle func(string, []byte) (string, uint32)) {
	_, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "session only") //nolint:errcheck
			continue
		}
		ch, requests, err := nc.Accept()
		if err != nil {
			return
		}
		go func() {
			defer ch.Close() //nolint:errcheck // best-effort cleanup in test
			for req := range requests {
				if req.Type != "exec" {
					req.Reply(false, nil) //nolint:errcheck
					continue
				}
				var payload struct{ Command string }
				if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
					req.Reply(false, nil) //nolint:errcheck
					return
				}
				req.Reply(true, nil) //nolint:errcheck
				in, _ := io.ReadAll(ch)
				out, status := handle(payload.Command, in)
				ch.Write([]byte(out))                                                              //nolint:errcheck
				ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status})) //nolint:errcheck
				return
			}
		}()
	}
}

func TestSSHGuest_RunsACommandAndReportsTheHostKey(t *testing.T) {
	host, daemon := newSigner(t), newSigner(t)
	var ran string
	port := sshServer(t, host, daemon.PublicKey(), func(cmd string) (string, uint32) {
		ran = cmd
		return "ready\n", 0
	})
	g := &sshGuest{signer: daemon, port: port}

	out, seen, err := g.Run(context.Background(), "127.0.0.1", "", "cat /x")
	if err != nil {
		t.Fatal(err)
	}
	if out != "ready\n" || ran != "cat /x" {
		t.Errorf("out = %q after running %q", out, ran)
	}
	if seen != authorizedKey(host.PublicKey()) {
		t.Errorf("seen host key = %q", seen)
	}
	// With the key recorded, the same host is still accepted.
	if _, _, err := g.Run(context.Background(), "127.0.0.1", seen, "true"); err != nil {
		t.Fatalf("second run with the recorded key: %v", err)
	}
}

func TestSSHGuest_RefusesAChangedHostKey(t *testing.T) {
	host, daemon := newSigner(t), newSigner(t)
	called := false
	port := sshServer(t, host, daemon.PublicKey(), func(string) (string, uint32) {
		called = true
		return "", 0
	})
	g := &sshGuest{signer: daemon, port: port}
	recorded := authorizedKey(newSigner(t).PublicKey())

	_, _, err := g.Run(context.Background(), "127.0.0.1", recorded, "sudo cat /etc/shadow")
	if !errors.Is(err, ErrHostKeyChanged) {
		t.Fatalf("err = %v, want ErrHostKeyChanged", err)
	}
	if called {
		t.Error("ran a command on a host whose key had changed")
	}
}

func TestSSHGuest_ReportsAFailedCommand(t *testing.T) {
	host, daemon := newSigner(t), newSigner(t)
	port := sshServer(t, host, daemon.PublicKey(), func(string) (string, uint32) { return "", 1 })
	g := &sshGuest{signer: daemon, port: port}
	if _, _, err := g.Run(context.Background(), "127.0.0.1", "", "false"); err == nil {
		t.Fatal("a command exiting 1 reported success")
	}
}

func TestSSHGuest_SendsInput(t *testing.T) {
	host, daemon := newSigner(t), newSigner(t)
	var got []byte
	port := sshServerIn(t, host, daemon.PublicKey(), func(_ string, in []byte) (string, uint32) {
		got = in
		return "", 0
	})
	g := &sshGuest{signer: daemon, port: port}
	want := bytes.Repeat([]byte("dashboard "), 50000)
	if _, _, err := g.Send(context.Background(), "127.0.0.1", "", "cat > /x", want); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the VM read %d bytes, want %d", len(got), len(want))
	}
}
