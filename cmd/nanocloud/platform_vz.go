//go:build darwin && cgo

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/nanocloud-io/nanocloud/internal/config"
	"github.com/nanocloud-io/nanocloud/internal/hypervisor"
	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/network"
	"github.com/nanocloud-io/nanocloud/internal/vswitch"
	"github.com/nanocloud-io/nanocloud/internal/vzvm"
)

// runDir holds each running VM's helper socket and each stopped VM's status
// file. macOS empties /var/run at boot, which is right for both.
const runDir = "/var/run/nanocloud"

// helperLogDir is where each VM's helper process logs.
const helperLogDir = "/Library/Logs/nanocloud/vms"

// switchSocket and switchLog are the control socket and log of the switch
// process (`nanocloud vz-switch`) that links every VM into one network.
const (
	switchSocket = runDir + "/vz-switch.sock"
	switchLog    = "/Library/Logs/nanocloud/vz-switch.log"
)

// newBackend builds the Virtualization.framework backend. Each VM runs in its
// own helper process, so VMs keep running across daemon restarts.
func newBackend(cfg *config.Config, _ string) hypervisor.Backend {
	vc := vzvm.Config{
		Spawn:  vzvm.ProcessSpawner(helperLogDir),
		RunDir: runDir,
		Leases: network.BootpdLeases(network.DefaultBootpdLeasesPath),
	}
	if cfg.VZNetwork != config.VZNetworkNAT {
		sup := &vswitch.Supervisor{Socket: switchSocket, LogPath: switchLog}
		vc.Switch, vc.EnsureSwitch = switchSocket, sup.Ensure
	}
	return vzvm.New(vc)
}

// adoptSpecs registers every VM in metadata.json with the backend, which keeps
// no definitions of its own, and picks up those whose helper still runs.
func adoptSpecs(hv hypervisor.Backend, store *metadata.Store) {
	m, ok := hv.(*vzvm.Manager)
	if !ok {
		return
	}
	entries, err := store.Read()
	if err != nil {
		log.Printf("warning: could not read VM specs to adopt: %v", err)
		return
	}
	m.AdoptSpecs(entries)
}

// platformCommand runs the processes the daemon starts, never by hand:
// `nanocloud vz-machine <name>`, one VM's helper, and `nanocloud vz-switch
// <socket>`, the switch every VM is linked into.
func platformCommand(cmd string) bool {
	switch cmd {
	case "vz-machine":
		log.SetPrefix(filepath.Base(os.Args[0]) + " vz-machine: ")
		os.Exit(vzvm.HelperMain(vzvm.Launch))
	case "vz-switch":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: nanocloud vz-switch <socket>")
			os.Exit(2)
		}
		log.SetPrefix(filepath.Base(os.Args[0]) + " vz-switch: ")
		os.Exit(vswitch.Main(os.Args[2]))
	}
	return false
}
