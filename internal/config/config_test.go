package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/config"
)

// writeConfig creates a temp config.yaml with the given content and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
	return path
}

func TestLoad_ValidConfig(t *testing.T) {
	poolDir := t.TempDir()

	content := "libvirt_uri: test:///default\nstorage_pools:\n  - name: default\n    path: " + poolDir + "\n    default: true\n"
	path := writeConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if cfg.LibvirtURI != "test:///default" {
		t.Errorf("LibvirtURI = %q, want %q", cfg.LibvirtURI, "test:///default")
	}
	if len(cfg.StoragePools) != 1 {
		t.Fatalf("len(StoragePools) = %d, want 1", len(cfg.StoragePools))
	}
	p := cfg.StoragePools[0]
	if p.Name != "default" {
		t.Errorf("pool Name = %q, want %q", p.Name, "default")
	}
	if p.Path != poolDir {
		t.Errorf("pool Path = %q, want %q", p.Path, poolDir)
	}
	if !p.Default {
		t.Error("pool Default = false, want true")
	}
}

func TestLoad_MultipleDefaults(t *testing.T) {
	d1, d2 := t.TempDir(), t.TempDir()
	content := "libvirt_uri: test:///default\nstorage_pools:\n  - name: a\n    path: " + d1 + "\n    default: true\n  - name: b\n    path: " + d2 + "\n    default: true\n"
	path := writeConfig(t, content)

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load: expected error for two defaults, got nil")
	}
}

func TestLoad_NoDefault(t *testing.T) {
	d := t.TempDir()
	content := "libvirt_uri: test:///default\nstorage_pools:\n  - name: a\n    path: " + d + "\n    default: false\n"
	path := writeConfig(t, content)

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load: expected error for no default, got nil")
	}
}

func TestLoad_EmptyPools(t *testing.T) {
	content := "libvirt_uri: test:///default\nstorage_pools: []\n"
	path := writeConfig(t, content)

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load: expected error for empty pools, got nil")
	}
}

func TestLoad_MissingPoolName(t *testing.T) {
	d := t.TempDir()
	content := "libvirt_uri: test:///default\nstorage_pools:\n  - path: " + d + "\n    default: true\n"
	path := writeConfig(t, content)

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load: expected error for missing pool name, got nil")
	}
}

func TestLoad_MissingPoolPath(t *testing.T) {
	content := "libvirt_uri: test:///default\nstorage_pools:\n  - name: nopath\n    default: true\n"
	path := writeConfig(t, content)

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load: expected error for missing pool path, got nil")
	}
}

func TestLoad_NonExistentConfigFile(t *testing.T) {
	_, err := config.Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("Load: expected error for missing config file, got nil")
	}
}

func TestLoad_NonExistentPoolPath_IsWarningNotError(t *testing.T) {
	content := "libvirt_uri: test:///default\nstorage_pools:\n  - name: ghost\n    path: /nonexistent/pool/path\n    default: true\n"
	path := writeConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: expected no error for non-existent pool path, got: %v", err)
	}
	if len(cfg.StoragePools) != 1 || cfg.StoragePools[0].Name != "ghost" {
		t.Errorf("unexpected pool config: %+v", cfg.StoragePools)
	}
}

func TestLoad_UnknownField(t *testing.T) {
	d := t.TempDir()
	content := "libvirt_uri: test:///default\nstorage_pools:\n  - name: a\n    path: " + d + "\n    default: true\nunknown_field: bad\n"
	path := writeConfig(t, content)

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load: expected error for unknown config field, got nil")
	}
}

func TestLoad_MultiplePools(t *testing.T) {
	d1, d2 := t.TempDir(), t.TempDir()
	content := "libvirt_uri: test:///default\nstorage_pools:\n  - name: primary\n    path: " + d1 + "\n    default: true\n  - name: secondary\n    path: " + d2 + "\n    default: false\n"
	path := writeConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if len(cfg.StoragePools) != 2 {
		t.Fatalf("len(StoragePools) = %d, want 2", len(cfg.StoragePools))
	}
}

func TestLoad_VZNetwork(t *testing.T) {
	pools := "storage_pools:\n  - name: default\n    path: " + t.TempDir() + "\n    default: true\n"
	for _, v := range []string{"", "vz_network: switch\n", "vz_network: nat\n"} {
		if _, err := config.Load(writeConfig(t, pools+v)); err != nil {
			t.Errorf("%q: %v", v, err)
		}
	}
	if _, err := config.Load(writeConfig(t, pools+"vz_network: bridged\n")); err == nil {
		t.Error("accepted vz_network: bridged")
	}
}

func TestLoad_TLS(t *testing.T) {
	pool := "storage_pools:\n  - name: default\n    path: " + t.TempDir() + "\n    default: true\n"
	for name, tc := range map[string]struct {
		yaml    string
		enabled bool
		wantErr bool
	}{
		"absent defaults on": {"", true, false},
		"explicitly off":     {"tls:\n  enabled: false\n", false, false},
		"own certificate":    {"tls:\n  cert: /c.pem\n  key: /k.pem\n", true, false},
		"cert without key":   {"tls:\n  cert: /c.pem\n", false, true},
	} {
		cfg, err := config.Load(writeConfig(t, pool+tc.yaml))
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: loaded, want an error", name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if cfg.TLSEnabled() != tc.enabled {
			t.Errorf("%s: TLSEnabled = %v, want %v", name, cfg.TLSEnabled(), tc.enabled)
		}
	}
}
