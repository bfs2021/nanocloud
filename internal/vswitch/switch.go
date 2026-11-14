// Package vswitch is the virtual Ethernet switch that puts every VM on a Mac
// on one network, since Virtualization.framework's NAT attachment links each
// VM to the host only. Each VM port is a datagram socket carrying one frame per
// datagram; the uplink is a vmnet interface in shared mode.
//
// Forwarding is static, never learned: a VM port is registered with its NIC's
// MAC and may only send from it, so a guest cannot impersonate another VM.
package vswitch

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"sync"
	"sync/atomic"
)

// Port is one link into the switch.
type Port interface {
	// ReadFrame blocks for the next frame. An error ends the port.
	ReadFrame(p []byte) (int, error)
	// WriteFrame sends one frame without blocking: a port that cannot take
	// it now drops it (ErrBusy), so one slow guest never stalls the others.
	WriteFrame(p []byte) error
	Close() error
}

// ErrBusy is a frame dropped because its destination could not take it.
var ErrBusy = errors.New("vswitch: port busy")

const (
	headerLen = 14
	// MaxPorts bounds how many VMs one switch serves.
	MaxPorts = 1024
	// readBuf is larger than any frame a port may send, so an oversized one
	// is seen whole and dropped instead of arriving truncated.
	readBuf = 16 << 10
)

// MAC is an Ethernet address in comparable form.
type MAC [6]byte

func (m MAC) String() string { return net.HardwareAddr(m[:]).String() }

// ParseVMMAC parses the address of a VM's NIC, which must be a unicast,
// non-zero Ethernet address.
func ParseVMMAC(s string) (MAC, error) {
	hw, err := net.ParseMAC(s)
	if err != nil || len(hw) != 6 {
		return MAC{}, fmt.Errorf("bad MAC %q", s)
	}
	var m MAC
	copy(m[:], hw)
	if m[0]&1 != 0 || m == (MAC{}) {
		return MAC{}, fmt.Errorf("MAC %s is not a unicast address", m)
	}
	return m, nil
}

// Counters are one port's traffic, as seen from the switch.
type Counters struct {
	RxFrames uint64 `json:"rx_frames"`
	RxBytes  uint64 `json:"rx_bytes"`
	TxFrames uint64 `json:"tx_frames"`
	TxBytes  uint64 `json:"tx_bytes"`
	// Spoofed are frames sent with a source MAC the port may not use.
	Spoofed uint64 `json:"spoofed"`
	// Malformed are frames too short to be Ethernet or larger than MaxFrame.
	Malformed uint64 `json:"malformed"`
	// Dropped are frames for this port that it could not take.
	Dropped uint64 `json:"dropped"`
}

type counters struct {
	rxFrames, rxBytes, txFrames, txBytes, spoofed, malformed, dropped atomic.Uint64
}

func (c *counters) snapshot() Counters {
	return Counters{
		RxFrames: c.rxFrames.Load(), RxBytes: c.rxBytes.Load(),
		TxFrames: c.txFrames.Load(), TxBytes: c.txBytes.Load(),
		Spoofed: c.spoofed.Load(), Malformed: c.malformed.Load(), Dropped: c.dropped.Load(),
	}
}

type port struct {
	name   string
	mac    MAC
	uplink bool
	p      Port
	stats  counters
}

// Switch forwards frames between VM ports and the uplink.
type Switch struct {
	maxFrame int // the largest frame forwarded, header included

	mu     sync.RWMutex
	byName map[string]*port
	byMAC  map[MAC]*port
	uplink *port
}

// New makes a switch that forwards frames of up to maxFrame bytes.
func New(maxFrame int) *Switch {
	return &Switch{maxFrame: maxFrame, byName: make(map[string]*port), byMAC: make(map[MAC]*port)}
}

// SetUplink connects the uplink and forwards from it until it fails, which
// is returned: without an uplink the switch is no use, so its owner exits.
func (s *Switch) SetUplink(p Port) error {
	up := &port{name: "uplink", uplink: true, p: p}
	s.mu.Lock()
	s.uplink = up
	s.mu.Unlock()
	return s.pump(up)
}

// Attach connects a VM's port and forwards from it until the port fails or
// detach is called. Reattaching under the same name replaces the old port; a
// MAC already held by another VM is refused.
func (s *Switch) Attach(name string, mac MAC, p Port) (detach func(), err error) {
	vp := &port{name: name, mac: mac, p: p}
	s.mu.Lock()
	if other, ok := s.byMAC[mac]; ok && other.name != name {
		s.mu.Unlock()
		return nil, fmt.Errorf("MAC %s already belongs to VM %q", mac, other.name)
	}
	old := s.byName[name]
	if old == nil && len(s.byName) >= MaxPorts {
		s.mu.Unlock()
		return nil, fmt.Errorf("the switch already has %d ports", MaxPorts)
	}
	if old != nil {
		delete(s.byMAC, old.mac)
	}
	s.byName[name], s.byMAC[mac] = vp, vp
	s.mu.Unlock()
	if old != nil {
		s.stop(old)
	}
	go func() {
		if err := s.pump(vp); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
			log.Printf("vswitch: port %s: %v", name, err)
		}
		s.detach(vp)
	}()
	return func() { s.detach(vp) }, nil
}

// Close disconnects every port and the uplink.
func (s *Switch) Close() {
	s.mu.Lock()
	ports := make([]*port, 0, len(s.byName)+1)
	for _, p := range s.byName {
		ports = append(ports, p)
	}
	if s.uplink != nil {
		ports = append(ports, s.uplink)
	}
	s.byName, s.byMAC, s.uplink = make(map[string]*port), make(map[MAC]*port), nil
	s.mu.Unlock()
	for _, p := range ports {
		s.stop(p)
	}
}

// detach removes p if it is still the port registered under its name.
func (s *Switch) detach(p *port) {
	s.mu.Lock()
	if s.byName[p.name] == p {
		delete(s.byName, p.name)
		delete(s.byMAC, p.mac)
	}
	s.mu.Unlock()
	s.stop(p)
}

func (s *Switch) stop(p *port) {
	p.p.Close() //nolint:errcheck // the port is going away either way
}

func (s *Switch) pump(p *port) error {
	buf := make([]byte, readBuf)
	for {
		n, err := p.p.ReadFrame(buf)
		if err != nil {
			return err
		}
		s.forward(p, buf[:n])
	}
}

// forward delivers one frame that arrived on from.
func (s *Switch) forward(from *port, f []byte) {
	if len(f) < headerLen || len(f) > s.maxFrame {
		from.stats.malformed.Add(1)
		return
	}
	var dst, src MAC
	copy(dst[:], f[0:6])
	copy(src[:], f[6:12])

	s.mu.RLock()
	switch {
	case !from.uplink && src != from.mac:
		from.stats.spoofed.Add(1)
		s.mu.RUnlock()
		return
	case from.uplink && s.byMAC[src] != nil:
		// Nothing beyond the uplink may claim a VM's MAC, and forwarding a
		// VM's own frame back would loop it.
		from.stats.spoofed.Add(1)
		s.mu.RUnlock()
		return
	}
	from.stats.rxFrames.Add(1)
	from.stats.rxBytes.Add(uint64(len(f)))

	var out []*port
	if dst[0]&1 != 0 { // broadcast or multicast
		out = make([]*port, 0, len(s.byName)+1)
		for _, p := range s.byName {
			if p != from {
				out = append(out, p)
			}
		}
		if !from.uplink && s.uplink != nil {
			out = append(out, s.uplink)
		}
	} else if p := s.byMAC[dst]; p != nil {
		if p != from {
			out = []*port{p}
		}
	} else if !from.uplink && s.uplink != nil {
		out = []*port{s.uplink}
	}
	s.mu.RUnlock()

	for _, p := range out {
		if err := p.p.WriteFrame(f); err != nil {
			p.stats.dropped.Add(1)
			continue
		}
		p.stats.txFrames.Add(1)
		p.stats.txBytes.Add(uint64(len(f)))
	}
}

// PortStatus is one port as the switch sees it.
type PortStatus struct {
	Name string `json:"name"`
	MAC  string `json:"mac,omitempty"`
	Counters
}

// Status is a snapshot of the switch.
type Status struct {
	Uplink *PortStatus  `json:"uplink,omitempty"`
	Ports  []PortStatus `json:"ports"`
}

// Status reports every port and its counters, VMs sorted by name.
func (s *Switch) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := Status{Ports: make([]PortStatus, 0, len(s.byName))}
	if s.uplink != nil {
		st.Uplink = &PortStatus{Name: s.uplink.name, Counters: s.uplink.stats.snapshot()}
	}
	for _, p := range s.byName {
		st.Ports = append(st.Ports, PortStatus{Name: p.name, MAC: p.mac.String(), Counters: p.stats.snapshot()})
	}
	sort.Slice(st.Ports, func(i, j int) bool { return st.Ports[i].Name < st.Ports[j].Name })
	return st
}
