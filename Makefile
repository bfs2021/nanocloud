BINARY := nanocloud

## Version stamped into the binary: the nearest git tag locally, the computed
## tag in release.yml. `nanocloud version` and GET /system report it.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/nanocloud-io/nanocloud/internal/version.Version=$(VERSION)

.PHONY: all ui build test test-ui test-e2e test-all lint cross cross-linux-amd64 cross-linux-arm64 cross-darwin-arm64 clean

## macOS binaries need this entitlement or Virtualization.framework refuses to
## run VMs. An ad-hoc signature (-s -) is enough.
ENTITLEMENTS := darwin/entitlements.plist

## Full pipeline: build frontend then binary
all: ui build

## Build the React frontend (output → web/dist)
ui:
	cd web && npm ci && npm run build

## Compile the Go binary (requires web/dist — run 'make ui' first)
build:
	@test -f web/dist/index.html || { echo "error: run 'make ui' first"; exit 1; }
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/nanocloud
	@if [ "$$(uname -s)" = Darwin ]; then codesign --force --sign - --entitlements $(ENTITLEMENTS) $(BINARY); fi

## Run all Go tests
test:
	go test -p 1 ./...

## Run frontend typecheck + unit/component tests (Vitest)
test-ui:
	cd web && npm ci && npm run typecheck && npm test

## Run the Playwright E2E smoke suite against the freshly built binary.
## Requires a running libvirtd (the suite uses the test:///default driver).
test-e2e: all
	cd e2e && npm ci && npx playwright test

## Run every test suite gate used for releases
test-all: test test-ui test-e2e

## Run golangci-lint
lint:
	golangci-lint run

## Build every release binary; `make -j3 cross` builds them in parallel. The
## macOS binary needs cgo and builds only on a Mac; elsewhere it is skipped.
cross: cross-linux-amd64 cross-linux-arm64 cross-darwin-arm64

cross-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY)-linux-amd64 ./cmd/nanocloud

cross-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BINARY)-linux-arm64 ./cmd/nanocloud

cross-darwin-arm64:
	@if [ "$$(uname -s)" != Darwin ]; then \
		echo "skipping $(BINARY)-darwin-arm64: it needs cgo and the macOS SDK, so it builds on a Mac (CI: release.yml's build-darwin job)"; \
	else \
		CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BINARY)-darwin-arm64 ./cmd/nanocloud && \
		codesign --force --sign - --entitlements $(ENTITLEMENTS) $(BINARY)-darwin-arm64; \
	fi

## Remove build artifacts (does not delete web/dist/index.html placeholder)
clean:
	rm -f $(BINARY) $(BINARY)-linux-amd64 $(BINARY)-linux-arm64 $(BINARY)-darwin-arm64
