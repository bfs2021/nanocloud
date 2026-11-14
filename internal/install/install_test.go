//go:build !darwin

package install

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestServiceActivationRestarts guards the upgrade path: `start` on an active
// unit is a no-op, so the old daemon would keep running after an upgrade.
func TestServiceActivationRestarts(t *testing.T) {
	args := serviceActivationArgs()

	if len(args) == 0 {
		t.Fatal("serviceActivationArgs() is empty — nothing would activate the service")
	}

	last := args[len(args)-1]
	if len(last) != 2 || last[0] != "restart" || last[1] != serviceName {
		t.Errorf("final systemctl verb = %v, want [restart %s]\n"+
			"A bare \"start\" is a no-op on an already-active unit, so an upgrade "+
			"keeps serving the old binary's inode.", last, serviceName)
	}

	// "restart" also starts an inactive unit, so "start" is never needed.
	for _, a := range args {
		if len(a) > 0 && a[0] == "start" {
			t.Errorf("serviceActivationArgs() contains a bare \"start\" (%v); use \"restart\"", a)
		}
	}
}

// TestServiceActivationReloadsBeforeEnable checks systemd re-reads the freshly
// written unit file before enabling or restarting it.
func TestServiceActivationReloadsBeforeEnable(t *testing.T) {
	args := serviceActivationArgs()
	if len(args) < 1 || len(args[0]) != 1 || args[0][0] != "daemon-reload" {
		t.Fatalf("first systemctl call = %v, want [daemon-reload]", args)
	}
}

func TestWaitForHealthy(t *testing.T) {
	t.Run("returns true once the endpoint answers 200", func(t *testing.T) {
		var calls int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			// Fail the first probe to prove the poll retries.
			if calls < 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		if !waitForHealthy(srv.URL, "", 5, time.Millisecond) {
			t.Error("waitForHealthy() = false, want true after the endpoint recovered")
		}
	})

	t.Run("returns false when nothing is listening", func(t *testing.T) {
		// A closed server's URL refuses connections: active but not serving.
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close()

		if waitForHealthy(url, "", 2, time.Millisecond) {
			t.Error("waitForHealthy() = true against a closed listener, want false")
		}
	})
}

// A VM's disk is an overlay on its cached image, so an uninstall that keeps
// the VMs must keep the images too.
func TestUninstallKeepsWhatVMsNeed(t *testing.T) {
	for _, removed := range uninstallRemoves() {
		for _, needed := range []string{dataDir, imagesDir, vmDisksDir} {
			if needed == removed || strings.HasPrefix(needed, removed+"/") {
				t.Errorf("uninstall removes %s, which takes %s with it", removed, needed)
			}
		}
	}
}
