//go:build unix

package vzvm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/digitalocean/go-libvirt"
	"golang.org/x/sys/unix"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

// When the test binary is started by ProcessSpawner (as `<exe> vz-machine
// <name>`, with this variable inherited), it plays the helper process,
// running the real HelperMain against a fake machine.
const helperEnv = "VZVM_TEST_AS_HELPER"

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "boot":
		os.Exit(HelperMain(func(s MachineSpec) (Machine, error) {
			return &fakeMachine{spec: s, ch: make(chan MachineState, 64)}, nil
		}))
	case "fail":
		os.Exit(HelperMain(func(MachineSpec) (Machine, error) {
			return nil, errors.New("VZErrorDomain: the binary lacks com.apple.security.virtualization")
		}))
	}
	os.Exit(m.Run())
}

func processHarness(t *testing.T, mode string) (*Manager, string, string) {
	t.Helper()
	t.Setenv(helperEnv, mode)
	run, err := os.MkdirTemp("", "vz")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(run) })
	logs, pool := t.TempDir(), t.TempDir()
	m := New(Config{Spawn: ProcessSpawner(logs), RunDir: run})
	t.Cleanup(func() { _ = m.Close() })
	disk := filepath.Join(pool, "web.raw")
	if err := os.WriteFile(disk, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.DefineFromSpec(metadata.VMEntry{ID: "web", PoolPath: pool, VCPUs: 1, MemoryMB: 512}, disk, ""); err != nil {
		t.Fatal(err)
	}
	return m, run, logs
}

func TestProcessSpawnerRunsADetachedHelper(t *testing.T) {
	m, run, logs := processHarness(t, "boot")
	if err := m.Start("web"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy("web") })
	waitRunning(t, m, "web")

	var inf helperInfo
	if err := callHelper(filepath.Join(run, "web.sock"), opInfo, &inf); err != nil {
		t.Fatal(err)
	}
	if inf.PID == os.Getpid() {
		t.Fatal("the helper runs inside the daemon's process")
	}
	// In its own session, so launchd stopping the daemon's process group
	// leaves it running.
	sid, err := unix.Getsid(inf.PID)
	if err != nil {
		t.Fatal(err)
	}
	if mine, _ := unix.Getsid(0); sid == mine || sid != inf.PID {
		t.Errorf("helper session %d (daemon's %d), want a session of its own", sid, mine)
	}

	if err := m.Destroy("web"); err != nil {
		t.Fatal(err)
	}
	if got := waitStopped(t, m, "web"); got != libvirt.DomainShutoffDestroyed {
		t.Errorf("reason = %v, want destroyed", got)
	}
	if st, ok := readStatus(filepath.Join(run, "web.status")); !ok || st.Reason != reasonDestroyed {
		t.Errorf("status file = %+v, %v", st, ok)
	}
	eventually(t, "the helper to exit", func() bool { return syscall.Kill(inf.PID, 0) != nil })
	if log, _ := os.ReadFile(filepath.Join(logs, "web.log")); !strings.Contains(string(log), "stopped (destroyed)") {
		t.Errorf("helper log = %q", log)
	}
}

func TestProcessSpawnerReportsABootFailure(t *testing.T) {
	m, _, _ := processHarness(t, "fail")
	err := m.Start("web")
	if err == nil || !strings.Contains(err.Error(), "com.apple.security.virtualization") {
		t.Fatalf("Start = %v, want the helper's reason", err)
	}
	if o := observe(t, m, "web"); libvirt.DomainShutoffReason(o.Reason) != libvirt.DomainShutoffFailed {
		t.Errorf("observed %+v, want failed", o)
	}
}
