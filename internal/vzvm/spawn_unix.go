//go:build unix

package vzvm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// bootTimeout bounds how long Start waits for a helper to report that its VM
// is booting.
const bootTimeout = 2 * time.Minute

// ProcessSpawner starts helpers as `<this binary> vz-machine <name>`, each in
// its own session so launchd stopping the daemon's process group leaves them
// running. Each helper logs to <logDir>/<name>.log.
func ProcessSpawner(logDir string) Spawner {
	return func(spec HelperSpec) error {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("find this binary: %w", err)
		}
		input, err := json.Marshal(spec)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(logDir, 0o755); err != nil {
			return err
		}
		logf, err := os.OpenFile(filepath.Join(logDir, spec.Machine.Name+".log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		defer logf.Close() //nolint:errcheck // the child holds its own descriptor
		readyR, readyW, err := os.Pipe()
		if err != nil {
			return err
		}
		defer readyR.Close() //nolint:errcheck // read once below

		cmd := exec.Command(exe, "vz-machine", spec.Machine.Name)
		cmd.Stdin = bytes.NewReader(input)
		cmd.Stdout, cmd.Stderr = logf, logf
		cmd.ExtraFiles = []*os.File{readyW} // fd 3 in the helper
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			readyW.Close() //nolint:errcheck // already failing
			return fmt.Errorf("start helper: %w", err)
		}
		readyW.Close() //nolint:errcheck // the child has its copy
		// Reap it if it exits while the daemon is still around; once the
		// daemon is gone the helper is re-parented and reaped by launchd.
		go cmd.Wait() //nolint:errcheck // exit status is reported via the status file

		result := make(chan error, 1)
		go func() {
			line, err := bufio.NewReader(readyR).ReadBytes('\n')
			if err != nil {
				result <- errNotStarted
				return
			}
			var r reply
			if err := json.Unmarshal(line, &r); err != nil {
				result <- fmt.Errorf("helper sent %q: %w", line, err)
				return
			}
			result <- r.err()
		}()
		select {
		case err := <-result:
			return err
		case <-time.After(bootTimeout):
			_ = cmd.Process.Kill()
			return fmt.Errorf("helper did not report within %s", bootTimeout)
		}
	}
}
