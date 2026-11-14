package api

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/nanocloud-io/nanocloud/internal/hypervisor"
	"github.com/nanocloud-io/nanocloud/internal/vm"
)

// handleVMConsole handles GET /vms/{id}/console. On libvirt the WebSocket
// carries RFB from the VM's VNC server; Virtualization.framework has no VNC,
// so there it carries the raw serial stream. GET /system's capabilities tell
// the UI which to expect.
func (s *Server) handleVMConsole(w http.ResponseWriter, r *http.Request) {
	switch hv := s.vmManager.(type) {
	case hypervisor.VNCConsoler:
		s.proxyVNC(w, r, hv)
	case hypervisor.SerialConsoler:
		s.proxySerial(w, r, hv)
	default:
		http.Error(w, fmt.Sprintf("no console on the %s backend", s.capabilities().Backend), http.StatusNotImplemented)
	}
}

// proxyVNC upgrades the request to a WebSocket and relays bytes between the
// browser and the VM's VNC server, which listens on loopback only.
func (s *Server) proxyVNC(w http.ResponseWriter, r *http.Request, hv hypervisor.VNCConsoler) {
	id := r.PathValue("id")

	addr, err := hv.VNCAddress(id)
	if err != nil {
		// An unreachable libvirt is a 503, as elsewhere; any other failure means
		// the VM has no console right now.
		status := http.StatusConflict
		if errors.Is(err, vm.ErrNotConnected) {
			status = http.StatusServiceUnavailable
		}
		http.Error(w, fmt.Sprintf("console unavailable for vm %q: %v", id, err), status)
		return
	}

	vncConn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		http.Error(w, fmt.Sprintf("dial vnc for vm %q: %v", id, err), http.StatusBadGateway)
		return
	}
	defer vncConn.Close() //nolint:errcheck // best-effort cleanup on proxy teardown

	// Accept's default Origin check refuses a page on another site: the
	// session cookie would otherwise let it drive this console. The Vite dev
	// server proxies without rewriting Host, so it passes as same-origin.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the HTTP error response.
	}
	defer c.CloseNow() //nolint:errcheck // best-effort cleanup on proxy teardown

	wsConn := websocket.NetConn(r.Context(), c, websocket.MessageBinary)
	defer wsConn.Close() //nolint:errcheck // best-effort cleanup on proxy teardown

	// Whichever side closes first unblocks the other copy through the deferred
	// Close calls once this handler returns.
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(vncConn, wsConn) //nolint:errcheck // proxy loop; EOF/reset is expected teardown
		done <- struct{}{}
	}()
	go func() {
		io.Copy(wsConn, vncConn) //nolint:errcheck // proxy loop; EOF/reset is expected teardown
		done <- struct{}{}
	}()
	<-done
}

// proxySerial attaches the browser to the VM's serial console. The backend
// hands back a stream that replays recent output before going live, so a
// reopened tab shows the login prompt rather than a blank screen.
func (s *Server) proxySerial(w http.ResponseWriter, r *http.Request, hv hypervisor.SerialConsoler) {
	id := r.PathValue("id")

	serial, err := hv.OpenSerial(id)
	if err != nil {
		http.Error(w, fmt.Sprintf("console unavailable for vm %q: %v", id, err), http.StatusConflict)
		return
	}
	defer serial.Close() //nolint:errcheck // best-effort cleanup on proxy teardown

	// See proxyVNC on the Origin check.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the HTTP error response.
	}
	defer c.CloseNow() //nolint:errcheck // best-effort cleanup on proxy teardown

	wsConn := websocket.NetConn(r.Context(), c, websocket.MessageBinary)
	defer wsConn.Close() //nolint:errcheck // best-effort cleanup on proxy teardown

	done := make(chan struct{}, 2)
	go func() {
		io.Copy(serial, wsConn) //nolint:errcheck // proxy loop; EOF/reset is expected teardown
		done <- struct{}{}
	}()
	go func() {
		io.Copy(wsConn, serial) //nolint:errcheck // proxy loop; EOF/reset is expected teardown
		done <- struct{}{}
	}()
	<-done
}
