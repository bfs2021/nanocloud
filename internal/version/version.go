// Package version exposes the release this binary was built from. Version is
// set at link time:
//
//	go build -ldflags "-X github.com/nanocloud-io/nanocloud/internal/version.Version=v1.2.3"
//
// The frontend bundle gets its own version from web/package.json; the release
// workflow stamps both from the same tag.
package version

// Version is the release this binary was built from, or "dev" for local builds.
var Version = "dev"
