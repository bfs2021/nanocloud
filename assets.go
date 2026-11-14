package nanocloud

import "embed"

// WebFS holds the embedded production build of the React frontend.
//
//go:embed web/dist
var WebFS embed.FS

// SystemdUnit is the nanocloud.service unit file written by nanocloud install.
//
//go:embed systemd/nanocloud.service
var SystemdUnit []byte

// LaunchdPlist is the launchd job `nanocloud install` writes on macOS, the
// counterpart of SystemdUnit.
//
//go:embed launchd/io.nanocloud.nanocloud.plist
var LaunchdPlist []byte

// VZEntitlements is what the macOS binary must be code-signed with for
// Virtualization.framework to run VMs. `make cross-darwin-arm64` signs with
// it, and `nanocloud install` re-signs a binary that lacks it.
//
//go:embed darwin/entitlements.plist
var VZEntitlements []byte
