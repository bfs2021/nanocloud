package services

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// The gateway gives every service endpoint a stable port on this host, such
// as localhost:30432, and forwards each connection to the VM behind it. The
// address survives the VM's address changing, needs no macOS Local Network
// permission, and when shared reaches machines outside the VM network. The
// target VM is resolved per connection.

// binding is one endpoint the gateway listens for.
type binding struct {
	service  string
	endpoint string
	port     int
	// shared listens on every interface instead of loopback only.
	shared bool
	// scheme is "http", "https" or "" for plain TCP. An unreachable http
	// endpoint answers with a page explaining why.
	scheme string
}

func (b binding) key() string { return b.service + "/" + b.endpoint }

// resolveFunc returns where a connection to a service's endpoint goes now,
// as host:port, or why it cannot, which may be a *downError.
type resolveFunc func(ctx context.Context, service, endpoint string) (string, error)

// downError is why a connection cannot be forwarded, and what a person at a
// browser can do about it.
type downError struct{ why, todo string }

func (e *downError) Error() string { return e.why }

// pageText is what a browser is shown for err: why, then what to do.
func pageText(err error) string {
	todo := "Reload this page in a moment."
	var d *downError
	if errors.As(err, &d) && d.todo != "" {
		todo = d.todo
	}
	return err.Error() + ". " + todo + "\n"
}

// Gateway listens for the endpoints it is given and forwards what connects.
type Gateway struct {
	resolve resolveFunc
	// listen and dial are net's; fields so tests choose their own ports.
	listen func(network, addr string) (net.Listener, error)
	dial   func(ctx context.Context, network, addr string) (net.Conn, error)

	mu     sync.Mutex
	open   map[string]*gwEntry
	conns  map[net.Conn]struct{}
	closed bool
	wg     sync.WaitGroup
}

// gwEntry is one binding and the listeners serving it, or why there are none.
type gwEntry struct {
	b   binding
	lns []net.Listener
	err error
}

func (e *gwEntry) close() {
	for _, ln := range e.lns {
		ln.Close() //nolint:errcheck // closing to stop accepting; nothing to report
	}
	e.lns = nil
}

func newGateway(resolve resolveFunc) *Gateway {
	d := &net.Dialer{Timeout: 5 * time.Second}
	return &Gateway{
		resolve: resolve,
		listen:  net.Listen,
		dial:    d.DialContext,
		open:    map[string]*gwEntry{},
		conns:   map[net.Conn]struct{}{},
	}
}

// sync makes the gateway listen for exactly want, reopening a binding whose
// port or sharing changed and retrying one that failed last time.
func (g *Gateway) sync(want []binding) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return
	}
	keep := make(map[string]bool, len(want))
	for _, b := range want {
		k := b.key()
		keep[k] = true
		if e := g.open[k]; e != nil {
			if e.b == b && e.err == nil {
				continue
			}
			e.close()
		}
		g.open[k] = g.bind(b)
	}
	for k, e := range g.open {
		if !keep[k] {
			e.close()
			delete(g.open, k)
		}
	}
}

// bind opens b's listeners: every interface when shared, otherwise
// 127.0.0.1 and ::1, since "localhost" may resolve to either. Called with
// g.mu held.
func (g *Gateway) bind(b binding) *gwEntry {
	e := &gwEntry{b: b}
	port := strconv.Itoa(b.port)
	if b.shared {
		ln, err := g.listen("tcp", ":"+port)
		if err != nil {
			e.err = listenError(b.port, err)
			return e
		}
		e.lns = append(e.lns, ln)
	} else {
		ln, err := g.listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			e.err = listenError(b.port, err)
			return e
		}
		e.lns = append(e.lns, ln)
		if ln6, err := g.listen("tcp", "[::1]:"+port); err == nil {
			e.lns = append(e.lns, ln6)
		}
	}
	for _, ln := range e.lns {
		g.wg.Add(1)
		go g.serve(ln, b)
	}
	return e
}

func listenError(port int, err error) error {
	if errors.Is(err, syscall.EADDRINUSE) {
		return fmt.Errorf("port %d is in use by another program", port)
	}
	return fmt.Errorf("cannot listen on port %d: %v", port, err)
}

// status reports whether the gateway is listening for a service's endpoint,
// and if it should be but is not, why.
func (g *Gateway) status(service, endpoint string) (listening bool, problem string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.open[service+"/"+endpoint]
	switch {
	case e == nil:
		return false, ""
	case e.err != nil:
		return false, sentence(e.err.Error())
	}
	return true, ""
}

// close stops every listener and connection and waits for them to finish.
func (g *Gateway) close() {
	g.mu.Lock()
	g.closed = true
	for k, e := range g.open {
		e.close()
		delete(g.open, k)
	}
	for c := range g.conns {
		c.Close() //nolint:errcheck // shutting down
	}
	g.mu.Unlock()
	g.wg.Wait()
}

func (g *Gateway) serve(ln net.Listener, b binding) {
	defer g.wg.Done()
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// Out of file descriptors and the like: wait rather than spin.
			log.Printf("services: gateway %s: accept: %v", b.key(), err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if !g.track(c) {
			c.Close() //nolint:errcheck // shutting down
			return
		}
		g.wg.Add(1)
		go g.forward(c, b)
	}
}

// track records an open connection so close can end it; false once closed.
func (g *Gateway) track(c net.Conn) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.conns[c] = struct{}{}
	return true
}

func (g *Gateway) untrack(c net.Conn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.conns, c)
}

// forward connects c to wherever b's endpoint is now and copies both ways
// until both sides are done.
func (g *Gateway) forward(c net.Conn, b binding) {
	defer g.wg.Done()
	defer g.untrack(c)
	defer c.Close() //nolint:errcheck // done with it either way

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	target, err := g.resolve(ctx, b.service, b.endpoint)
	var up net.Conn
	if err == nil {
		up, err = g.dial(ctx, "tcp", target)
		if err != nil {
			err = fmt.Errorf("%s is not answering yet", b.service)
		}
	}
	cancel()
	if err != nil {
		if b.scheme == "http" {
			unavailable(c, err)
		}
		return
	}
	if !g.track(up) {
		up.Close() //nolint:errcheck // shutting down
		return
	}
	defer g.untrack(up)
	defer up.Close() //nolint:errcheck // done with it either way
	pipe(c, up)
}

// pipe copies in both directions, passing each end of stream on as a
// half-close so a client that has finished sending still gets the response.
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	half := func(dst, src net.Conn) {
		io.Copy(dst, src) //nolint:errcheck // an error ends this direction, which is all it can do
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite() //nolint:errcheck // the peer may be gone already
		} else {
			dst.Close() //nolint:errcheck // no half-close: end it whole
		}
		done <- struct{}{}
	}
	go half(a, b)
	go half(b, a)
	<-done
	<-done
}

// unavailable answers an http client with a 503 explaining why the service
// cannot be reached. It reads the request first, because closing mid-request
// shows in the browser as a reset.
func unavailable(c net.Conn, why error) {
	c.SetDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck // best effort
	r := bufio.NewReader(io.LimitReader(c, 64<<10))
	for {
		line, err := r.ReadString('\n')
		if err != nil || line == "\r\n" || line == "\n" {
			break
		}
	}
	body := pageText(why)
	fmt.Fprintf(c, "HTTP/1.1 503 Service Unavailable\r\nContent-Type: text/plain; charset=utf-8\r\n"+ //nolint:errcheck // the client may be gone
		"Content-Length: %d\r\nRetry-After: 5\r\nConnection: close\r\n\r\n%s", len(body), body)
}
