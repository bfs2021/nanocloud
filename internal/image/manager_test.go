package image

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

// testCatalogID returns a real catalog ID, so lookups succeed without network.
func testCatalogID() string {
	return Catalog[0].ID
}

// TestManager_Pull_RequestContextDoesNotAbortDownload checks the download runs
// under a live context of its own, so it outlives the HTTP request.
func TestManager_Pull_RequestContextDoesNotAbortDownload(t *testing.T) {
	id := testCatalogID()

	fnCtxCh := make(chan context.Context, 1)

	var wg sync.WaitGroup
	wg.Add(1)

	m := NewManager(t.TempDir())
	m.pullFn = func(ctx context.Context, entry CatalogEntry, dir string, progress chan<- int) error {
		defer close(progress)
		defer wg.Done()
		fnCtxCh <- ctx
		return nil
	}

	err := m.Pull(id)
	if err != nil {
		t.Fatalf("Pull: unexpected error: %v", err)
	}

	wg.Wait()

	select {
	case fnCtx := <-fnCtxCh:
		if fnCtx.Err() != nil {
			t.Fatalf("pullFn received a canceled context: %v — bug is present", fnCtx.Err())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for pullFn to be called")
	}
}

// TestManager_Pull_HappyPath checks PullStatus goes from in flight to done.
func TestManager_Pull_HappyPath(t *testing.T) {
	id := testCatalogID()

	// proceed holds the fake download at 50% so the in-flight state is visible.
	proceed := make(chan struct{})

	m := NewManager(t.TempDir())
	m.pullFn = func(ctx context.Context, entry CatalogEntry, dir string, progress chan<- int) error {
		defer close(progress)
		progress <- 50
		<-proceed
		progress <- 100
		return nil
	}

	if err := m.Pull(id); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pct, done, err := m.PullStatus(id)
		if err != nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if !done && pct >= 50 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	pct, done, err := m.PullStatus(id)
	if err != nil {
		t.Fatalf("PullStatus during download: %v", err)
	}
	if done {
		t.Fatal("PullStatus: expected in-flight (done=false), got done=true")
	}
	if pct < 50 {
		t.Fatalf("PullStatus: expected progress >= 50, got %d", pct)
	}

	close(proceed)

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, done, _ = m.PullStatus(id)
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	pct, done, err = m.PullStatus(id)
	if err != nil {
		t.Fatalf("PullStatus after completion: %v", err)
	}
	if !done {
		t.Fatal("PullStatus: expected done=true after pull completed")
	}
	if pct != 100 {
		t.Fatalf("PullStatus: expected progress=100, got %d", pct)
	}
}

// TestManager_Pull_ErrorPropagated checks PullStatus reports pullFn's error.
func TestManager_Pull_ErrorPropagated(t *testing.T) {
	id := testCatalogID()
	sentinel := errors.New("injected pull failure")

	done := make(chan struct{})
	m := NewManager(t.TempDir())
	m.pullFn = func(ctx context.Context, entry CatalogEntry, dir string, progress chan<- int) error {
		defer close(progress)
		defer close(done)
		return sentinel
	}

	if err := m.Pull(id); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for pullFn to finish")
	}

	// Let the outer goroutine record the result.
	time.Sleep(10 * time.Millisecond)

	_, isDone, pullErr := m.PullStatus(id)
	if !isDone {
		t.Fatal("PullStatus: expected done=true after error")
	}
	if !errors.Is(pullErr, sentinel) {
		t.Fatalf("PullStatus: expected sentinel error, got %v", pullErr)
	}
}

func TestManager_Pull_UnknownID(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.Pull("no-such-image"); err == nil {
		t.Fatal("Pull: expected error for unknown ID, got nil")
	}
}

func TestManager_Pull_AlreadyPulling(t *testing.T) {
	id := testCatalogID()
	proceed := make(chan struct{})

	m := NewManager(t.TempDir())
	m.pullFn = func(ctx context.Context, entry CatalogEntry, dir string, progress chan<- int) error {
		defer close(progress)
		<-proceed // block until test releases it
		return nil
	}

	if err := m.Pull(id); err != nil {
		t.Fatalf("first Pull: %v", err)
	}
	if err := m.Pull(id); err == nil {
		t.Fatal("second Pull: expected conflict error, got nil")
	}

	close(proceed) // let the goroutine finish cleanly
}

// TestManager_CancelPull checks CancelPull cancels the context, removes the
// partial file and clears the pull state.
func TestManager_CancelPull(t *testing.T) {
	id := testCatalogID()
	entry, _ := EntryByID(id)

	dir := t.TempDir()
	m := NewManager(dir)

	ctxCh := make(chan context.Context, 1)

	m.pullFn = func(ctx context.Context, e CatalogEntry, d string, progress chan<- int) error {
		defer close(progress)
		ctxCh <- ctx
		<-ctx.Done() // block until canceled
		return ctx.Err()
	}

	if err := m.Pull(id); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	fnCtx := <-ctxCh

	tmpPath := m.cache.Path(entry.Filename + ".tmp")
	if err := os.WriteFile(tmpPath, []byte("partial"), 0o644); err != nil {
		t.Fatalf("create fake .tmp: %v", err)
	}

	if err := m.CancelPull(id); err != nil {
		t.Fatalf("CancelPull: %v", err)
	}

	if fnCtx.Err() == nil {
		t.Error("CancelPull did not cancel the pull context")
	}

	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("CancelPull did not remove the .tmp file")
	}

	_, _, pullErr := m.PullStatus(id)
	if pullErr == nil {
		t.Error("PullStatus: expected 'no active pull' error after CancelPull, got nil")
	}
}

func TestManager_CancelPull_NoOp(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.CancelPull(testCatalogID()); err != nil {
		t.Fatalf("CancelPull with no active pull: %v", err)
	}
}

func TestManager_CancelPull_UnknownID(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.CancelPull("no-such-image"); err == nil {
		t.Fatal("CancelPull unknown ID: expected error, got nil")
	}
}

// TestManager_Remove_CleansUpTmp checks Remove also deletes a partial download.
func TestManager_Remove_CleansUpTmp(t *testing.T) {
	id := testCatalogID()
	entry, _ := EntryByID(id)

	m := NewManager(t.TempDir())

	if err := os.WriteFile(m.cache.Path(entry.Filename), []byte("image"), 0o644); err != nil {
		t.Fatalf("create fake cache file: %v", err)
	}
	tmpPath := m.cache.Path(entry.Filename + ".tmp")
	if err := os.WriteFile(tmpPath, []byte("partial"), 0o644); err != nil {
		t.Fatalf("create fake .tmp: %v", err)
	}

	if err := m.Remove(id); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("Remove did not clean up the stray .tmp file")
	}
}
