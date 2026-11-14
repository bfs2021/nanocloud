package storage

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBackingFile(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "raw.img")
	if err := os.WriteFile(raw, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := BackingFile(raw); err != nil || got != "" {
		t.Errorf("raw disk: got %q, %v; want no backing file", got, err)
	}

	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not installed; the header layout is only checked against a real overlay")
	}
	base := filepath.Join(dir, "base.qcow2")
	run(t, "qemu-img", "create", "-f", "qcow2", base, "1M")
	if got, err := BackingFile(base); err != nil || got != "" {
		t.Errorf("standalone qcow2: got %q, %v; want no backing file", got, err)
	}

	abs := filepath.Join(dir, "abs.qcow2")
	run(t, "qemu-img", "create", "-f", "qcow2", "-b", base, "-F", "qcow2", abs)
	if got, err := BackingFile(abs); err != nil || got != base {
		t.Errorf("overlay: got %q, %v; want %q", got, err, base)
	}

	// A relative name resolves against the overlay's directory, not the cwd.
	rel := filepath.Join(dir, "rel.qcow2")
	run(t, "qemu-img", "create", "-f", "qcow2", "-b", "base.qcow2", "-F", "qcow2", rel)
	if got, err := BackingFile(rel); err != nil || got != base {
		t.Errorf("overlay with a relative name: got %q, %v; want %q", got, err, base)
	}
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, out)
	}
}
