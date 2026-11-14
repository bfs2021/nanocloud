package vzvm

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/cloudinit"
)

const testReady = cloudinit.OSCPrefix + "nanocloud-ready;ip=192.168.64.7;ci=done\a"

// A marker is found wherever the console output happens to be cut.
func TestMarkerScannerFindsMarkersAtEverySplit(t *testing.T) {
	stream := "boot noise\r\n" + testReady + "web login: " + cloudinit.RebootMarker + "bye"
	want := []string{"nanocloud-ready;ip=192.168.64.7;ci=done", cloudinit.RebootCommand}
	for cut := 0; cut <= len(stream); cut++ {
		var s markerScanner
		got := append(s.feed([]byte(stream[:cut])), s.feed([]byte(stream[cut:]))...)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("cut at %d: got %q, want %q", cut, got, want)
		}
	}
	var s markerScanner // and byte by byte
	var got []string
	for i := range len(stream) {
		got = append(got, s.feed([]byte{stream[i]})...)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("byte by byte: got %q, want %q", got, want)
	}
}

func TestMarkerScannerIgnoresWhatIsNotAMarker(t *testing.T) {
	var s markerScanner
	other := "\x1b]0;window title\a\x1b[1;32mgreen\x1b[0m"
	if got := s.feed([]byte(other)); len(got) != 0 {
		t.Errorf("found %q in ordinary terminal output", got)
	}
	// An unterminated prefix never pins more than maxMarker bytes, and a real
	// marker after it is still found.
	runaway := cloudinit.OSCPrefix + strings.Repeat("x", 2*maxMarker)
	if got := s.feed([]byte(runaway)); len(got) != 0 {
		t.Errorf("found %q in a runaway sequence", got)
	}
	if len(s.pending) > maxMarker {
		t.Errorf("holding %d bytes back", len(s.pending))
	}
	if got := s.feed([]byte(testReady)); len(got) != 1 {
		t.Errorf("after a runaway sequence: got %q", got)
	}
}

func TestReadyReportTakesOnlyWellFormedValues(t *testing.T) {
	for body, want := range map[string]readyReport{
		"nanocloud-ready;ip=192.168.64.7;ci=done":               {IP: "192.168.64.7", Provisioned: "done"},
		"nanocloud-ready;ip=;ci=error":                          {Provisioned: "error"},
		"nanocloud-ready;ip=fe80::1;ci=done":                    {Provisioned: "done"}, // IPv4 only
		"nanocloud-ready;ip=192.168.64.7;ci=Done!":              {IP: "192.168.64.7"},
		"nanocloud-ready;ip=999.1.1.1;ci=done":                  {Provisioned: "done"},
		"nanocloud-ready;ip=10.0.0.5;ci=done;x=y":               {IP: "10.0.0.5", Provisioned: "done"},
		"nanocloud-ready;ip=10.0.0.5;ci=notarealwordthatislong": {IP: "10.0.0.5"},
	} {
		cmd, fields := parseMarker(body)
		if cmd != cloudinit.ReadyCommand {
			t.Fatalf("%q: command %q", body, cmd)
		}
		if got := readyFrom(fields); got != want {
			t.Errorf("%q: got %+v, want %+v", body, got, want)
		}
	}
}

// The hub hands the latest ready report to the helper and still sees reboots.
func TestSerialHubReportsReadiness(t *testing.T) {
	hub := newSerialHub()
	hub.broadcast([]byte(cloudinit.OSCPrefix + "nanocloud-ready;ip=10.0.0.1;ci=done\a"))
	hub.broadcast([]byte(testReady)) // a newer report replaces one not yet taken
	select {
	case r := <-hub.ready:
		if r.IP != "192.168.64.7" || r.Provisioned != "done" {
			t.Errorf("report = %+v", r)
		}
	default:
		t.Fatal("no ready report")
	}
	select {
	case r := <-hub.ready:
		t.Errorf("a second report %+v was queued", r)
	default:
	}
	hub.broadcast([]byte(cloudinit.RebootMarker))
	if !hub.rebootRequested(0x7fffffff) {
		t.Error("the reboot marker was missed")
	}
}
