package vzvm

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

// The daemon and a helper talk over the helper's unix socket in JSON lines,
// one request and one reply per connection. "watch" then streams an event per
// state change until the VM stops; "serial" switches to the raw console.

const (
	opInfo    = "info"
	opWatch   = "watch"
	opStop    = "stop"
	opDestroy = "destroy"
	opPause   = "pause"
	opResume  = "resume"
	opSerial  = "serial"
)

// Why a helper's VM stopped for good, as recorded in its status file and
// final event. reasonCode maps them onto libvirt's shutoff reasons.
const (
	reasonShutdown  = "shutdown"  // the guest powered off, or Stop() asked it to
	reasonDestroyed = "destroyed" // Destroy() pulled the plug
	reasonCrashed   = "crashed"   // the framework reported an error state
	reasonFailed    = "failed"    // the machine would not boot
	reasonHost      = "host"      // the helper was told to quit (the Mac shutting down)
)

type request struct {
	Op string `json:"op"`
}

type reply struct {
	OK    bool   `json:"ok,omitempty"`
	Error string `json:"error,omitempty"`
}

func (r reply) err() error {
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return nil
}

// event is one state report from a helper.
type event struct {
	State MachineState `json:"state"`
	// Rebooting is set between the two boots of a guest reboot.
	Rebooting bool `json:"rebooting,omitempty"`
	// StartedAt is when the current boot began, in Unix nanoseconds.
	StartedAt int64 `json:"started_at,omitempty"`
	// GuestIP and Provisioned are the address and cloud-init progress the
	// guest reported on its console this boot; empty until it reports.
	GuestIP     string `json:"guest_ip,omitempty"`
	Provisioned string `json:"provisioned,omitempty"`
	// Final marks the last event: the VM has stopped for good, for Reason.
	Final  bool   `json:"final,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// helperInfo answers opInfo: enough for a daemon that has just restarted to
// pick a running VM back up.
type helperInfo struct {
	reply
	PID    int              `json:"pid"`
	Active metadata.VMEntry `json:"active"`
	Event  event            `json:"event"`
}

// statusFile is what a helper leaves behind when its VM stops for good, so a
// daemon that was not running at the time still learns why.
type statusFile struct {
	Reason string `json:"reason"`
	At     int64  `json:"at"`
}

// HelperSpec is everything a helper process needs, passed to it as JSON on
// its standard input.
type HelperSpec struct {
	Machine MachineConfig `json:"machine"`
	// Active is the spec the VM was booted from, reported back to the daemon
	// so "restart to apply" still works after the daemon restarts.
	Active metadata.VMEntry `json:"active"`
	// Socket is where the helper serves requests; Status is where it records
	// why the VM stopped.
	Socket string `json:"socket"`
	Status string `json:"status"`
	// Switch is the switch's control socket; empty puts the NIC on Apple's NAT.
	Switch string `json:"switch,omitempty"`
}

const dialTimeout = 2 * time.Second

// openHelper sends one request and reads the reply line. The returned reader
// holds anything the helper sent after it, for streaming ops.
func openHelper(sock, op string) (net.Conn, *bufio.Reader, error) {
	c, err := net.DialTimeout("unix", sock, dialTimeout)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (net.Conn, *bufio.Reader, error) {
		c.Close() //nolint:errcheck // already failing
		return nil, nil, err
	}
	if err := json.NewEncoder(c).Encode(request{Op: op}); err != nil {
		return fail(err)
	}
	return c, bufio.NewReader(c), nil
}

// callHelper sends one request and decodes its reply into out.
func callHelper(sock, op string, out any) error {
	c, br, err := openHelper(sock, op)
	if err != nil {
		return err
	}
	defer c.Close() //nolint:errcheck // one-shot connection
	if err = c.SetReadDeadline(time.Now().Add(stopTimeout)); err != nil {
		return err
	}
	line, err := br.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("helper %s: %w", op, err)
	}
	return json.Unmarshal(line, out)
}

// simpleCall sends an op whose reply carries only success or an error.
func simpleCall(sock, op string) error {
	var r reply
	if err := callHelper(sock, op, &r); err != nil {
		return err
	}
	return r.err()
}

// readStatus reads a helper's status file, if it left one.
func readStatus(path string) (statusFile, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return statusFile{}, false
	}
	var st statusFile
	if json.Unmarshal(data, &st) != nil || st.Reason == "" {
		return statusFile{}, false
	}
	return st, true
}

func writeStatus(path, reason string) error {
	data, err := json.Marshal(statusFile{Reason: reason, At: time.Now().Unix()})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// bufferedConn reads first what a bufio.Reader already pulled off the
// connection, then the connection itself.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }
