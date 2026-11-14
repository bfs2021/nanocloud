//go:build darwin && !cgo

package main

import (
	"log"

	"github.com/nanocloud-io/nanocloud/internal/config"
	"github.com/nanocloud-io/nanocloud/internal/hypervisor"
	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

// Virtualization.framework is reached through cgo, so a CGO_ENABLED=0 macOS
// build cannot run VMs. It still compiles, so `GOOS=darwin go build` and
// `go vet` work from any machine; it refuses to serve.

// fatalf is log.Fatalf, behind a variable so static analysis of main does
// not conclude that everything after newBackend is unreachable.
var fatalf = log.Fatalf

func newBackend(*config.Config, string) hypervisor.Backend {
	fatalf("this nanocloud binary was built without cgo and cannot run VMs on macOS; " +
		"build it on a Mac with CGO_ENABLED=1 (make cross-darwin-arm64)")
	return nil
}

func adoptSpecs(hypervisor.Backend, *metadata.Store) {}

func platformCommand(string) bool { return false }
