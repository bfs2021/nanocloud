//go:build darwin || linux

package vswitch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"regexp"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/nanocloud-io/nanocloud/internal/version"
)

// The control socket speaks one JSON request line per connection, answered
// with one reply line. "attach" passes the VM's link as an SCM_RIGHTS
// descriptor and holds the connection open; whichever end closes it detaches
// the VM. Both ends only talk to a peer running as their own user.

const (
	opAttach = "attach"
	opStatus = "status"
	opQuit   = "quit"

	// protocolVersion changes only when an old helper could no longer attach
	// to a new switch. Helpers outlive daemon upgrades, so it must not.
	protocolVersion = 1

	maxRequest = 4 << 10
	// oobSize exceeds any control message macOS or Linux lets a sender build.
	// On truncation macOS still installs every descriptor but reports none,
	// leaking them.
	oobSize       = 4 << 10
	ioTimeout     = 5 * time.Second
	statusTimeout = 2 * time.Second
)

// namePattern is what a port may be called: a VM name, as the API allows it.
var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

type request struct {
	Op      string `json:"op"`
	Version int    `json:"version,omitempty"`
	Name    string `json:"name,omitempty"`
	MAC     string `json:"mac,omitempty"`
}

type reply struct {
	OK    bool   `json:"ok,omitempty"`
	Error string `json:"error,omitempty"`
}

// ErrNoSwitch is a host whose VMs are not on nano-cloud's switch.
var ErrNoSwitch = errors.New("this host's VMs are not on a nano-cloud switch")

// Report is what "status" answers: the switch's own version and its ports.
type Report struct {
	reply
	Version string `json:"version"`
	PID     int    `json:"pid"`
	Status
}

// Server serves the control socket for a Switch.
type Server struct {
	sw   *Switch
	ln   *net.UnixListener
	path string
	info os.FileInfo
	uid  int

	quitOnce sync.Once
	quit     chan struct{}

	mu    sync.Mutex
	conns map[*net.UnixConn]struct{} // attach connections, held open
	shut  bool
}

// Listen takes the control socket at path for sw. It refuses while another
// switch answers there, and otherwise replaces a socket left by one that died.
func Listen(path string, sw *Switch) (*Server, error) {
	if _, err := Ping(path); err == nil {
		return nil, fmt.Errorf("another switch is already serving %s", path)
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	ln.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close() //nolint:errcheck // already failing
		return nil, err
	}
	info, _ := os.Stat(path)
	return &Server{sw: sw, ln: ln, path: path, info: info, uid: os.Geteuid(), quit: make(chan struct{}), conns: make(map[*net.UnixConn]struct{})}, nil
}

// Quit is closed when a client asks the switch to exit.
func (s *Server) Quit() <-chan struct{} { return s.quit }

// Serve accepts connections until Close.
func (s *Server) Serve() {
	for {
		c, err := s.ln.AcceptUnix()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

// Close stops serving, ends every attach connection so helpers reattach to
// the next switch, and removes the socket if it is still this server's.
func (s *Server) Close() {
	s.ln.Close() //nolint:errcheck // shutting down
	s.mu.Lock()
	s.shut = true
	for c := range s.conns {
		c.Close() //nolint:errcheck // shutting down
	}
	s.mu.Unlock()
	if fi, err := os.Stat(s.path); err == nil && s.info != nil && os.SameFile(fi, s.info) {
		os.Remove(s.path) //nolint:errcheck // best-effort cleanup
	}
}

func (s *Server) handle(c *net.UnixConn) {
	defer c.Close() //nolint:errcheck // one request per connection
	if err := checkPeer(c, s.uid); err != nil {
		log.Printf("vswitch: refused a client: %v", err)
		return
	}
	_ = c.SetDeadline(time.Now().Add(ioTimeout))
	req, files, err := readRequest(c)
	// Everything received is closed here, whatever happens: the switch keeps
	// its own duplicate of an attached link, and closing a File twice is safe.
	defer closeFiles(files)
	enc := json.NewEncoder(c)
	if err != nil {
		_ = enc.Encode(reply{Error: err.Error()})
		return
	}

	switch req.Op {
	case opStatus:
		_ = enc.Encode(Report{reply: reply{OK: true}, Version: version.Version, PID: os.Getpid(), Status: s.sw.Status()})
	case opQuit:
		_ = enc.Encode(reply{OK: true})
		s.quitOnce.Do(func() { close(s.quit) })
	case opAttach:
		detach, err := s.attach(req, files)
		if err != nil {
			_ = enc.Encode(reply{Error: err.Error()})
			return
		}
		defer detach()
		if !s.hold(c) {
			return
		}
		defer s.release(c)
		if enc.Encode(reply{OK: true}) != nil {
			return
		}
		// Held until the helper goes away; it sends nothing more.
		_ = c.SetDeadline(time.Time{})
		_, _ = io.Copy(io.Discard, io.LimitReader(c, maxRequest))
	default:
		_ = enc.Encode(reply{Error: fmt.Sprintf("unknown op %q", req.Op)})
	}
}

// hold records an attach connection for Close, unless the server is closing.
func (s *Server) hold(c *net.UnixConn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shut {
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) release(c *net.UnixConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, c)
}

// attach validates an attach request and connects its link. The caller
// closes files.
func (s *Server) attach(req request, files []*os.File) (func(), error) {
	fail := func(err error) (func(), error) { return nil, err }
	if req.Version != protocolVersion {
		return fail(fmt.Errorf("protocol version %d is not supported (this switch speaks %d)", req.Version, protocolVersion))
	}
	if !namePattern.MatchString(req.Name) {
		return fail(fmt.Errorf("bad VM name %q", req.Name))
	}
	mac, err := ParseVMMAC(req.MAC)
	if err != nil {
		return fail(err)
	}
	if len(files) != 1 {
		return fail(fmt.Errorf("expected one link descriptor, got %d", len(files)))
	}
	p, err := newDgramPort(files[0])
	if err != nil {
		return nil, err
	}
	detach, err := s.sw.Attach(req.Name, mac, p)
	if err != nil {
		p.Close() //nolint:errcheck // refused
		return nil, err
	}
	log.Printf("vswitch: %s (%s) attached", req.Name, mac)
	return func() {
		detach()
		log.Printf("vswitch: %s (%s) detached", req.Name, mac)
	}, nil
}

// readRequest reads one request line and any descriptors sent with it. Each
// descriptor is wrapped in a File at once so no path can close the same number
// twice, by which time it may be another VM's link.
func readRequest(c *net.UnixConn) (request, []*os.File, error) {
	var (
		line  []byte
		files []*os.File
		buf   = make([]byte, maxRequest)
		oob   = make([]byte, oobSize)
	)
	fail := func(err error) (request, []*os.File, error) {
		closeFiles(files)
		return request{}, nil, err
	}
	for !bytes.Contains(line, []byte{'\n'}) {
		n, oobn, flags, _, err := c.ReadMsgUnix(buf, oob)
		got, perr := parseRights(oob[:oobn])
		files = append(files, got...)
		if perr != nil {
			return fail(perr)
		}
		if flags&unix.MSG_CTRUNC != 0 {
			return fail(errors.New("too many descriptors"))
		}
		line = append(line, buf[:n]...)
		if len(line) > maxRequest {
			return fail(errors.New("request too long"))
		}
		if err != nil {
			return fail(fmt.Errorf("read request: %w", err))
		}
	}
	var req request
	if err := json.Unmarshal(bytes.TrimSpace(line), &req); err != nil {
		return fail(errors.New("bad request"))
	}
	return req, files, nil
}

func parseRights(oob []byte) ([]*os.File, error) {
	if len(oob) == 0 {
		return nil, nil
	}
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, err
	}
	var files []*os.File
	for _, m := range msgs {
		fds, err := unix.ParseUnixRights(&m)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			files = append(files, os.NewFile(uintptr(fd), "received"))
		}
	}
	return files, nil
}

func closeFiles(files []*os.File) {
	for _, f := range files {
		f.Close() //nolint:errcheck // discarding; a second Close is a no-op
	}
}

// checkPeer refuses a peer that is not running as uid.
func checkPeer(c *net.UnixConn, uid int) error {
	raw, err := c.SyscallConn()
	if err != nil {
		return err
	}
	peer := -1
	var perr error
	if err := raw.Control(func(fd uintptr) { peer, perr = peerUID(int(fd)) }); err != nil {
		return err
	}
	if perr != nil {
		return perr
	}
	if peer != uid {
		return fmt.Errorf("peer runs as uid %d, not %d", peer, uid)
	}
	return nil
}

// dial connects to the switch at path and checks it runs as this process's
// own user, so a helper never hands its VM's link to anything else.
func dial(path string) (*net.UnixConn, error) {
	c, err := net.DialTimeout("unix", path, statusTimeout)
	if err != nil {
		return nil, err
	}
	uc := c.(*net.UnixConn)
	if err := checkPeer(uc, os.Geteuid()); err != nil {
		uc.Close() //nolint:errcheck // refused
		return nil, fmt.Errorf("switch at %s: %w", path, err)
	}
	return uc, nil
}

// call sends one request and reads one reply line into out.
func call(c *net.UnixConn, req request, rights []byte, out any) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_ = c.SetDeadline(time.Now().Add(ioTimeout))
	if _, _, err = c.WriteMsgUnix(append(data, '\n'), rights, nil); err != nil {
		return err
	}
	line, err := readLine(c)
	if err != nil {
		return err
	}
	return json.Unmarshal(line, out)
}

// readLine reads up to a newline byte by byte, consuming nothing after it.
func readLine(c io.Reader) ([]byte, error) {
	var line []byte
	b := make([]byte, 1)
	for len(line) < maxRequest {
		if _, err := c.Read(b); err != nil {
			return nil, err
		}
		if b[0] == '\n' {
			return line, nil
		}
		line = append(line, b[0])
	}
	return nil, errors.New("reply too long")
}

// Ping asks the switch at path for its report.
func Ping(path string) (Report, error) {
	c, err := dial(path)
	if err != nil {
		return Report{}, err
	}
	defer c.Close() //nolint:errcheck // one-shot
	var r Report
	if err := call(c, request{Op: opStatus}, nil, &r); err != nil {
		return Report{}, err
	}
	if r.Error != "" {
		return Report{}, errors.New(r.Error)
	}
	return r, nil
}

// Quit asks the switch at path to exit.
func Quit(path string) error {
	c, err := dial(path)
	if err != nil {
		return err
	}
	defer c.Close() //nolint:errcheck // one-shot
	var r reply
	if err := call(c, request{Op: opQuit}, nil, &r); err != nil {
		return err
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return nil
}

// attachOnce hands link to the switch at path as name's port. The returned
// connection keeps the port attached until it is closed or the switch goes.
func attachOnce(path, name, mac string, link *Link) (*net.UnixConn, error) {
	c, err := dial(path)
	if err != nil {
		return nil, err
	}
	var r reply
	req := request{Op: opAttach, Version: protocolVersion, Name: name, MAC: mac}
	if err := call(c, req, unix.UnixRights(link.sw), &r); err != nil {
		c.Close() //nolint:errcheck // already failing
		return nil, err
	}
	if r.Error != "" {
		c.Close() //nolint:errcheck // refused
		return nil, errors.New(r.Error)
	}
	_ = c.SetDeadline(time.Time{})
	return c, nil
}
