package image

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CachedImage describes an image file in the local cache directory. ID, Name
// and OS are filled in by Manager.Cached for files that match the catalog.
type CachedImage struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	OS       string    `json:"os"`
	Filename string    `json:"filename"`
	Size     int64     `json:"size_bytes"`
	CachedAt time.Time `json:"cached_at"`
}

// Cache manages a directory of locally cached cloud images.
type Cache struct {
	dir string
}

// NewCache creates a Cache backed by dir. The directory is created on first write.
func NewCache(dir string) *Cache {
	return &Cache{dir: dir}
}

// List returns all files in the cache directory, or an empty slice if the
// directory does not exist yet.
func (c *Cache) List() ([]CachedImage, error) {
	entries, err := os.ReadDir(c.dir)
	if os.IsNotExist(err) {
		return []CachedImage{}, nil
	}
	if err != nil {
		return nil, err
	}

	images := make([]CachedImage, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue // skip partial in-progress downloads
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		images = append(images, CachedImage{
			Filename: e.Name(),
			Size:     info.Size(),
			CachedAt: info.ModTime(),
		})
	}
	return images, nil
}

// Path returns the full path for a filename within the cache directory.
func (c *Cache) Path(filename string) string {
	return filepath.Join(c.dir, filename)
}

// Has reports whether filename exists in the cache directory.
func (c *Cache) Has(filename string) bool {
	_, err := os.Stat(filepath.Join(c.dir, filename))
	return err == nil
}

// Remove deletes filename from the cache directory.
func (c *Cache) Remove(filename string) error {
	return os.Remove(filepath.Join(c.dir, filename))
}
