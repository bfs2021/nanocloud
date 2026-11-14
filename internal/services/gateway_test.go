package services

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeNet hands the gateway real loopback listeners on kernel-chosen ports,
// remembering the address each was asked for, so tests never hold the real
// 30000s. An address in busy fails as in use.
type fakeNet struct {
	mu    sync.Mutex
	asked []string
	lns   map[string]net.Listener
	busy  map[string]bool
}

func (f *fakeNet) listen(_, addr string) (net.Listener, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, addr)
	if f.busy[addr] {
		return nil, &net.OpError{Op: "listen", Net: "tcp", Err: os.NewSyscallError("bind", syscall.EADDRINUSE)}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	if f.lns == nil {
		f.lns = map[string]net.Listener{}
	}
	f.lns[addr] = ln
	return ln, nil
}

// addr is where the listener asked for at asked really is.
func (f *fakeNet) addr(t *testing.T, asked string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	ln, ok := f.lns[asked]
	if !ok {
		t.Fatalf("nothing listened for %s; asked for %v", asked, f.asked)
	}
	return ln.Addr().String()
}

func (f *fakeNet) setBusy(addr string, busy bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy == nil {
		f.busy = map[string]bool{}
	}
	f.busy[addr] = busy
}

// vmServer stands in for a service on a VM: it reads a whole request, up to
// the client's half-close, then answers and closes.
func vmServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() }) //nolint:errcheck // test cleanup
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close() //nolint:errcheck // test server
				data, _ := io.ReadAll(c)
				c.Write([]byte("got " + string(data))) //nolint:errcheck // test server
			}()
		}
	}()
	return ln.Addr().String()
}

func newTestGateway(t *testing.T, resolve resolveFunc) (*Gateway, *fakeNet) {
	t.Helper()
	fn := &fakeNet{}
	g := newGateway(resolve)
	g.listen = fn.listen
	t.Cleanup(g.close)
	return g, fn
}

func TestGateway_ForwardsBothWaysUntilBothSidesAreDone(t *testing.T) {
	vm := vmServer(t)
	var mu sync.Mutex
	var asked []string
	g, fn := newTestGateway(t, func(_ context.Context, service, endpoint string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, service+"/"+endpoint)
		return vm, nil
	})
	g.sync([]binding{{service: "db", endpoint: "db", port: 30432}})
	if ok, problem := g.status("db", "db"); !ok {
		t.Fatalf("not listening: %s", problem)
	}

	c, err := net.Dial("tcp", fn.addr(t, "127.0.0.1:30432"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()                                //nolint:errcheck // test
	c.Write([]byte("select 1"))                    //nolint:errcheck // checked by the answer
	c.(*net.TCPConn).CloseWrite()                  //nolint:errcheck // checked by the answer
	c.SetDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck // test
	out, err := io.ReadAll(c)
	if err != nil || string(out) != "got select 1" {
		t.Fatalf("answer = %q, %v; want the VM's whole answer after the half-close", out, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(asked) != 1 || asked[0] != "db/db" {
		t.Errorf("resolved %v, want db/db once, as the connection opened", asked)
	}
}

func TestGateway_ListensOnLoopbackUnlessShared(t *testing.T) {
	g, fn := newTestGateway(t, func(context.Context, string, string) (string, error) { return "", errors.New("down") })
	g.sync([]binding{{service: "db", endpoint: "db", port: 30432}})
	if got := strings.Join(fn.asked, " "); got != "127.0.0.1:30432 [::1]:30432" {
		t.Fatalf("local listens on %q, want both loopbacks only", got)
	}
	loopback := fn.addr(t, "127.0.0.1:30432")

	g.sync([]binding{{service: "db", endpoint: "db", port: 30432, shared: true}})
	if got := fn.asked[len(fn.asked)-1]; got != ":30432" {
		t.Fatalf("shared listens on %q, want every interface", got)
	}
	if c, err := net.DialTimeout("tcp", loopback, time.Second); err == nil {
		c.Close() //nolint:errcheck // test
		t.Error("the loopback-only listener is still open after sharing")
	}

	// The same binding again changes nothing.
	n := len(fn.asked)
	g.sync([]binding{{service: "db", endpoint: "db", port: 30432, shared: true}})
	if len(fn.asked) != n {
		t.Errorf("re-listened for an unchanged binding: %v", fn.asked[n:])
	}
}

func TestGateway_SaysWhenAPortIsTakenAndTriesAgain(t *testing.T) {
	g, fn := newTestGateway(t, func(context.Context, string, string) (string, error) { return "", errors.New("down") })
	fn.setBusy("127.0.0.1:30432", true)
	b := []binding{{service: "db", endpoint: "db", port: 30432}}
	g.sync(b)
	ok, problem := g.status("db", "db")
	if ok || problem != "Port 30432 is in use by another program." {
		t.Fatalf("status = %v %q", ok, problem)
	}
	fn.setBusy("127.0.0.1:30432", false)
	g.sync(b)
	if ok, problem := g.status("db", "db"); !ok {
		t.Fatalf("still not listening once the port is free: %s", problem)
	}
}

func TestGateway_StopsListeningForAnEndpointThatIsGone(t *testing.T) {
	g, fn := newTestGateway(t, func(context.Context, string, string) (string, error) { return "", errors.New("down") })
	g.sync([]binding{{service: "db", endpoint: "db", port: 30432}})
	addr := fn.addr(t, "127.0.0.1:30432")
	g.sync(nil)
	if ok, problem := g.status("db", "db"); ok || problem != "" {
		t.Errorf("status after removal = %v %q", ok, problem)
	}
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		c.Close() //nolint:errcheck // test
		t.Error("still accepting connections for a removed endpoint")
	}
}

// A browser that opens a stopped service's web UI is told so, instead of
// getting a connection reset.
func TestGateway_TellsABrowserWhyTheServiceIsNotThere(t *testing.T) {
	g, fn := newTestGateway(t, func(context.Context, string, string) (string, error) {
		return "", &downError{why: "obs-1 is stopped", todo: "Start it in nano-cloud, then reload this page."}
	})
	g.sync([]binding{{service: "obs-1", endpoint: "grafana", port: 30000, scheme: "http"}})
	c, err := net.Dial("tcp", fn.addr(t, "127.0.0.1:30000"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()                                              //nolint:errcheck // test
	c.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")) //nolint:errcheck // checked by the answer
	c.SetDeadline(time.Now().Add(5 * time.Second))               //nolint:errcheck // test
	out, _ := io.ReadAll(c)
	if !strings.HasPrefix(string(out), "HTTP/1.1 503 ") || !strings.HasSuffix(string(out), "\r\n\r\nobs-1 is stopped. Start it in nano-cloud, then reload this page.\n") {
		t.Errorf("answer:\n%s", out)
	}
}

// A database client gets no HTTP page: plain TCP endpoints just close.
func TestPageText_SaysWhyAndWhatToDo(t *testing.T) {
	if got := pageText(errors.New("db is still starting")); got != "db is still starting. Reload this page in a moment.\n" {
		t.Errorf("plain error: %q", got)
	}
	if got := pageText(&downError{why: "db is stopped", todo: "Start it."}); got != "db is stopped. Start it.\n" {
		t.Errorf("down error: %q", got)
	}
}

func TestGateway_ClosesAPlainConnectionThatCannotBeForwarded(t *testing.T) {
	g, fn := newTestGateway(t, func(context.Context, string, string) (string, error) { return "", errors.New("db is stopped") })
	g.sync([]binding{{service: "db", endpoint: "db", port: 30432}})
	c, err := net.Dial("tcp", fn.addr(t, "127.0.0.1:30432"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()                                //nolint:errcheck // test
	c.SetDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck // test
	if out, _ := io.ReadAll(c); len(out) != 0 {
		t.Errorf("a plain TCP client was sent %q", out)
	}
}

func TestGateway_CloseEndsOpenConnections(t *testing.T) {
	// A VM that never answers keeps the forwarded connection open.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close() //nolint:errcheck // test
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, aerr := ln.Accept(); aerr == nil {
			accepted <- c
		}
	}()
	fn := &fakeNet{}
	g := newGateway(func(context.Context, string, string) (string, error) { return ln.Addr().String(), nil })
	g.listen = fn.listen
	g.sync([]binding{{service: "db", endpoint: "db", port: 30432}})
	c, err := net.Dial("tcp", fn.addr(t, "127.0.0.1:30432"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close() //nolint:errcheck // test
	select {
	case vmSide := <-accepted:
		defer vmSide.Close() //nolint:errcheck // test
	case <-time.After(5 * time.Second):
		t.Fatal("the connection never reached the VM")
	}

	done := make(chan struct{})
	go func() {
		g.close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("close waited on an open connection")
	}
}
