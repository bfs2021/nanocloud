//go:build darwin || linux

package vswitch

import (
	"context"
	"io"
	"log"
	"time"
)

// Retry delays for a helper whose switch is not answering.
var (
	retryMin = 250 * time.Millisecond
	retryMax = 2 * time.Second
)

// KeepAttached keeps link attached to the switch at path as name's port until
// ctx is done, handing it to the next switch whenever the old one goes away.
// It logs each change, not each retry.
func KeepAttached(ctx context.Context, path, name, mac string, link *Link) {
	delay := retryMin
	lastErr := ""
	for {
		c, err := attachOnce(path, name, mac, link)
		if err == nil {
			log.Printf("vswitch: %s attached to the switch", name)
			lastErr, delay = "", retryMin
			done := make(chan struct{})
			go func() {
				_, _ = io.Copy(io.Discard, c) // returns when the switch goes
				close(done)
			}()
			select {
			case <-ctx.Done():
				c.Close() //nolint:errcheck // detaching
				return
			case <-done:
				c.Close() //nolint:errcheck // already gone
				log.Printf("vswitch: %s lost the switch; attaching again", name)
			}
			continue
		}
		if err.Error() != lastErr {
			log.Printf("vswitch: %s cannot attach to the switch at %s: %v", name, path, err)
			lastErr = err.Error()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, retryMax)
	}
}
