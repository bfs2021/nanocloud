package main

import (
	nanocloud "github.com/nanocloud-io/nanocloud"
	"github.com/nanocloud-io/nanocloud/internal/api"
	"github.com/nanocloud-io/nanocloud/internal/image"
	"github.com/nanocloud-io/nanocloud/internal/install"
	"github.com/nanocloud-io/nanocloud/internal/network"
)

// This file wires macOS: Apple's Virtualization.framework instead of libvirt.
// The backend itself is built in platform_vz.go, which needs cgo.

// dataDir keeps images, disks and metadata on one volume, so a new VM's disk
// is an instant APFS clone of its image. Matches internal/install's config.
const dataDir = "/Library/Application Support/nano-cloud"

// defaultMetadataPath is the production path for the per-VM metadata store.
const defaultMetadataPath = dataDir + "/metadata.json"

// defaultImageCacheDir is where pulled cloud images are stored.
const defaultImageCacheDir = dataDir + "/images"

func init() {
	// `nanocloud install` signs the binary with these when it lacks them.
	install.Entitlements = nanocloud.VZEntitlements
}

// newImageManager lists arm64 images: Virtualization.framework runs guests
// of the host's architecture only, and every Mac it supports is Apple silicon.
func newImageManager(cacheDir string) *image.Manager {
	return image.NewManagerForArch(cacheDir, "arm64")
}

// newNetManager reads NAT guests' leases from macOS's DHCP server.
func newNetManager() *network.Manager {
	return network.NewWithLeases(network.BootpdLeases(network.DefaultBootpdLeasesPath))
}

// newHostNet is nil: macOS has no VLAN provisioning yet.
func newHostNet() api.VLANProvisioner {
	return nil
}

// serviceUnit is the launchd job `nanocloud install` writes.
func serviceUnit() []byte {
	return nanocloud.LaunchdPlist
}

// platformLibvirtURI is empty: macOS does not use libvirt.
func platformLibvirtURI(string) string { return "" }
