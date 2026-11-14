//go:build darwin || linux

package vswitch

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/version"
)

// startTimeout bounds how long Ensure waits for a new switch to answer.
const startTimeout = 15 * time.Second

// Supervisor keeps the switch running for the daemon. The switch runs in its
// own session, so a daemon restart leaves every VM's network up.
type Supervisor struct {
	Socket  string
	LogPath string

	mu sync.Mutex
}

// Ensure starts the switch unless one from this build already answers. One
// from another build is asked to quit and replaced; helpers reattach within
// seconds, so guests only lose packets briefly.
func (s *Supervisor) Ensure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, err := Ping(s.Socket); err == nil {
		if r.Version == version.Version {
			return nil
		}
		log.Printf("vswitch: replacing the %s switch (pid %d) with %s", r.Version, r.PID, version.Version)
		if err := Quit(s.Socket); err != nil {
			return fmt.Errorf("stop the old switch: %w", err)
		}
		if !waitUntil(func() bool { _, err := Ping(s.Socket); return err != nil }) {
			return fmt.Errorf("the old switch (pid %d) did not stop", r.PID)
		}
	}
	if err := s.spawn(); err != nil {
		return err
	}
	if !waitUntil(func() bool { _, err := Ping(s.Socket); return err == nil }) {
		return fmt.Errorf("the switch did not start within %s; see %s", startTimeout, s.LogPath)
	}
	return nil
}

func (s *Supervisor) spawn() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find this binary: %w", err)
	}
	if err = os.MkdirAll(filepath.Dir(s.LogPath), 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(s.LogPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close() //nolint:errcheck // the child holds its own descriptor
	cmd := exec.Command(exe, "vz-switch", s.Socket)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start the switch: %w", err)
	}
	// Reaped here while the daemon lives; launchd adopts it after that.
	go cmd.Wait() //nolint:errcheck // its log says why it exited
	return nil
}

func waitUntil(cond func() bool) bool {
	deadline := time.Now().Add(startTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
	return true
}
