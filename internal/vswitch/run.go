//go:build darwin || linux

package vswitch

import (
	"context"
	"fmt"
)

// Run serves a switch whose uplink is up on the control socket at path,
// until ctx is done, a client asks it to quit, or the uplink fails.
func Run(ctx context.Context, path string, up Port, maxFrame int) error {
	sw := New(maxFrame)
	defer sw.Close()
	srv, err := Listen(path, sw)
	if err != nil {
		return err
	}
	defer srv.Close()
	go srv.Serve()

	upErr := make(chan error, 1)
	go func() { upErr <- sw.SetUplink(up) }()
	select {
	case <-ctx.Done():
	case <-srv.Quit():
	case err := <-upErr:
		return fmt.Errorf("uplink: %w", err)
	}
	return nil
}
