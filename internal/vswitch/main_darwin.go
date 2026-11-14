//go:build darwin && cgo

package vswitch

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
)

// Main is `nanocloud vz-switch <socket>`, the switch process the daemon
// starts. It runs until asked to quit, SIGTERM, or uplink failure, and
// returns the process exit code.
func Main(path string) int {
	if os.Geteuid() != 0 {
		// vmnet itself only answers "failure".
		log.Printf("vz-switch: must run as root: macOS's vmnet serves no one else")
		return 1
	}
	up, err := StartVmnet()
	if err != nil {
		log.Printf("vz-switch: uplink: %v", err)
		return 1
	}
	defer up.Close() //nolint:errcheck // exiting
	log.Printf("vz-switch: uplink up (MAC %s, MTU %d), serving %s", up.MAC, up.MTU, path)

	signal.Ignore(syscall.SIGHUP)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := Run(ctx, path, up, up.MaxFrame); err != nil {
		log.Printf("vz-switch: %v", err)
		return 1
	}
	log.Printf("vz-switch: stopped")
	return 0
}
