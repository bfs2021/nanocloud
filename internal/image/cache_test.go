package image_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/image"
)

func TestCache_ListEmpty_NonExistentDir(t *testing.T) {
	c := image.NewCache(filepath.Join(t.TempDir(), "images"))
	imgs, err := c.List()
	if err != nil {
		t.Fatalf("List on non-existent dir: %v", err)
	}
	if len(imgs) != 0 {
		t.Errorf("expected 0 images, got %d", len(imgs))
	}
}

func TestCache_ListEmpty_ExistingDir(t *testing.T) {
	c := image.NewCache(t.TempDir())
	imgs, err := c.List()
	if err != nil {
		t.Fatalf("List on empty dir: %v", err)
	}
	if len(imgs) != 0 {
		t.Errorf("expected 0 images, got %d", len(imgs))
	}
}

func TestCache_Has(t *testing.T) {
	dir := t.TempDir()
	c := image.NewCache(dir)

	if c.Has("test.img") {
		t.Error("Has: expected false for missing file")
	}

	if err := os.WriteFile(filepath.Join(dir, "test.img"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !c.Has("test.img") {
		t.Error("Has: expected true for existing file")
	}
}

func TestCache_Remove(t *testing.T) {
	dir := t.TempDir()
	c := image.NewCache(dir)

	fname := "test.img"
	if err := os.WriteFile(filepath.Join(dir, fname), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := c.Remove(fname); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if c.Has(fname) {
		t.Error("file still exists after Remove")
	}
}

func TestCache_List_ReturnsFiles(t *testing.T) {
	dir := t.TempDir()
	c := image.NewCache(dir)

	for _, name := range []string{"a.img", "b.img"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	imgs, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(imgs) != 2 {
		t.Errorf("expected 2 images, got %d", len(imgs))
	}
}
