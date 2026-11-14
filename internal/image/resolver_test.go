package image_test

import (
	"context"
	"crypto/sha512"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/image"
)

// fixtureServer serves testdata files by URL path, and 404 for unmapped paths.
func fixtureServer(t *testing.T, urlToFile map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, ok := urlToFile[r.URL.Path]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		data, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data) //nolint:errcheck
	}))
}

func TestParseIndexLinks_Alpine(t *testing.T) {
	body, err := os.ReadFile("testdata/alpine-320-cloud.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	links := image.ParseIndexLinks(body)

	wantPresent := []string{
		"nocloud_alpine-3.20.0-x86_64-bios-cloudinit-r0.qcow2",
		"nocloud_alpine-3.20.0-x86_64-bios-cloudinit-r0.qcow2.sha512",
		"nocloud_alpine-3.20.0-aarch64-uefi-cloudinit-r0.qcow2",
	}
	linkSet := make(map[string]bool, len(links))
	for _, l := range links {
		linkSet[l] = true
	}
	for _, want := range wantPresent {
		if !linkSet[want] {
			t.Errorf("parseIndexLinks: expected %q in links, got %v", want, links)
		}
	}
	// Parent-directory and query links must be excluded.
	for _, l := range links {
		if len(l) > 0 && (l[0] == '/' || l[0] == '?') {
			t.Errorf("parseIndexLinks: unexpected link %q", l)
		}
	}
}

func TestParseIndexLinks_Debian(t *testing.T) {
	body, err := os.ReadFile("testdata/debian-bookworm-latest.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	links := image.ParseIndexLinks(body)

	wantPresent := []string{
		"SHA512SUMS",
		"debian-12-genericcloud-amd64.qcow2",
	}
	linkSet := make(map[string]bool, len(links))
	for _, l := range links {
		linkSet[l] = true
	}
	for _, want := range wantPresent {
		if !linkSet[want] {
			t.Errorf("parseIndexLinks: expected %q in links, got %v", want, links)
		}
	}
}

func TestParseIndexLinks_Ubuntu(t *testing.T) {
	body, err := os.ReadFile("testdata/ubuntu-2404-release.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	links := image.ParseIndexLinks(body)

	wantPresent := []string{
		"SHA256SUMS",
		"ubuntu-24.04-server-cloudimg-amd64.img",
	}
	linkSet := make(map[string]bool, len(links))
	for _, l := range links {
		linkSet[l] = true
	}
	for _, want := range wantPresent {
		if !linkSet[want] {
			t.Errorf("parseIndexLinks: expected %q in links, got %v", want, links)
		}
	}
}

func TestResolver_Alpine(t *testing.T) {
	srv := fixtureServer(t, map[string]string{
		"/cloud/": "alpine-320-cloud.html",
	})
	defer srv.Close()

	entry := image.CatalogEntry{
		ID:          "alpine-3.20",
		Name:        "Alpine Linux 3.20",
		Distro:      "alpine",
		Arch:        "amd64",
		Version:     "3.20",
		ApproxBytes: 180 * 1024 * 1024,
		IndexURL:    srv.URL + "/cloud/",
		FilePattern: `^nocloud_alpine-3\.\d+\.\d+-x86_64-bios-cloudinit-r\d+\.qcow2$`,
	}

	r := image.NewDirIndexResolver(srv.Client())
	resolved, err := r.Resolve(context.Background(), entry)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	wantFilename := "nocloud_alpine-3.20.0-x86_64-bios-cloudinit-r0.qcow2"
	if resolved.Filename != wantFilename {
		t.Errorf("Filename = %q, want %q", resolved.Filename, wantFilename)
	}
	wantChecksumURL := srv.URL + "/cloud/" + wantFilename + ".sha512"
	if resolved.ChecksumURL != wantChecksumURL {
		t.Errorf("ChecksumURL = %q, want %q", resolved.ChecksumURL, wantChecksumURL)
	}
	if resolved.ChecksumType != image.ChecksumSHA512 {
		t.Errorf("ChecksumType = %q, want %q", resolved.ChecksumType, image.ChecksumSHA512)
	}
}

func TestResolver_Debian(t *testing.T) {
	srv := fixtureServer(t, map[string]string{
		"/bookworm/latest/": "debian-bookworm-latest.html",
	})
	defer srv.Close()

	entry := image.CatalogEntry{
		ID:          "debian-12",
		Name:        "Debian 12 (Bookworm)",
		Distro:      "debian",
		Arch:        "amd64",
		Version:     "12",
		ApproxBytes: 550 * 1024 * 1024,
		IndexURL:    srv.URL + "/bookworm/latest/",
		FilePattern: `^debian-12-genericcloud-amd64\.qcow2$`,
	}

	r := image.NewDirIndexResolver(srv.Client())
	resolved, err := r.Resolve(context.Background(), entry)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if resolved.Filename != "debian-12-genericcloud-amd64.qcow2" {
		t.Errorf("Filename = %q, want debian-12-genericcloud-amd64.qcow2", resolved.Filename)
	}
	wantChecksumURL := srv.URL + "/bookworm/latest/SHA512SUMS"
	if resolved.ChecksumURL != wantChecksumURL {
		t.Errorf("ChecksumURL = %q, want %q", resolved.ChecksumURL, wantChecksumURL)
	}
	if resolved.ChecksumType != image.ChecksumSHA512 {
		t.Errorf("ChecksumType = %q, want sha512", resolved.ChecksumType)
	}
}

func TestResolver_Ubuntu(t *testing.T) {
	srv := fixtureServer(t, map[string]string{
		"/releases/24.04/release/": "ubuntu-2404-release.html",
	})
	defer srv.Close()

	entry := image.CatalogEntry{
		ID:          "ubuntu-2404-standard",
		Name:        "Ubuntu 24.04 LTS",
		Distro:      "ubuntu",
		Arch:        "amd64",
		Version:     "24.04 LTS",
		ApproxBytes: 650 * 1024 * 1024,
		IndexURL:    srv.URL + "/releases/24.04/release/",
		FilePattern: `^ubuntu-24\.04-server-cloudimg-amd64\.img$`,
	}

	r := image.NewDirIndexResolver(srv.Client())
	resolved, err := r.Resolve(context.Background(), entry)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if resolved.Filename != "ubuntu-24.04-server-cloudimg-amd64.img" {
		t.Errorf("Filename = %q, want ubuntu-24.04-server-cloudimg-amd64.img", resolved.Filename)
	}
	wantChecksumURL := srv.URL + "/releases/24.04/release/SHA256SUMS"
	if resolved.ChecksumURL != wantChecksumURL {
		t.Errorf("ChecksumURL = %q, want %q", resolved.ChecksumURL, wantChecksumURL)
	}
	if resolved.ChecksumType != image.ChecksumSHA256 {
		t.Errorf("ChecksumType = %q, want sha256", resolved.ChecksumType)
	}
}

func TestResolver_StaticEntry(t *testing.T) {
	// A static entry must resolve without any HTTP call.
	entry := image.CatalogEntry{
		ID:          "fedora-41",
		Filename:    "Fedora-Cloud-Base.qcow2",
		URL:         "https://example.com/Fedora-Cloud-Base.qcow2",
		ChecksumURL: "https://example.com/CHECKSUM",
		ApproxBytes: 600 * 1024 * 1024,
	}

	r := image.NewDirIndexResolver(nil) // nil client — panics if used
	resolved, err := r.Resolve(context.Background(), entry)
	if err != nil {
		t.Fatalf("Resolve static entry: %v", err)
	}
	if resolved.ImageURL != entry.URL {
		t.Errorf("ImageURL = %q, want %q", resolved.ImageURL, entry.URL)
	}
	if resolved.Filename != entry.Filename {
		t.Errorf("Filename = %q, want %q", resolved.Filename, entry.Filename)
	}
}

func TestResolver_NoPatternMatch(t *testing.T) {
	srv := fixtureServer(t, map[string]string{
		"/cloud/": "alpine-320-cloud.html",
	})
	defer srv.Close()

	entry := image.CatalogEntry{
		ID:          "test",
		IndexURL:    srv.URL + "/cloud/",
		FilePattern: `^does-not-match-anything\.img$`,
		ApproxBytes: 1,
	}

	r := image.NewDirIndexResolver(srv.Client())
	_, err := r.Resolve(context.Background(), entry)
	if err == nil {
		t.Fatal("expected error for no pattern match, got nil")
	}
}

func TestResolver_IndexFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	entry := image.CatalogEntry{
		ID:          "test",
		IndexURL:    srv.URL + "/missing/",
		FilePattern: `.*`,
		ApproxBytes: 1,
	}

	r := image.NewDirIndexResolver(srv.Client())
	_, err := r.Resolve(context.Background(), entry)
	if err == nil {
		t.Fatal("expected error for 404 index, got nil")
	}
}

func TestPull_SHA512_Success(t *testing.T) {
	const filename = "test-sha512.img"
	content := []byte("hello nano cloud sha512 image")

	sum := sha512.Sum512(content)
	checksum := fmt.Sprintf("%x", sum)

	mux := http.NewServeMux()
	mux.HandleFunc("/"+filename, func(w http.ResponseWriter, _ *http.Request) {
		w.Write(content) //nolint:errcheck
	})
	mux.HandleFunc("/SHA512SUMS", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", checksum, filename) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	entry := image.CatalogEntry{
		ID:           "test-sha512",
		Name:         "Test SHA-512",
		Distro:       "test",
		Filename:     filename,
		URL:          srv.URL + "/" + filename,
		ChecksumURL:  srv.URL + "/SHA512SUMS",
		ChecksumType: string(image.ChecksumSHA512),
		ApproxBytes:  1024,
	}

	dir := t.TempDir()
	progress := make(chan int, 128)
	err := image.Pull(context.Background(), entry, dir, progress)
	if err != nil {
		t.Fatalf("Pull SHA-512: %v", err)
	}
	for range progress {
	}

	got, err := os.ReadFile(filepath.Join(dir, filename))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}
}

// TestPull_SHA512_Mismatch checks a bad checksum leaves no file behind.
func TestPull_SHA512_Mismatch(t *testing.T) {
	const filename = "bad-sha512.img"
	content := []byte("image data for sha512 mismatch test")

	mux := http.NewServeMux()
	mux.HandleFunc("/"+filename, func(w http.ResponseWriter, _ *http.Request) {
		w.Write(content) //nolint:errcheck
	})
	mux.HandleFunc("/SHA512SUMS", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", fmt.Sprintf("%0128x", 0), filename) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	entry := image.CatalogEntry{
		ID:           "test-sha512-mismatch",
		Filename:     filename,
		URL:          srv.URL + "/" + filename,
		ChecksumURL:  srv.URL + "/SHA512SUMS",
		ChecksumType: string(image.ChecksumSHA512),
		ApproxBytes:  1024,
	}

	dir := t.TempDir()
	progress := make(chan int, 128)
	err := image.Pull(context.Background(), entry, dir, progress)
	if err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
	for range progress {
	}

	if _, err := os.Stat(filepath.Join(dir, filename+".tmp")); !os.IsNotExist(err) {
		t.Error("temp file still exists after SHA-512 mismatch")
	}
	if _, err := os.Stat(filepath.Join(dir, filename)); !os.IsNotExist(err) {
		t.Error("final file should not exist after SHA-512 mismatch")
	}
}

// TestPull_CanonicalFilenameAndVersionPick checks the image is cached under
// entry.Filename rather than the remote name, and that 3.20.10 is picked over
// 3.20.9.
func TestPull_CanonicalFilenameAndVersionPick(t *testing.T) {
	const localName = "myapp-stable.img" // catalog's stable filename
	newContent := []byte("this is the NEWER 3.20.10 image")
	oldContent := []byte("this is the older 3.20.9 image")
	newSum := fmt.Sprintf("%x", sha512.Sum512(newContent))
	oldSum := fmt.Sprintf("%x", sha512.Sum512(oldContent))

	mux := http.NewServeMux()
	mux.HandleFunc("/d/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body><pre>`+
			`<a href="app-3.20.9.img">app-3.20.9.img</a>`+
			`<a href="app-3.20.10.img">app-3.20.10.img</a>`+
			`<a href="SHA512SUMS">SHA512SUMS</a>`+
			`</pre></body></html>`)
	})
	mux.HandleFunc("/d/app-3.20.10.img", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(newContent) //nolint:errcheck
	})
	mux.HandleFunc("/d/app-3.20.9.img", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(oldContent) //nolint:errcheck
	})
	mux.HandleFunc("/d/SHA512SUMS", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  app-3.20.10.img\n%s  app-3.20.9.img\n", newSum, oldSum) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	entry := image.CatalogEntry{
		ID:          "test-canonical",
		Filename:    localName, // stable local name, differs from remote filename
		IndexURL:    srv.URL + "/d/",
		FilePattern: `^app-\d+\.\d+\.\d+\.img$`,
		ApproxBytes: 1024,
	}

	dir := t.TempDir()
	progress := make(chan int, 128)
	if err := image.Pull(context.Background(), entry, dir, progress); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	for range progress {
	}

	got, err := os.ReadFile(filepath.Join(dir, localName))
	if err != nil {
		t.Fatalf("expected cached file at stable name %q: %v", localName, err)
	}
	if string(got) != string(newContent) {
		t.Errorf("resolver picked the wrong version: got %q, want newer %q", got, newContent)
	}
	if _, err := os.Stat(filepath.Join(dir, "app-3.20.10.img")); !os.IsNotExist(err) {
		t.Error("file was cached under the remote filename; should use entry.Filename")
	}
}
