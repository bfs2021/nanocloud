package vzvm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/vswitch"
)

// A helper process (`nanocloud vz-machine`) owns one running VM. The daemon
// starts it in its own session so a daemon restart leaves the guest up. It
// exits once the VM stops for good, leaving a status file that says why.

// rebootWindow is how recently the guest must have announced a reboot for a
// stop to count as one; the machine stops up to a minute after the announcement.
var rebootWindow = 2 * time.Minute

// hostStopGrace is how long a helper told to quit gives its guest to power
// off before forcing it.
var hostStopGrace = 15 * time.Second

type helper struct {
	spec   HelperSpec
	launch Launcher
	hub    *serialHub
	link   *os.File // the NIC's link into the switch; nil on Apple's NAT

	ln       *net.UnixListener
	sockInfo os.FileInfo
	sockOnce sync.Once

	mu       sync.Mutex
	machine  Machine
	port     *serialPort
	ev       event
	intent   stopIntent
	watchers map[chan event]struct{}
}

type stopIntent int

const (
	intentNone stopIntent = iota
	intentGraceful
	intentDestroy
	intentHost
)

// RunHelper boots the VM spec describes and serves it until it stops for
// good. ready is called exactly once: with nil once the guest is booting,
// or with the reason it could not be. Cancelling ctx shuts the guest down
// (gracefully, then by force) as when the Mac itself shuts down.
func RunHelper(ctx context.Context, spec HelperSpec, launch Launcher, ready func(error)) error {
	h := &helper{spec: spec, launch: launch, hub: newSerialHub(), watchers: make(map[chan event]struct{})}

	// A socket left by a helper that died without cleaning up.
	_ = os.Remove(spec.Socket)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: spec.Socket, Net: "unix"})
	if err != nil {
		err = fmt.Errorf("listen on %s: %w", spec.Socket, err)
		ready(err)
		return err
	}
	// Closing the listener would unlink whatever socket is at the path by then;
	// closeSocket removes it only while it is still ours.
	ln.SetUnlinkOnClose(false)
	h.ln = ln
	h.sockInfo, _ = os.Stat(spec.Socket)
	_ = os.Chmod(spec.Socket, 0o600)
	defer h.closeSocket()
	go h.serve(ln)

	if spec.Switch != "" {
		link, lerr := vswitch.NewLink()
		if lerr != nil {
			lerr = fmt.Errorf("network link: %w", lerr)
			_ = writeStatus(spec.Status, reasonFailed)
			ready(lerr)
			return lerr
		}
		actx, cancel := context.WithCancel(context.Background())
		attached := make(chan struct{})
		go func() {
			vswitch.KeepAttached(actx, spec.Switch, spec.Machine.Name, spec.Machine.MAC, link)
			close(attached)
		}()
		defer func() {
			cancel()
			<-attached   // done with the link before it is closed
			link.Close() //nolint:errcheck // the VM is gone
		}()
		h.link = link.VM
	}

	if err := h.boot(); err != nil {
		h.hub.close()
		_ = writeStatus(spec.Status, reasonFailed)
		ready(err)
		return err
	}
	ready(nil)
	return h.run(ctx)
}

// boot builds and starts a machine on fresh console pipes.
func (h *helper) boot() error {
	port, err := h.hub.newPort()
	if err != nil {
		return fmt.Errorf("console: %w", err)
	}
	select { // a ready report from the previous boot says nothing about this one
	case <-h.hub.ready:
	default:
	}
	m, err := h.launch(MachineSpec{MachineConfig: h.spec.Machine, SerialIn: port.guestIn, SerialOut: port.guestOut, Network: h.link})
	if err != nil {
		h.hub.endPort(port)
		return err
	}
	if err := m.Start(); err != nil {
		h.hub.endPort(port)
		return err
	}
	h.mu.Lock()
	h.machine, h.port = m, port
	h.ev = event{State: m.State(), StartedAt: time.Now().UnixNano()}
	ev := h.ev
	h.mu.Unlock()
	h.publish(ev)
	return nil
}

// run follows the machine until the VM stops for good.
func (h *helper) run(ctx context.Context) error {
	ctxDone := ctx.Done()
	var force <-chan time.Time
	for {
		h.mu.Lock()
		m := h.machine
		h.mu.Unlock()

		select {
		case st := <-m.StateChanges():
			h.mu.Lock()
			h.ev.State = st
			ev := h.ev
			intent := h.intent
			h.mu.Unlock()
			if !st.terminal() {
				h.publish(ev)
				continue
			}
			reason := h.reasonFor(st, intent)
			if reason == "" {
				if err := h.reboot(); err != nil {
					log.Printf("vz-machine %s: reboot failed: %v", h.spec.Machine.Name, err)
					return h.finish(reasonFailed)
				}
				continue
			}
			return h.finish(reason)

		case r := <-h.hub.ready:
			log.Printf("vz-machine %s: guest is up (address %q, cloud-init %q)", h.spec.Machine.Name, r.IP, r.Provisioned)
			h.mu.Lock()
			h.ev.GuestIP, h.ev.Provisioned = r.IP, r.Provisioned
			ev := h.ev
			h.mu.Unlock()
			h.publish(ev)

		case <-ctxDone:
			ctxDone = nil
			h.setIntent(intentHost)
			if err := m.RequestStop(); err != nil {
				log.Printf("vz-machine %s: asking the guest to stop: %v", h.spec.Machine.Name, err)
			}
			force = time.After(hostStopGrace)

		case <-force:
			force = nil
			log.Printf("vz-machine %s: guest still running after %s; forcing it off", h.spec.Machine.Name, hostStopGrace)
			_ = m.Stop()
		}
	}
}

// reasonFor says why the machine stopped, or "" when the guest rebooted and
// the VM should come straight back.
func (h *helper) reasonFor(st MachineState, intent stopIntent) string {
	switch {
	case intent == intentDestroy:
		return reasonDestroyed
	case intent == intentHost:
		return reasonHost
	case st == MachineError:
		return reasonCrashed
	case intent == intentGraceful:
		return reasonShutdown
	case h.hub.rebootRequested(rebootWindow):
		return ""
	default:
		return reasonShutdown
	}
}

// reboot boots a fresh machine after the guest rebooted. A stop or destroy
// requested meanwhile is applied to the new machine straight away.
func (h *helper) reboot() error {
	log.Printf("vz-machine %s: guest rebooted; starting it again", h.spec.Machine.Name)
	h.mu.Lock()
	old := h.port
	h.port = nil
	h.ev.Rebooting = true
	h.ev.GuestIP, h.ev.Provisioned = "", "" // the old boot's report; the next boot sends its own
	ev := h.ev
	h.mu.Unlock()
	h.publish(ev)
	if old != nil {
		h.hub.endPort(old)
	}

	if err := h.boot(); err != nil {
		return err
	}

	h.mu.Lock()
	m, intent := h.machine, h.intent
	h.mu.Unlock()
	switch intent {
	case intentDestroy:
		return m.Stop()
	case intentGraceful, intentHost:
		return m.RequestStop()
	}
	return nil
}

// closeSocket stops taking requests and removes the socket, if the file at
// that path is still the one this helper created: the next helper for the
// same VM may already be listening there.
func (h *helper) closeSocket() {
	h.sockOnce.Do(func() {
		h.ln.Close() //nolint:errcheck // shutting down
		if fi, err := os.Stat(h.spec.Socket); err == nil && h.sockInfo != nil && os.SameFile(fi, h.sockInfo) {
			os.Remove(h.spec.Socket) //nolint:errcheck // best-effort cleanup
		}
	})
}

// finish records why the VM stopped, tells every watcher, and releases the
// console. The socket goes first: once the daemon hears the VM stopped it
// may start it again straight away, and the new helper needs the path.
func (h *helper) finish(reason string) error {
	h.closeSocket()
	if err := writeStatus(h.spec.Status, reason); err != nil {
		log.Printf("vz-machine %s: writing status: %v", h.spec.Machine.Name, err)
	}
	h.mu.Lock()
	h.ev = event{State: MachineStopped, Final: true, Reason: reason}
	ev := h.ev
	watchers := h.watchers
	h.watchers = nil
	h.mu.Unlock()
	for ch := range watchers {
		select {
		case ch <- ev:
		case <-time.After(time.Second):
		}
		close(ch)
	}
	h.hub.close()
	log.Printf("vz-machine %s: stopped (%s)", h.spec.Machine.Name, reason)
	return nil
}

func (h *helper) setIntent(i stopIntent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// A destroy outranks everything; nothing downgrades to "none".
	if h.intent != intentDestroy && i != intentNone {
		h.intent = i
	}
}

// publish sends a non-final event to every watcher. State changes are rare,
// so a watcher that is not keeping up only misses intermediate states.
func (h *helper) publish(ev event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.watchers {
		select {
		case ch <- ev:
		default:
		}
	}
}

// subscribe returns a channel of events and the current one, or ok=false
// once the VM has stopped for good.
func (h *helper) subscribe() (ch chan event, current event, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.watchers == nil {
		return nil, h.ev, false
	}
	ch = make(chan event, 64)
	h.watchers[ch] = struct{}{}
	return ch, h.ev, true
}

func (h *helper) unsubscribe(ch chan event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.watchers[ch]; ok {
		delete(h.watchers, ch)
		close(ch)
	}
}

func (h *helper) serve(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go h.handle(c)
	}
}

func (h *helper) handle(c net.Conn) {
	defer c.Close() //nolint:errcheck // per-request connection

	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(c)
	line, err := br.ReadBytes('\n')
	if err != nil {
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	var req request
	enc := json.NewEncoder(c)
	if err := json.Unmarshal(line, &req); err != nil {
		_ = enc.Encode(reply{Error: "bad request"})
		return
	}

	h.mu.Lock()
	m, rebooting := h.machine, h.ev.Rebooting
	h.mu.Unlock()
	answer := func(err error) {
		if err != nil {
			_ = enc.Encode(reply{Error: err.Error()})
			return
		}
		_ = enc.Encode(reply{OK: true})
	}

	switch req.Op {
	case opInfo:
		h.mu.Lock()
		inf := helperInfo{reply: reply{OK: true}, PID: os.Getpid(), Active: h.spec.Active, Event: h.ev}
		h.mu.Unlock()
		_ = enc.Encode(inf)

	case opWatch:
		ch, current, ok := h.subscribe()
		if err := enc.Encode(current); err != nil || !ok {
			if ok {
				h.unsubscribe(ch)
			}
			return
		}
		// The daemon closing its end is the only thing that arrives on it.
		go func() {
			_, _ = io.Copy(io.Discard, br)
			h.unsubscribe(ch)
		}()
		for ev := range ch {
			if enc.Encode(ev) != nil {
				h.unsubscribe(ch)
				return
			}
		}

	case opStop, opDestroy, opPause, opResume:
		if m == nil {
			answer(errors.New("the VM is still starting"))
			return
		}
		h.machineOp(req.Op, m, rebooting, answer)

	case opSerial:
		viewer, err := h.hub.attach()
		answer(err)
		if err != nil {
			return
		}
		defer viewer.Close() //nolint:errcheck // detach on disconnect
		go func() {
			_, _ = io.Copy(viewer, br)
			c.Close() //nolint:errcheck // the browser went away; end the other direction too
		}()
		_, _ = io.Copy(c, viewer)

	default:
		answer(fmt.Errorf("unknown op %q", req.Op))
	}
}

// machineOp applies a power request. Mid-reboot, a stop or destroy is
// recorded and applied to the new machine as soon as it is up (see reboot).
func (h *helper) machineOp(op string, m Machine, rebooting bool, answer func(error)) {
	switch op {
	case opStop:
		h.setIntent(intentGraceful)
		if rebooting {
			answer(nil)
			return
		}
		answer(m.RequestStop())
	case opDestroy:
		h.setIntent(intentDestroy)
		if err := m.Stop(); err != nil && !rebooting && !m.State().terminal() {
			answer(err)
			return
		}
		answer(nil)
	case opPause:
		answer(m.Pause())
	case opResume:
		answer(m.Resume())
	}
}

// HelperMain is `nanocloud vz-machine`: it reads a HelperSpec from standard
// input, reports the boot result on file descriptor 3 (one JSON reply line),
// and runs the VM until it stops for good. SIGTERM and SIGINT shut the guest
// down as the Mac shutting down would. It returns the process exit code.
func HelperMain(launch Launcher) int {
	var spec HelperSpec
	if err := json.NewDecoder(os.Stdin).Decode(&spec); err != nil {
		log.Printf("vz-machine: reading spec: %v", err)
		return 2
	}
	readyFile := os.NewFile(3, "ready")
	ready := func(err error) {
		if readyFile == nil {
			return
		}
		r := reply{OK: err == nil}
		if err != nil {
			r.Error = err.Error()
		}
		_ = json.NewEncoder(readyFile).Encode(r)
		readyFile.Close() //nolint:errcheck // one line, then done
	}
	signal.Ignore(syscall.SIGHUP)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := RunHelper(ctx, spec, launch, ready); err != nil {
		log.Printf("vz-machine %s: %v", spec.Machine.Name, err)
		return 1
	}
	return 0
}
