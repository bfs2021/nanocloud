package image

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/nanocloud-io/nanocloud/internal/storage"
)

// pullState tracks an in-flight or completed image pull. Its fields are
// guarded by Manager.mu.
type pullState struct {
	progress int
	done     bool
	err      error
	// cancel is non-nil only while the pull is active.
	cancel context.CancelFunc
	// wg lets CancelPull wait for the pull goroutine to finish.
	wg sync.WaitGroup
}

// Manager coordinates the image catalog, local cache, and in-flight pulls.
// mu guards pulls; pullFn is set at construction and never changes.
type Manager struct {
	cache   *Cache
	baseCtx context.Context // governs all background pulls; never request-scoped
	mu      sync.Mutex
	pulls   map[string]*pullState
	// pullFn is the download implementation; tests replace it.
	pullFn func(ctx context.Context, entry CatalogEntry, dir string, progress chan<- int) error
	// arch, when set, limits Catalog to entries for that architecture.
	arch string
}

// NewManager creates an image Manager backed by cacheDir.
func NewManager(cacheDir string) *Manager {
	return &Manager{
		cache:   NewCache(cacheDir),
		baseCtx: context.Background(),
		pulls:   make(map[string]*pullState),
		pullFn:  Pull,
	}
}

// CatalogEntryWithStatus extends CatalogEntry with runtime status.
type CatalogEntryWithStatus struct {
	CatalogEntry
	Status   string `json:"status"`   // "cached", "pulling", "available"
	Progress int    `json:"progress"` // 0–100; meaningful only when Status=="pulling"
}

// Catalog returns the full catalog enriched with per-entry cache status.
func (m *Manager) Catalog() []CatalogEntryWithStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]CatalogEntryWithStatus, 0, len(Catalog))
	for _, e := range Catalog {
		if m.arch != "" && e.Arch != m.arch {
			continue
		}
		ce := CatalogEntryWithStatus{CatalogEntry: e, Status: "available"}
		if m.cache.Has(e.Filename) {
			ce.Status = "cached"
		} else if ps, ok := m.pulls[e.ID]; ok && !ps.done {
			ce.Status = "pulling"
			ce.Progress = ps.progress
		}
		out = append(out, ce)
	}
	return out
}

// Cached returns the locally cached images, with ID, Name and OS filled in
// from the catalog where the file matches. The frontend deletes by ID.
func (m *Manager) Cached() ([]CachedImage, error) {
	images, err := m.cache.List()
	if err != nil {
		return nil, err
	}
	byFilename := make(map[string]CatalogEntry, len(Catalog))
	for _, e := range Catalog {
		byFilename[e.Filename] = e
	}
	for i := range images {
		if e, ok := byFilename[images[i].Filename]; ok {
			images[i].ID = e.ID
			images[i].Name = e.Name
			images[i].OS = e.Distro
		}
	}
	return images, nil
}

// Pull starts a background download for the catalog entry with the given ID.
// It runs under the manager's base context, not the request's, so the pull
// outlives the HTTP handler. It fails if the image is unknown, cached or
// already pulling.
func (m *Manager) Pull(id string) error {
	entry, ok := EntryByID(id)
	if !ok {
		return fmt.Errorf("image: unknown catalog ID %q", id)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cache.Has(entry.Filename) {
		return fmt.Errorf("image: %q is already cached", id)
	}
	if ps, ok := m.pulls[id]; ok && !ps.done {
		return fmt.Errorf("image: %q is already being pulled", id)
	}

	ctx, cancel := context.WithCancel(m.baseCtx)

	ps := &pullState{cancel: cancel}
	ps.wg.Add(1)
	m.pulls[id] = ps

	go func() {
		defer ps.wg.Done()
		ch := make(chan int, 16)

		doneCh := make(chan error, 1)
		go func() {
			doneCh <- m.pullFn(ctx, entry, m.cache.dir, ch)
		}()

		for pct := range ch {
			m.mu.Lock()
			ps.progress = pct
			m.mu.Unlock()
		}

		err := <-doneCh
		m.mu.Lock()
		ps.done = true
		ps.err = err
		ps.cancel = nil // release cancel func; context already terminal
		if err == nil {
			ps.progress = 100
		}
		m.mu.Unlock()
	}()

	return nil
}

// CancelPull stops an in-flight pull, waits for it to exit, removes the
// partial download and returns the entry to "available". Without an active
// pull it does nothing.
func (m *Manager) CancelPull(id string) error {
	entry, ok := EntryByID(id)
	if !ok {
		return fmt.Errorf("image: unknown catalog ID %q", id)
	}

	m.mu.Lock()
	ps, exists := m.pulls[id]
	if !exists || ps.done {
		m.mu.Unlock()
		return nil // no active pull; no-op
	}

	// Read under the lock: the goroutine clears ps.cancel when it finishes.
	cancel := ps.cancel
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	ps.wg.Wait()

	tmpPath := m.cache.Path(entry.Filename + ".tmp")
	os.Remove(tmpPath) //nolint:errcheck // best-effort; file may not exist

	m.mu.Lock()
	delete(m.pulls, id)
	m.mu.Unlock()

	return nil
}

// PullStatus returns the current progress state for a pull by catalog ID.
func (m *Manager) PullStatus(id string) (progress int, done bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ps, ok := m.pulls[id]
	if !ok {
		return 0, false, fmt.Errorf("image: no active or recent pull for %q", id)
	}
	return ps.progress, ps.done, ps.err
}

// CachedPath returns the path a cached image with this filename (not catalog
// ID) has or would have. It does not check that the file exists.
func (m *Manager) CachedPath(filename string) string {
	return m.cache.Path(filename)
}

// Remove deletes a cached image and any partial download by catalog ID. It
// refuses while a pull for that image is in flight.
func (m *Manager) Remove(id string) error {
	entry, ok := EntryByID(id)
	if !ok {
		return fmt.Errorf("image: unknown catalog ID %q", id)
	}

	m.mu.Lock()
	if ps, pulling := m.pulls[id]; pulling && !ps.done {
		m.mu.Unlock()
		return fmt.Errorf("image: cannot remove %q while pull is in progress", id)
	}
	m.mu.Unlock()

	os.Remove(m.cache.Path(entry.Filename + ".tmp")) //nolint:errcheck // best-effort; file may not exist
	// The raw conversion exists only on macOS, where VM disks are independent
	// clones of it.
	os.Remove(storage.RawBasePath(m.cache.Path(entry.Filename))) //nolint:errcheck // best-effort; file may not exist

	return m.cache.Remove(entry.Filename)
}
