//go:build darwin || linux

package vswitch

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// Socket buffer sizes for a VM's link. Virtualization.framework wants the
// receive buffer at least twice the send buffer, ideally four times.
const (
	sndBuf = 1 << 20
	rcvBuf = 4 << 20
)

// Link is one VM's cable into the switch: a datagram socket pair. The VM's
// NIC uses VM; the other end is kept so it can be handed to a restarted
// switch without the guest's link dropping.
type Link struct {
	VM *os.File
	sw int
}

// NewLink makes a VM's link.
func NewLink() (*Link, error) {
	syscall.ForkLock.RLock() // no child may inherit the pair before it is close-on-exec
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err == nil {
		unix.CloseOnExec(fds[0])
		unix.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("socketpair: %w", err)
	}
	for _, fd := range fds {
		// Best effort: the defaults work, only more slowly under load.
		_ = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_SNDBUF, sndBuf)
		_ = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, rcvBuf)
	}
	// The switch writes without blocking; the descriptor it receives shares
	// these flags.
	if err := unix.SetNonblock(fds[1], true); err != nil {
		unix.Close(fds[0]) //nolint:errcheck // failing
		unix.Close(fds[1]) //nolint:errcheck // failing
		return nil, err
	}
	return &Link{VM: os.NewFile(uintptr(fds[0]), "vm-link"), sw: fds[1]}, nil
}

// Close closes both ends.
func (l *Link) Close() error {
	unix.Close(l.sw) //nolint:errcheck // closing
	return l.VM.Close()
}

// dgramPort is the switch's end of a VM's link.
type dgramPort struct {
	c   *net.UnixConn
	raw syscall.RawConn
}

// newDgramPort makes a port of a duplicate of f, which must be a unix
// datagram socket.
func newDgramPort(f *os.File) (*dgramPort, error) {
	if err := checkDatagram(f); err != nil {
		return nil, err
	}
	c, err := net.FileConn(f)
	if err != nil {
		return nil, fmt.Errorf("link: %w", err)
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		c.Close() //nolint:errcheck // rejected
		return nil, errors.New("link is not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		uc.Close() //nolint:errcheck // rejected
		return nil, err
	}
	return &dgramPort{c: uc, raw: raw}, nil
}

// checkDatagram refuses anything but a unix datagram socket. It reads the
// descriptor without File.Fd, which would switch it to blocking mode.
func checkDatagram(f *os.File) error {
	raw, err := f.SyscallConn()
	if err != nil {
		return fmt.Errorf("link: %w", err)
	}
	var typ int
	var sa unix.Sockaddr
	var serr error
	if err := raw.Control(func(fd uintptr) {
		if typ, serr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_TYPE); serr == nil {
			sa, serr = unix.Getsockname(int(fd))
		}
	}); err != nil {
		return fmt.Errorf("link: %w", err)
	}
	if serr != nil {
		return fmt.Errorf("link: %w", serr)
	}
	if _, ok := sa.(*unix.SockaddrUnix); !ok || typ != unix.SOCK_DGRAM {
		return errors.New("link is not a unix datagram socket")
	}
	return nil
}

// ReadFrame reads one datagram. A departed VM is noticed by its control
// connection closing, because a blocked datagram read is not reliably woken
// when the peer closes.
func (p *dgramPort) ReadFrame(b []byte) (int, error) { return p.c.Read(b) }

// WriteFrame makes a single attempt: a VM that is not reading has its frames
// dropped rather than holding up the switch.
func (p *dgramPort) WriteFrame(b []byte) error {
	var werr error
	err := p.raw.Write(func(fd uintptr) bool {
		_, werr = unix.Write(int(fd), b)
		return true
	})
	if err != nil {
		return err
	}
	if errors.Is(werr, unix.EAGAIN) || errors.Is(werr, unix.ENOBUFS) {
		return ErrBusy
	}
	return werr
}

func (p *dgramPort) Close() error { return p.c.Close() }
