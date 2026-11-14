## What and why

<!-- What changes for someone running Nano Cloud, and why, in plain words. Link the issue: "Fixes #123". -->

## How you tested it

<!-- Commands you ran. If this touches VM lifecycle, networking or cloud-init, also the real-host check: the distribution, the guest image, what you did and what you saw. No such host? Say so and I'll run it. -->

- [ ] `go test -p 1 ./...` with libvirtd running, and no new skips
- [ ] `golangci-lint run` and `gofmt -l .`
- [ ] `cd web && npm run typecheck && npm run lint && npm test`, if the UI changed
- [ ] A test covers this change, and it fails without the change

<!-- For a UI change, add a screenshot or two. -->
