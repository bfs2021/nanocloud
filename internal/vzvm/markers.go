package vzvm

import (
	"bytes"
	"net"
	"strings"

	"github.com/nanocloud-io/nanocloud/internal/cloudinit"
)

// Guests created on a Mac print short markers on their console for the host.
// Each is an OSC escape sequence (cloudinit.OSCPrefix … BEL), which terminals
// ignore.

// maxMarker bounds how much console output is held back waiting for a
// marker's BEL. Real markers are under 100 bytes.
const maxMarker = 512

// markerScanner finds complete markers in console output that may arrive in
// arbitrary pieces, including split in the middle of a marker.
type markerScanner struct {
	// pending is the end of the output seen so far that may still turn into
	// a marker: an unterminated one, or a partial prefix.
	pending []byte
}

// feed scans p after anything held back from earlier calls and returns the
// bodies of the markers it completes, in order.
func (s *markerScanner) feed(p []byte) []string {
	buf := append(s.pending, p...)
	s.pending = nil
	prefix := []byte(cloudinit.OSCPrefix)
	var bodies []string
	for {
		i := bytes.Index(buf, prefix)
		if i < 0 {
			s.pending = append([]byte(nil), partialPrefix(buf, prefix)...)
			return bodies
		}
		rest := buf[i+len(prefix):]
		end := bytes.IndexByte(rest, '\a')
		if end < 0 {
			if len(buf)-i > maxMarker {
				buf = buf[i+1:] // not ours: look again past its ESC
				continue
			}
			s.pending = append([]byte(nil), buf[i:]...)
			return bodies
		}
		if end+len(prefix) <= maxMarker {
			bodies = append(bodies, string(rest[:end]))
		}
		buf = rest[end+1:]
	}
}

// partialPrefix returns the longest end of buf that is the start of prefix,
// so a prefix split across two reads is still found.
func partialPrefix(buf, prefix []byte) []byte {
	for n := min(len(buf), len(prefix)-1); n > 0; n-- {
		if bytes.Equal(buf[len(buf)-n:], prefix[:n]) {
			return buf[len(buf)-n:]
		}
	}
	return nil
}

// readyReport is what a guest's ready marker says.
type readyReport struct {
	// IP is the guest's IPv4 address, or "" when it had none to report.
	IP string
	// Provisioned is where cloud-init was when the guest reported: "running"
	// on a first boot, "done", "error", or "" when the guest could not tell.
	Provisioned string
}

// parseMarker splits a marker body into its command and key=value fields.
func parseMarker(body string) (cmd string, fields map[string]string) {
	parts := strings.Split(body, ";")
	fields = make(map[string]string, len(parts)-1)
	for _, f := range parts[1:] {
		if k, v, ok := strings.Cut(f, "="); ok {
			fields[k] = v
		}
	}
	return parts[0], fields
}

// readyFrom turns a ready marker's fields into a report. Console output is
// untrusted, so only a valid IPv4 address and a short lowercase word are kept.
func readyFrom(fields map[string]string) readyReport {
	var r readyReport
	if ip := net.ParseIP(fields["ip"]); ip != nil && ip.To4() != nil {
		r.IP = ip.String()
	}
	if ci := fields["ci"]; len(ci) <= 16 && strings.Trim(ci, "abcdefghijklmnopqrstuvwxyz") == "" {
		r.Provisioned = ci
	}
	return r
}
