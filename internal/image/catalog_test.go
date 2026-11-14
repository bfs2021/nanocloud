package image_test

import (
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/image"
)

func TestCatalog_AllEntriesHaveRequiredFields(t *testing.T) {
	seen := make(map[string]bool)
	for _, e := range image.Catalog {
		if e.ID == "" {
			t.Errorf("entry has empty ID: %+v", e)
		}
		if seen[e.ID] {
			t.Errorf("duplicate catalog ID: %q", e.ID)
		}
		seen[e.ID] = true

		if e.Name == "" {
			t.Errorf("entry %q has empty Name", e.ID)
		}
		if e.Distro == "" {
			t.Errorf("entry %q has empty Distro", e.ID)
		}
		if e.URL == "" {
			t.Errorf("entry %q has empty URL", e.ID)
		}
		if !strings.HasPrefix(e.URL, "https://") {
			t.Errorf("entry %q URL does not start with https://: %s", e.ID, e.URL)
		}
		if e.ChecksumURL == "" {
			t.Errorf("entry %q has empty ChecksumURL", e.ID)
		}
		if e.Filename == "" {
			t.Errorf("entry %q has empty Filename", e.ID)
		}
		if e.ApproxBytes <= 0 {
			t.Errorf("entry %q has non-positive ApproxBytes: %d", e.ID, e.ApproxBytes)
		}
	}
	if len(image.Catalog) == 0 {
		t.Fatal("catalog is empty")
	}
	t.Logf("catalog has %d entries", len(image.Catalog))
}

// TestCatalog_DynamicResolutionCoverage pins which entries resolve
// dynamically. Fedora's filenames carry a build number, so it must; Rocky,
// AlmaLinux and openSUSE use aliases the distro keeps current.
func TestCatalog_DynamicResolutionCoverage(t *testing.T) {
	wantDynamic := map[string]bool{
		"ubuntu-2404-standard": true,
		"ubuntu-2204-standard": true,
		"debian-12":            true,
		"alpine-3.20":          true,
		"fedora-41":            true,
		"rocky-9":              false,
		"almalinux-9":          false,
		"opensuse-leap-156":    false,
		// arm64, for macOS: the same choice as each amd64 counterpart.
		"ubuntu-2404-standard-arm64": true,
		"ubuntu-2204-standard-arm64": true,
		"debian-12-arm64":            true,
		"fedora-41-arm64":            true,
		"rocky-9-arm64":              false,
		"almalinux-9-arm64":          false,
		"opensuse-leap-156-arm64":    false,
	}
	for _, e := range image.Catalog {
		want, ok := wantDynamic[e.ID]
		if !ok {
			t.Errorf("entry %q not covered by this test's expectations — add it", e.ID)
			continue
		}
		got := e.IndexURL != ""
		if got != want {
			t.Errorf("entry %q: IndexURL set = %v, want %v", e.ID, got, want)
		}
	}
}

func TestEntryByID_Found(t *testing.T) {
	e, ok := image.EntryByID("ubuntu-2404-standard")
	if !ok {
		t.Fatal("EntryByID: ubuntu-2404-standard not found")
	}
	if e.Distro != "ubuntu" {
		t.Errorf("Distro = %q, want ubuntu", e.Distro)
	}
}

func TestEntryByID_NotFound(t *testing.T) {
	_, ok := image.EntryByID("nonexistent-image")
	if ok {
		t.Error("EntryByID: expected not found for unknown ID")
	}
}

// TestCatalog_LinuxListIsUnchanged pins the amd64 list a Linux daemon shows.
func TestCatalog_LinuxListIsUnchanged(t *testing.T) {
	m := image.NewManagerForArch(t.TempDir(), "amd64")
	var got []string
	for _, e := range m.Catalog() {
		got = append(got, e.ID)
	}
	want := []string{"ubuntu-2404-standard", "ubuntu-2204-standard", "debian-12", "alpine-3.20",
		"fedora-41", "rocky-9", "almalinux-9", "opensuse-leap-156"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("amd64 catalog = %v, want %v", got, want)
	}
}

func TestCatalog_Arm64ListsOnlyArm64(t *testing.T) {
	m := image.NewManagerForArch(t.TempDir(), "arm64")
	entries := m.Catalog()
	if len(entries) == 0 {
		t.Fatal("no arm64 images")
	}
	for _, e := range entries {
		if e.Arch != "arm64" || !strings.HasSuffix(e.ID, "-arm64") {
			t.Errorf("arm64 catalog lists %q (%s)", e.ID, e.Arch)
		}
		if strings.Contains(e.Filename, "amd64") || strings.Contains(e.Filename, "x86_64") {
			t.Errorf("%q downloads an x86 file: %s", e.ID, e.Filename)
		}
	}
}
