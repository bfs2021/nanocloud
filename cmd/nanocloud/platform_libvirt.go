//go:build !darwin

package main

import (
	"log"

	nanocloud "github.com/nanocloud-io/nanocloud"
	"github.com/nanocloud-io/nanocloud/internal/api"
	"github.com/nanocloud-io/nanocloud/internal/config"
	"github.com/nanocloud-io/nanocloud/internal/hostnet"
	"github.com/nanocloud-io/nanocloud/internal/hypervisor"
	"github.com/nanocloud-io/nanocloud/internal/image"
	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/network"
	"github.com/nanocloud-io/nanocloud/internal/vm"
)

// This file wires the libvirt/QEMU backend, used on every platform but macOS.

// defaultMetadataPath is the production path for the per-VM metadata store.
const defaultMetadataPath = "/var/lib/nano-cloud/metadata.json"

// defaultImageCacheDir is where pulled cloud images are stored.
const defaultImageCacheDir = "/var/lib/nano-cloud/images"

// newBackend connects to libvirt lazily. A failed first connect is logged, and
// every vm.Manager method reconnects on its own.
func newBackend(_ *config.Config, libvirtURI string) hypervisor.Backend {
	m := vm.NewLazy(libvirtURI)
	if connErr := m.Connect(); connErr != nil {
		log.Printf("warning: could not connect to libvirt at %q: %v — will retry automatically", libvirtURI, connErr)
	}
	return m
}

// adoptSpecs is a no-op: libvirt keeps its own domain definitions, and the
// reconcile loop rebuilds any it has lost.
func adoptSpecs(hypervisor.Backend, *metadata.Store) {}

// newImageManager lists the amd64 catalog.
func newImageManager(cacheDir string) *image.Manager {
	return image.NewManagerForArch(cacheDir, "amd64")
}

// newNetManager reads leases from libvirt's dnsmasq status file.
func newNetManager() *network.Manager {
	return network.New(network.DefaultLeasesPath)
}

// newHostNet provisions VLAN links and bridges with iproute2.
func newHostNet() api.VLANProvisioner {
	return hostnet.New()
}

// platformCommand runs subcommands only this platform has. Linux has none.
func platformCommand(string) bool { return false }

// serviceUnit is the systemd unit `nanocloud install` writes.
func serviceUnit() []byte {
	return nanocloud.SystemdUnit
}

// platformLibvirtURI is the URI Settings reports; libvirt is in use here.
func platformLibvirtURI(uri string) string { return uri }
