package vzvm

import (
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/cloudinit"
)

// serialReplayBytes is how much recent console output a newly attached viewer
// is shown — enough for the login prompt and the last screens of boot.
const serialReplayBytes = 64 << 10

// subscriberQueue bounds how far a viewer may fall behind before it is
// dropped, so one stalled browser tab cannot stall the console for the rest.
const subscriberQueue = 256

// serialHub is the host side of one VM's console, owned by its helper. It
// keeps recent output for replay, fans it out to viewers and forwards their
// input. Viewers stay attached across a guest reboot; each boot gets a fresh
// serialPort.
//
// It also watches for guest markers. Virtualization.framework stops a VM that
// resets, so the reboot marker is how a reboot is told apart from a poweroff;
// the ready marker reports the guest's address.
type serialHub struct {
	mu       sync.Mutex
	ring     []byte
	subs     map[*serialConn]struct{}
	closed   bool
	port     *serialPort
	markers  markerScanner
	rebootAt time.Time
	// ready carries the latest ready report to the helper; it holds at most
	// one, and a newer report replaces one not yet taken.
	ready chan readyReport

	writeMu sync.Mutex
}

// serialPort is one boot's pipes. guestIn and guestOut go to the machine;
// hostIn and hostOut are the hub's ends.
type serialPort struct {
	guestIn, guestOut *os.File
	hostIn, hostOut   *os.File
	done              chan struct{}
}

func newSerialHub() *serialHub {
	return &serialHub{subs: make(map[*serialConn]struct{}), ready: make(chan readyReport, 1)}
}

// newPort makes the pipes for a new boot and makes them the hub's current
// ones. The caller hands guestIn/guestOut to the machine.
func (h *serialHub) newPort() (*serialPort, error) {
	guestIn, hostIn, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	hostOut, guestOut, err := os.Pipe()
	if err != nil {
		guestIn.Close() //nolint:errcheck // already failing
		hostIn.Close()  //nolint:errcheck // already failing
		return nil, err
	}
	p := &serialPort{guestIn: guestIn, guestOut: guestOut, hostIn: hostIn, hostOut: hostOut, done: make(chan struct{})}
	h.mu.Lock()
	h.port = p
	h.markers = markerScanner{} // nothing half-read carries over into a new boot
	h.mu.Unlock()
	go h.pump(p)
	return p, nil
}

// pump copies one boot's guest output into the hub until its end is closed.
func (h *serialHub) pump(p *serialPort) {
	defer close(p.done)
	buf := make([]byte, 4096)
	for {
		n, err := p.hostOut.Read(buf)
		if n > 0 {
			h.broadcast(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (h *serialHub) broadcast(p []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, body := range h.markers.feed(p) {
		switch cmd, fields := parseMarker(body); cmd {
		case cloudinit.RebootCommand:
			h.rebootAt = time.Now()
		case cloudinit.ReadyCommand:
			select { // replace a report the helper has not taken yet
			case <-h.ready:
			default:
			}
			h.ready <- readyFrom(fields)
		}
	}

	h.ring = append(h.ring, p...)
	if over := len(h.ring) - serialReplayBytes; over > 0 {
		h.ring = append(h.ring[:0:0], h.ring[over:]...)
	}
	for c := range h.subs {
		chunk := append([]byte(nil), p...)
		select {
		case c.in <- chunk:
		default:
			// Too far behind: drop this viewer rather than block the rest.
			delete(h.subs, c)
			c.closeLocked()
		}
	}
}

// rebootRequested reports whether the guest announced a reboot within the
// last window, and forgets the announcement either way.
func (h *serialHub) rebootRequested(window time.Duration) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	at := h.rebootAt
	h.rebootAt = time.Time{}
	return !at.IsZero() && time.Since(at) <= window
}

// attach returns a new viewer that first sees the replay buffer, then live
// output. Reads end with io.EOF when the VM stops for good.
func (h *serialHub) attach() (io.ReadWriteCloser, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errors.New("console closed: the VM has stopped")
	}
	c := &serialConn{hub: h, in: make(chan []byte, subscriberQueue), gone: make(chan struct{})}
	if len(h.ring) > 0 {
		c.in <- append([]byte(nil), h.ring...)
	}
	h.subs[c] = struct{}{}
	return c, nil
}

// write sends viewer input to the guest. Between the boots of a reboot there
// is no guest to read it, and it is dropped.
func (h *serialHub) write(p []byte) (int, error) {
	h.mu.Lock()
	port := h.port
	h.mu.Unlock()
	if port == nil {
		return len(p), nil
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if _, err := port.hostIn.Write(p); err != nil && !errors.Is(err, os.ErrClosed) {
		return 0, err
	}
	return len(p), nil
}

// endPort releases one boot's pipes once its machine has stopped.
func (h *serialHub) endPort(p *serialPort) {
	h.mu.Lock()
	if h.port == p {
		h.port = nil
	}
	h.mu.Unlock()
	p.close()
}

func (p *serialPort) close() {
	// Closing the guest's write end lets pump see EOF. If the framework still
	// holds a copy of it, closing the read end after a grace period unblocks
	// pump anyway.
	p.guestOut.Close() //nolint:errcheck // unblocks pump with EOF
	select {
	case <-p.done:
	case <-time.After(time.Second):
	}
	p.hostOut.Close() //nolint:errcheck // best-effort release
	<-p.done
	p.guestIn.Close() //nolint:errcheck // best-effort release
	p.hostIn.Close()  //nolint:errcheck // best-effort release
}

// close ends the console for good: every viewer reads EOF, and the current
// boot's pipes are released.
func (h *serialHub) close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	for c := range h.subs {
		delete(h.subs, c)
		c.closeLocked()
	}
	port := h.port
	h.port = nil
	h.mu.Unlock()
	if port != nil {
		port.close()
	}
}

// serialConn is one viewer of a serialHub.
type serialConn struct {
	hub     *serialHub
	in      chan []byte
	pending []byte
	gone    chan struct{}
	once    sync.Once
}

func (c *serialConn) Read(p []byte) (int, error) {
	if len(c.pending) == 0 {
		select {
		case chunk := <-c.in:
			c.pending = chunk
		case <-c.gone:
			// Drain what was queued before the close, then report EOF.
			select {
			case chunk := <-c.in:
				c.pending = chunk
			default:
				return 0, io.EOF
			}
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *serialConn) Write(p []byte) (int, error) {
	select {
	case <-c.gone:
		return 0, io.ErrClosedPipe
	default:
	}
	return c.hub.write(p)
}

// Close detaches the viewer. The VM and other viewers are unaffected.
func (c *serialConn) Close() error {
	c.hub.mu.Lock()
	delete(c.hub.subs, c)
	c.closeLocked()
	c.hub.mu.Unlock()
	return nil
}

// closeLocked marks the viewer gone; the hub's mutex must be held.
func (c *serialConn) closeLocked() {
	c.once.Do(func() { close(c.gone) })
}
