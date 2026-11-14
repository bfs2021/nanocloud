//go:build netcheck

// Package image_test resolves every catalog entry against the live distro
// mirrors. These tests are excluded from the default suite; run them after
// changing the catalog with:
//
//	go test -tags=netcheck ./internal/image/...
package image_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/nanocloud-io/nanocloud/internal/image"
)

// TestNetcheck_CatalogResolvesAgainstLiveEndpoints checks that every static
// URL answers and every dynamic entry resolves to an image that answers.
func TestNetcheck_CatalogResolvesAgainstLiveEndpoints(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}
	resolver := image.NewDirIndexResolver(client)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	for _, entry := range image.Catalog {
		entry := entry // capture for t.Run parallel
		t.Run(entry.ID, func(t *testing.T) {
			if entry.IndexURL == "" {
				req, err := http.NewRequestWithContext(ctx, http.MethodHead, entry.URL, nil)
				if err != nil {
					t.Fatalf("build HEAD request: %v", err)
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatalf("HEAD %s: %v", entry.URL, err)
				}
				resp.Body.Close() //nolint:errcheck
				if resp.StatusCode == http.StatusNotFound {
					t.Errorf("HEAD %s returned 404 — static URL is stale", entry.URL)
				}
				return
			}

			resolved, err := resolver.Resolve(ctx, entry)
			if err != nil {
				t.Fatalf("Resolve %q: %v", entry.ID, err)
			}
			if resolved.Filename == "" {
				t.Errorf("%q: resolved Filename is empty", entry.ID)
			}
			if resolved.ChecksumURL == "" {
				t.Errorf("%q: resolved ChecksumURL is empty", entry.ID)
			}

			req, err := http.NewRequestWithContext(ctx, http.MethodHead, resolved.ImageURL, nil)
			if err != nil {
				t.Fatalf("build HEAD request for %s: %v", resolved.ImageURL, err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("HEAD %s: %v", resolved.ImageURL, err)
			}
			resp.Body.Close() //nolint:errcheck
			if resp.StatusCode == http.StatusNotFound {
				t.Errorf("HEAD %s returned 404 — resolved image URL does not exist", resolved.ImageURL)
			}

			t.Logf("%s -> %s (%s, %s)", entry.ID, resolved.Filename, resolved.ChecksumType, resolved.ChecksumURL)
		})
	}
}
