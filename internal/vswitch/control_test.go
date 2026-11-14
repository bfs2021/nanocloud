//go:build darwin || linux

package vswitch

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// shortDir is a temp dir whose socket paths fit macOS's 104-byte limit,
// which t.TempDir's paths do not.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "vsw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) }) //nolint:errcheck // test cleanup
	return d
}

// runSwitch runs a switch with a fake uplink at path until the test ends,
// and returns once it answers. stopped is closed when Run returns, with its
// error in *runErr.
func runSwitch(t *testing.T, path string) (uplink *fakePort, stopped <-chan struct{}, runErr *error) {
	t.Helper()
	uplink = newFakePort()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	runErr = new(error)
	go func() {
		*runErr = Run(ctx, path, uplink, 1514)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waitFor(t, "the switch to answer", func() bool { _, err := Ping(path); return err == nil })
	return uplink, done, runErr
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func ports(t *testing.T, path string) []string {
	t.Helper()
	r, err := Ping(path)
	if err != nil {
		return nil
	}
	var names []string
	for _, p := range r.Ports {
		names = append(names, p.Name)
	}
	return names
}

// vmConn is a test's view of a VM's NIC: its end of the link, with deadlines.
func vmConn(t *testing.T, l *Link) net.Conn {
	t.Helper()
	c, err := net.FileConn(l.VM)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() }) //nolint:errcheck // test cleanup
	return c
}

// attachVM makes a link for name and keeps it attached until the test ends.
func attachVM(t *testing.T, path, name string, m MAC) net.Conn {
	t.Helper()
	l, err := NewLink()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		KeepAttached(ctx, path, name, m.String(), l)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		l.Close() //nolint:errcheck // test cleanup
	})
	return vmConn(t, l)
}

func readFrame(t *testing.T, c net.Conn) []byte {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("no frame arrived: %v", err)
	}
	return buf[:n]
}

func TestLinksCarryFramesBetweenVMs(t *testing.T) {
	path := filepath.Join(shortDir(t), "sw.sock")
	uplink, _, _ := runSwitch(t, path)
	macA, macB := MAC{0x52, 0x54, 0, 0, 0, 0xa}, MAC{0x52, 0x54, 0, 0, 0, 0xb}
	a, b := attachVM(t, path, "a", macA), attachVM(t, path, "b", macB)
	waitFor(t, "both VMs to attach", func() bool { return len(ports(t, path)) == 2 })

	unicast := frame(macB, macA, "hello b")
	if _, err := a.Write(unicast); err != nil {
		t.Fatal(err)
	}
	if got := readFrame(t, b); !bytes.Equal(got, unicast) {
		t.Errorf("b got %x, want %x", got, unicast)
	}

	reply := frame(broadcast, macB, "who has?")
	if _, err := b.Write(reply); err != nil {
		t.Fatal(err)
	}
	if got := readFrame(t, a); !bytes.Equal(got, reply) {
		t.Errorf("a got %x, want the broadcast", got)
	}
	waitFor(t, "the broadcast to reach the host", func() bool { return len(uplink.got()) == 1 })
}

func TestSwitchRestartReattaches(t *testing.T) {
	path := filepath.Join(shortDir(t), "sw.sock")
	_, stopped, runErr := runSwitch(t, path)
	macA := MAC{0x52, 0x54, 0, 0, 0, 0xa}
	a := attachVM(t, path, "a", macA)
	waitFor(t, "a to attach", func() bool { return len(ports(t, path)) == 1 })

	if err := Quit(path); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
		if *runErr != nil {
			t.Fatalf("switch stopped with %v", *runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the switch did not quit")
	}

	uplink, _, _ := runSwitch(t, path)
	waitFor(t, "a to attach to the new switch", func() bool { return len(ports(t, path)) == 1 })
	out := frame(gateway, macA, "still connected")
	if _, err := a.Write(out); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the frame to reach the new switch's host side", func() bool {
		for _, g := range uplink.got() {
			if bytes.Equal(g, out) {
				return true
			}
		}
		return false
	})
}

func TestAttachRefusals(t *testing.T) {
	path := filepath.Join(shortDir(t), "sw.sock")
	runSwitch(t, path)
	ok := request{Op: opAttach, Version: protocolVersion, Name: "vm", MAC: "52:54:00:00:00:01"}
	with := func(f func(*request)) request { r := ok; f(&r); return r }
	tests := []struct {
		name  string
		req   request
		links int
	}{
		{"an unknown protocol version", with(func(r *request) { r.Version = 99 }), 1},
		{"a name that is not a VM name", with(func(r *request) { r.Name = "../../etc" }), 1},
		{"a broadcast MAC", with(func(r *request) { r.MAC = "ff:ff:ff:ff:ff:ff" }), 1},
		{"no link", ok, 0},
		{"two links", ok, 2},
		{"a fistful of links", ok, 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var links []*Link
			var fds []int
			for range tt.links {
				l, err := NewLink()
				if err != nil {
					t.Fatal(err)
				}
				links = append(links, l)
				fds = append(fds, l.sw)
			}
			// The switch runs in this process, so a descriptor it kept from a
			// refused request shows up here.
			before := openFDs(t)
			c, err := dial(path)
			if err != nil {
				t.Fatal(err)
			}
			var rights []byte
			if len(fds) > 0 {
				rights = unix.UnixRights(fds...)
			}
			var r reply
			if err := call(c, tt.req, rights, &r); err != nil {
				t.Fatal(err)
			}
			c.Close() //nolint:errcheck // done
			if r.Error == "" {
				t.Fatal("the switch accepted it")
			}
			if got := ports(t, path); len(got) != 0 {
				t.Errorf("ports %v attached", got)
			}
			waitFor(t, "the switch to close everything the refused request carried", func() bool {
				return openFDs(t) <= before
			})
			for _, l := range links {
				l.Close() //nolint:errcheck // test cleanup
			}
		})
	}
}

// openFDs counts this process's open descriptors.
func openFDs(t *testing.T) int {
	t.Helper()
	n := 0
	for fd := range 4096 {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
			n++
		}
	}
	return n
}

func TestListen(t *testing.T) {
	t.Run("refuses while another switch answers", func(t *testing.T) {
		path := filepath.Join(shortDir(t), "sw.sock")
		runSwitch(t, path)
		if _, err := Listen(path, New(1514)); err == nil {
			t.Fatal("took the socket of a running switch")
		}
	})

	t.Run("replaces a socket left by a dead switch", func(t *testing.T) {
		path := filepath.Join(shortDir(t), "sw.sock")
		ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		ln.SetUnlinkOnClose(false)
		ln.Close() //nolint:errcheck // leaves the socket file behind, as a crash would
		srv, err := Listen(path, New(1514))
		if err != nil {
			t.Fatal(err)
		}
		srv.Close()
	})

	t.Run("will not remove a file that is not a socket", func(t *testing.T) {
		path := filepath.Join(shortDir(t), "sw.sock")
		if err := os.WriteFile(path, []byte("keep me"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Listen(path, New(1514)); err == nil {
			t.Fatal("listened over a regular file")
		}
		if data, _ := os.ReadFile(path); string(data) != "keep me" {
			t.Error("the file was replaced")
		}
	})

	t.Run("the socket is private to its owner", func(t *testing.T) {
		path := filepath.Join(shortDir(t), "sw.sock")
		runSwitch(t, path)
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("socket mode %o, want 600", perm)
		}
	})
}

func TestStatusReportsVersionAndPorts(t *testing.T) {
	path := filepath.Join(shortDir(t), "sw.sock")
	runSwitch(t, path)
	attachVM(t, path, "web-1", MAC{0x52, 0x54, 0, 0, 0, 7})
	waitFor(t, "web-1 to attach", func() bool { return len(ports(t, path)) == 1 })
	r, err := Ping(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Version == "" || r.PID != os.Getpid() || r.Uplink == nil {
		t.Errorf("report = %+v, want a version, this PID and an uplink", r)
	}
	if r.Ports[0].Name != "web-1" || r.Ports[0].MAC != "52:54:00:00:00:07" {
		t.Errorf("port = %+v", r.Ports[0])
	}
}

func TestPingWithoutSwitch(t *testing.T) {
	if _, err := Ping(filepath.Join(shortDir(t), "none.sock")); err == nil || errors.Is(err, os.ErrExist) {
		t.Fatalf("Ping = %v, want a dial error", err)
	}
}
