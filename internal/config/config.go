// Package config parses and validates nano-cloud configuration from a YAML file.
package config

import (
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

// DefaultPath is the production config file location, written by nanocloud install.
const DefaultPath = "/etc/nano-cloud/config.yaml"

// Config holds application configuration parsed from config.yaml. It is parsed
// once at startup and passed down explicitly.
//
// Optional blocks that `install` never writes must keep defaulting sensibly
// when absent, so an older binary can still read a newer config.
type Config struct {
	// StoragePools lists the host paths available for VM disk storage.
	StoragePools []StoragePool `yaml:"storage_pools"`
	// LibvirtURI is the libvirt connection URI (e.g. "qemu:///system").
	LibvirtURI string `yaml:"libvirt_uri"`
	// Port is the TCP port the HTTP server listens on. Defaults to 8080 if zero.
	Port int `yaml:"port"`
	// ImageCacheDir is where pulled cloud images are stored.
	// Defaults to /var/lib/nano-cloud/images when empty.
	ImageCacheDir string `yaml:"image_cache_dir"`
	// MetadataPath is the per-VM spec file. Defaults to
	// /var/lib/nano-cloud/metadata.json when empty. It must be writable, or VM
	// start and stop fail.
	MetadataPath string `yaml:"metadata_path"`
	// Reconcile configures the self-healing loop.
	Reconcile ReconcileConfig `yaml:"reconcile"`
	// VZNetwork is how VMs are networked on macOS: VZNetworkSwitch (the
	// default) links every VM into nano-cloud's switch so guests reach each
	// other; VZNetworkNAT gives each VM Apple's NAT, which cannot reach other
	// VMs. Ignored on Linux.
	VZNetwork string `yaml:"vz_network"`
	// TLS configures HTTPS. With no block, the daemon serves HTTPS with a
	// self-signed certificate it generates.
	TLS TLSConfig `yaml:"tls"`
	// UpdateCheck lets the daemon ask GitHub, at most twice a day and only
	// while the UI is open, whether a newer release exists. Defaults to true.
	UpdateCheck *bool `yaml:"update_check"`
}

// TLSConfig is how the daemon serves HTTPS.
type TLSConfig struct {
	// Enabled defaults to true; false serves plain HTTP, for a reverse proxy
	// that terminates TLS itself, or a test.
	Enabled *bool `yaml:"enabled"`
	// Cert and Key are a PEM certificate chain and key to serve instead of
	// the self-signed pair. Both or neither.
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

// TLSEnabled reports whether the daemon serves HTTPS, defaulting to true.
func (c *Config) TLSEnabled() bool {
	return c.TLS.Enabled == nil || *c.TLS.Enabled
}

// UpdateCheckEnabled reports whether the daemon may look for new releases.
func (c *Config) UpdateCheckEnabled() bool {
	return c.UpdateCheck == nil || *c.UpdateCheck
}

// The values VZNetwork may take.
const (
	VZNetworkSwitch = "switch"
	VZNetworkNAT    = "nat"
)

// ReconcileConfig is the reconcile loop's only configurable setting.
type ReconcileConfig struct {
	// Enabled is a pointer so an absent block or key defaults to true. A plain
	// bool would disable self-healing on every config that predates the key.
	Enabled *bool `yaml:"enabled"`
}

// ReconcileEnabled reports whether the reconcile loop should run, defaulting
// to true when the reconcile block or its enabled key is absent.
func (c *Config) ReconcileEnabled() bool {
	return c.Reconcile.Enabled == nil || *c.Reconcile.Enabled
}

// StoragePool is a host path that VMs can use for disk storage.
type StoragePool struct {
	// Name is a human-readable label (e.g. "fast-ssd").
	Name string `yaml:"name"`
	// Path is the absolute directory on the host (e.g. "/var/lib/nano-cloud/pools/default").
	Path string `yaml:"path"`
	// Default marks the pool pre-selected in the create-VM wizard. Exactly one
	// pool must set it.
	Default bool `yaml:"default"`
}

// Load reads, parses and validates the YAML file at path. A pool path that does
// not exist yet only logs a warning, so pools can be configured before their
// directories are created.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: open %q: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only file; close error is not actionable

	var cfg Config
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true) // reject unknown keys — catches typos early
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: parse %q: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if len(c.StoragePools) == 0 {
		return fmt.Errorf("config: storage_pools must contain at least one entry")
	}

	defaults := 0
	for i, p := range c.StoragePools {
		if p.Name == "" {
			return fmt.Errorf("config: storage_pools[%d]: name is required", i)
		}
		if p.Path == "" {
			return fmt.Errorf("config: storage_pools[%d] (%s): path is required", i, p.Name)
		}
		if p.Default {
			defaults++
		}

		if _, err := os.Stat(p.Path); os.IsNotExist(err) {
			log.Printf("config: warning: storage pool %q path %q does not exist", p.Name, p.Path)
		}
	}

	if defaults == 0 {
		return fmt.Errorf("config: exactly one storage pool must have default: true (found none)")
	}
	if defaults > 1 {
		return fmt.Errorf("config: exactly one storage pool must have default: true (found %d)", defaults)
	}
	if (c.TLS.Cert == "") != (c.TLS.Key == "") {
		return fmt.Errorf("config: tls.cert and tls.key must be set together")
	}
	switch c.VZNetwork {
	case "", VZNetworkSwitch, VZNetworkNAT:
	default:
		return fmt.Errorf("config: vz_network must be %q or %q, not %q", VZNetworkSwitch, VZNetworkNAT, c.VZNetwork)
	}

	return nil
}
