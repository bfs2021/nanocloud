package image_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/image"
)

// testEntry builds a CatalogEntry pointing at the given test server URLs.
func testEntry(imageURL, checksumURL, filename string) image.CatalogEntry {
	return image.CatalogEntry{
		ID:          "test-image",
		Name:        "Test Image",
		Distro:      "test",
		Filename:    filename,
		URL:         imageURL,
		ChecksumURL: checksumURL,
		ApproxBytes: 1024,
	}
}

func TestPull_Success(t *testing.T) {
	const filename = "test.img"
	content := []byte("hello nano cloud image")
	sum := sha256.Sum256(content)
	checksum := fmt.Sprintf("%x", sum)

	mux := http.NewServeMux()
	mux.HandleFunc("/"+filename, func(w http.ResponseWriter, _ *http.Request) {
		w.Write(content) //nolint:errcheck
	})
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  %s\n", checksum, filename)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	entry := testEntry(srv.URL+"/"+filename, srv.URL+"/SHA256SUMS", filename)

	progress := make(chan int, 128)
	err := image.Pull(context.Background(), entry, dir, progress)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}

	// Channel must be closed by Pull.
	for range progress {
	}

	got, err := os.ReadFile(filepath.Join(dir, filename))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("file content mismatch: got %q, want %q", got, content)
	}

	if _, err := os.Stat(filepath.Join(dir, filename+".tmp")); !os.IsNotExist(err) {
		t.Error("temp file still exists after successful pull")
	}
}

func TestPull_ChecksumMismatch(t *testing.T) {
	const filename = "bad.img"
	content := []byte("image data")

	mux := http.NewServeMux()
	mux.HandleFunc("/"+filename, func(w http.ResponseWriter, _ *http.Request) {
		w.Write(content) //nolint:errcheck
	})
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  %s\n", fmt.Sprintf("%064x", 0), filename)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	entry := testEntry(srv.URL+"/"+filename, srv.URL+"/SHA256SUMS", filename)

	progress := make(chan int, 128)
	err := image.Pull(context.Background(), entry, dir, progress)
	if err == nil {
		t.Fatal("Pull: expected checksum mismatch error, got nil")
	}

	// Channel must be closed even on error.
	for range progress {
	}

	if _, err := os.Stat(filepath.Join(dir, filename+".tmp")); !os.IsNotExist(err) {
		t.Error("temp file still exists after failed pull")
	}
	if _, err := os.Stat(filepath.Join(dir, filename)); !os.IsNotExist(err) {
		t.Error("final file should not exist after checksum failure")
	}
}

func TestPull_ContextCancellation(t *testing.T) {
	const filename = "cancel.img"

	mux := http.NewServeMux()
	mux.HandleFunc("/"+filename, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		// Write one byte, then block until the client disconnects.
		w.Write([]byte{0}) //nolint:errcheck
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	entry := testEntry(srv.URL+"/"+filename, srv.URL+"/SHA256SUMS", filename)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	progress := make(chan int, 128)
	err := image.Pull(ctx, entry, dir, progress)
	if err == nil {
		t.Fatal("Pull: expected context error, got nil")
	}
	for range progress {
	}
}
