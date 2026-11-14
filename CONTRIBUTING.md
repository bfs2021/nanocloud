# Contributing

Thanks for helping. This file takes you from a fresh clone to a running UI
and passing tests, then through sending a change.
[ARCHITECTURE.md](ARCHITECTURE.md) explains how the code fits together.

## Before you start

Questions, and ideas that aren't a concrete request yet, go in
[Discussions](https://github.com/nanocloud-io/nanocloud/discussions), not
issues. If you're unsure whether something fits, open a Discussion first and
I'll tell you.

Security problems are reported privately, never in a public issue. See
[SECURITY.md](SECURITY.md).

Large changes start with an issue, so we can agree on the shape before you
write the code.

Looking for something to do? Issues labelled
[good first issue](https://github.com/nanocloud-io/nanocloud/labels/good%20first%20issue)
are small and written for someone new to the code;
[help wanted](https://github.com/nanocloud-io/nanocloud/labels/help%20wanted)
ones are bigger.

### What Nano Cloud will not take

The README's [What it does not do](README.md#what-it-does-not-do) list is a
promise to the people running it, and I'll decline a pull request that breaks
it, however good it is:

- users, roles, SSO or tenants (there's one login, plus API tokens);
- clustering, or anything that spans more than one host;
- a database: state stays in libvirt's own XML and plain JSON files;
- a container runtime, Kubernetes distribution or model runtime inside the
  binary (they run in the VMs it makes), or anything else that stops it being
  one static binary;
- telemetry, or an outbound call the user didn't ask for.

## Set up

| You need | For |
|---|---|
| Go, the version in `go.mod` | the daemon and its tests |
| Node.js, the version in `.nvmrc` | the UI and the end-to-end tests |
| A running libvirtd that your user can reach | most Go tests, and the end-to-end tests |
| `xorriso` and `qemu-img` | the tests that build seed ISOs and disks |
| golangci-lint v2.12.2 | lint, as CI runs it |

The libvirt tests use libvirt's built-in `test:///default` driver: it needs
the daemon but not KVM, and no guest ever boots. On Debian or Ubuntu:

```sh
sudo apt install libvirt-daemon-system xorriso qemu-utils
sudo usermod -aG libvirt "$USER"    # then log out and back in
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
cd web && npm ci                    # the UI's dependencies
```

`go install` puts golangci-lint in `$(go env GOPATH)/bin`; add that to your
`PATH`. On a Mac, see [libvirt on a Mac](#libvirt-on-a-mac).

## Build

```sh
make all     # builds the UI into web/dist, then the binary that embeds it
```

`make build` compiles only the Go side and embeds whatever is in `web/dist`,
possibly a stale bundle from an earlier build, so use `make all` whenever the
UI changed. A local build also rewrites `web/dist/index.html`; restore it
with `git checkout -- web/dist/index.html` before committing.

## Run it

For the UI alone, with mock data (no daemon, and you're already signed in):

```sh
cd web && VITE_MOCK=true npm run dev    # http://localhost:5173
```

Add `VITE_MOCK_BACKEND=vz` to see the UI a macOS host gets.

To run it against a local daemon, give the daemon a config that keeps its
state somewhere you can write. Git ignores `config.yaml` at the repository
root, so put it there:

```yaml
libvirt_uri: test:///default    # libvirt's fake driver: one VM named "test", nothing boots
metadata_path: /tmp/nanocloud-dev/metadata.json
image_cache_dir: /tmp/nanocloud-dev/images
storage_pools:
  - {name: dev, path: /tmp/nanocloud-dev/pool, default: true}
```

Set a password, start the daemon, then start the UI in a second terminal:

```sh
mkdir -p /tmp/nanocloud-dev/pool
echo dev-password | go run ./cmd/nanocloud passwd --config ./config.yaml --stdin
go run ./cmd/nanocloud serve --config ./config.yaml    # https://localhost:8080
cd web && npm run dev                                   # http://localhost:5173
```

The daemon refuses every request until a password is set. It serves HTTPS
with a self-signed certificate, which the dev server's proxy accepts. With
`tls: {enabled: false}` in the config, start the UI with
`NANOCLOUD_DAEMON=http://localhost:8080 npm run dev` instead.

On a Mac the daemon runs the Virtualization.framework backend and ignores
`libvirt_uri`. The UI and API work, but booting a VM needs a signed build
running as root (see [MACOS.md](MACOS.md)).

For the real thing, on an x86-64 Linux host with KVM or an Apple silicon Mac,
run `make all && sudo ./nanocloud install`. `sudo nanocloud uninstall`
removes it and keeps the VMs.

## Test

| What | Command |
|---|---|
| Go | `go test -p 1 ./...` |
| UI | `cd web && npm run typecheck && npm run lint && npm test` |
| End to end | `make all`, then `cd e2e && npm ci && npx playwright install chromium && npx playwright test` |
| Go lint | `golangci-lint run` and `gofmt -l .` (prints nothing when clean) |

Keep the `-p 1`. The tests share libvirt's `test:///default` state through
the one daemon, so packages tested in parallel change the same fake domain
under each other and fail at random.

The libvirt tests skip, rather than fail, when libvirtd is unreachable. A
green run can hide dozens that never ran, so count the skips:

```sh
go test -p 1 -v ./... | grep -- '--- SKIP'
```

CI fails on any skip not named in the "No unexpected skips" step of
`.github/workflows/ci.yml`. With libvirtd, `xorriso` and `qemu-img` all
available, Linux skips at most those and a Mac three. More means libvirtd is
unreachable or a tool is missing.

`npm run format` fixes the formatting problems `npm run lint` reports.

Playwright starts the binary you built, `./nanocloud`, with
`e2e/config.e2e.yaml`: libvirt's test driver, port 18080. On a Mac that
binary uses the macOS backend, so the three specs that need libvirt's `test`
domain fail there: "dashboard lists the libvirt test domain as running",
"VM lifecycle: stop and start the test domain through the UI" and "images
view renders the built-in catalog". CI runs them on Linux.

golangci-lint runs as its own CI job and finds things `go vet` doesn't. Its
config doesn't check formatting, which is why CI also runs `gofmt`.

### libvirt on a Mac

The libvirt tests run on a Mac too, since no VM boots.
`brew install libvirt qemu xorriso`, then start the daemon with short,
writable paths (macOS caps socket paths at 104 bytes):

```sh
mkdir -p /tmp/nclv/c /tmp/nclv/r
XDG_CACHE_HOME=/tmp/nclv/c XDG_RUNTIME_DIR=/tmp/nclv/r /opt/homebrew/sbin/libvirtd -d
```

`Unable to obtain pidfile` means one is already running. If all it says is
`Unable to initialize network sockets`, run it without `-d` to see why.

The tests dial libvirt's default socket, so point them at this one for the
run and put them back before committing:

```sh
grep -rl 'test:///default' --include='*_test.go' . | xargs sed -i '' 's|test:///default|test:///default?socket=/tmp/nclv/r/libvirt/libvirt-sock|g'
go test -p 1 ./...
grep -rl 'socket=/tmp/nclv' --include='*_test.go' . | xargs sed -i '' 's|?socket=/tmp/nclv/r/libvirt/libvirt-sock||g'
```

### Real hosts

`test:///default` can't boot a guest, so nothing in CI proves that cloud-init
ran, that a guest got an address, or that a bridge passes traffic. That's
checked by hand on a Linux host with KVM, and on an Apple silicon Mac for the
macOS backend ([MACOS.md](MACOS.md) has a checklist).

If your change touches VM lifecycle, networking or cloud-init, describe that
check in the pull request: the host and its distribution, the guest image,
what you did and what you saw. No such host? Say so and I'll run it.

## Send a change

1. Fork the repository and branch from `main` (`fix/…`, `feature/…`).
2. Make one change. Two unrelated fixes are two pull requests; small ones are
   quicker to review and easier to revert.
3. For any change in behaviour, add a test that fails without it. Check that
   it does: put the bug back and watch the test fail.
4. Run the checks above.
5. Open a pull request and fill in the template.

Write comments that explain why, where a reader would otherwise reintroduce a
bug: not what the next line does, and not the story of the fix, which belongs
in the pull request. Go code is `gofmt`ed and returns errors with context
instead of logging and dropping them. The UI follows Prettier and ESLint, as
`npm run lint` checks.

CI has to be green: Go tests on Linux and macOS, UI tests, end to end, lint,
the security scans, CodeQL and dependency review. For a first-time
contributor, CI runs once I approve it.

Pull requests are squash-merged, so the title becomes the commit on `main`
and the line in the release notes. Write it for the person running Nano
Cloud: "Stop returns 404 for a VM that does not exist", not "fix handler".

Every merge to `main` is a release. The workflow tags the next version,
builds, signs and publishes it; nothing is tagged by hand.
[RELEASING.md](RELEASING.md) has each step and the release key.

## License

Nano Cloud is licensed under the [Apache License 2.0](LICENSE). By sending a
pull request, you agree that your contribution is licensed under the same
terms, as section 5 of the license says. There's no CLA and no sign-off to
add to your commits.
