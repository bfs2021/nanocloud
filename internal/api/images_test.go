package api_test

import (
	"encoding/binary"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/api"
	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/storage"
)

// A VM's boot disk is a qcow2 overlay on the cached image, so deleting the
// image strands every VM made from it. The delete must refuse while one
// exists, and must not count a clone, whose disk was flattened.
func TestHandleImagesDelete_RefusesImageAVMStillBacksOnto(t *testing.T) {
	imgs := cachedImageManager(t)
	base := imgs.CachedPath(testImageFilename)
	pool := t.TempDir()
	store := metadata.New(t.TempDir() + "/metadata.json")
	srv := api.New(api.Deps{WebFS: minimalWebFS(), MetaStore: store, ImgManager: imgs})

	writeQcow2Header(t, storage.DiskPathFor(pool, "web"), base)
	writeQcow2Header(t, storage.DiskPathFor(pool, "web-copy"), "")
	for _, name := range []string{"web", "web-copy"} {
		if err := store.Upsert(name, func(e *metadata.VMEntry) { e.PoolPath, e.ImageID = pool, testImageID }); err != nil {
			t.Fatal(err)
		}
	}

	rec := serve(srv, http.MethodDelete, "/images/"+testImageID, "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "web") || strings.Contains(rec.Body.String(), "web-copy") {
		t.Fatalf("delete with a VM on the image: got %d %q, want 409 naming web only", rec.Code, rec.Body)
	}
	if _, err := os.Stat(base); err != nil {
		t.Fatalf("refused delete removed the image anyway: %v", err)
	}

	if err := store.Delete("web"); err != nil {
		t.Fatal(err)
	}
	if rec := serve(srv, http.MethodDelete, "/images/"+testImageID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete once only the clone remains: got %d %q, want 204", rec.Code, rec.Body)
	}
}

// writeQcow2Header writes just enough of a qcow2 header for
// storage.BackingFile to read backing, or no backing file when it is empty.
func writeQcow2Header(t *testing.T, path, backing string) {
	t.Helper()
	hdr := make([]byte, 72, 72+len(backing))
	copy(hdr, "QFI\xfb")
	binary.BigEndian.PutUint32(hdr[4:], 3)
	if backing != "" {
		binary.BigEndian.PutUint64(hdr[8:], 72)
		binary.BigEndian.PutUint32(hdr[16:], uint32(len(backing)))
		hdr = append(hdr, backing...)
	}
	if err := os.WriteFile(path, hdr, 0o600); err != nil {
		t.Fatal(err)
	}
}
