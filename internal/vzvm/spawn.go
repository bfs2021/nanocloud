package vzvm

import (
	"context"
	"errors"
	"sync"
)

// Spawner starts a helper process for spec and returns once the helper has
// booted the VM, or with the reason it could not.
type Spawner func(spec HelperSpec) error

// InProcess runs helpers as goroutines of the calling process, so tests can
// drive the real helper code against a fake Machine.
type InProcess struct {
	Launch Launcher

	mu      sync.Mutex
	cancels []context.CancelFunc
	wg      sync.WaitGroup
}

// Spawn implements Spawner.
func (p *InProcess) Spawn(spec HelperSpec) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	p.cancels = append(p.cancels, cancel)
	p.mu.Unlock()

	ready := make(chan error, 1)
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		_ = RunHelper(ctx, spec, p.Launch, func(err error) { ready <- err })
	}()
	if err := <-ready; err != nil {
		return err
	}
	return nil
}

// Close stops every helper (gracefully, as at host shutdown) and waits for them.
func (p *InProcess) Close() {
	p.mu.Lock()
	cancels := p.cancels
	p.cancels = nil
	p.mu.Unlock()
	for _, c := range cancels {
		c()
	}
	p.wg.Wait()
}

var errNotStarted = errors.New("helper exited before reporting whether the VM booted")
