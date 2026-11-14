# Architecture

Nano Cloud is one Go binary that manages VMs on the machine it runs on. It
holds the daemon, a JSON API and a React UI embedded with `embed.FS`. There's
no database: state is the hypervisor's own records plus a few JSON files.

```
browser ──HTTPS──▶ nanocloud serve
                    ├─ internal/api        routes, auth, consoles (WebSocket)
                    ├─ internal/reconcile  keeps VMs the way you left them
                    ├─ internal/services   databases, S3, Kubernetes as recipes
                    └─ internal/hypervisor ─┬─ internal/vm     libvirt (Linux)
                                            └─ internal/vzvm   Virtualization.framework (macOS)
```

## The spec is the machine

`metadata.json` records what you asked for, per VM: whether it should run,
and its pool, network, hardware and cloud-init settings. Every lifecycle call
writes the spec **before** it calls the hypervisor, so a crash in between
still leaves your intent on disk.

Every 10 seconds, `internal/reconcile` compares that spec with what the
hypervisor reports. A VM that should run but doesn't is started again, with
back-off, until it gives up and says so. If a VM's definition is gone but its
disk is still there, it's rebuilt from the spec with the same UUID and MAC,
so it keeps its address. A VM you stopped stays stopped. Every decision goes
to the activity feed (`internal/events`, an in-memory ring).

Writes go through `internal/atomicfile` or the metadata store: a temporary
file, `fsync`, `rename`, then an `fsync` of the directory, all under a
`flock` on a sidecar lock file. A power cut leaves the old file or the new
one, never half of each.

## Two hypervisors, one interface

`internal/hypervisor` is the seam: the API and the reconcile loop only ever
see `hypervisor.Backend`.

On Linux, `internal/vm` drives libvirt over its socket (go-libvirt, no cgo).
Disks are qcow2 overlays on a cached cloud image, cloud-init seeds are ISOs
built with `xorriso`, and consoles are VNC, proxied over a WebSocket to
noVNC.

On macOS, `internal/vzvm` uses Apple's Virtualization.framework (cgo). Each
running VM lives in its own helper process (`nanocloud vz-machine`), so
restarting the daemon leaves guests running, and `internal/vswitch` is a
small Ethernet switch between them. [MACOS.md](MACOS.md) has the rest.

`GET /system` reports which backend is running and what it can do, and the
UI hides what it can't.

## Packages

| Package | Job |
|---|---|
| `cmd/nanocloud` | Subcommands: `serve`, `install`, `uninstall`, `update`, `doctor`, `passwd`, `version` |
| `internal/api` | HTTP routes, the auth middleware, console proxies |
| `internal/auth` | The one password, sessions, API tokens, login rate limiting (`auth.json`) |
| `internal/tlscert` | Self-signed certificate on first start, or the configured one |
| `internal/settings` | Options changed from the UI (`settings.json`), layered over `config.yaml` |
| `internal/metadata` | The VM spec store (`metadata.json`) |
| `internal/reconcile` | The loop described above |
| `internal/services` | Service recipes (`catalog/*.yaml`): VMs plus cloud-init that make a database, S3, a Kubernetes cluster or monitoring |
| `internal/template` | Saved VM shapes for reuse |
| `internal/image` | The image catalog, downloads and the cache |
| `internal/cloudinit` | User data and the seed ISO |
| `internal/storage` | Pools, disks, snapshots |
| `internal/network`, `internal/hostnet` | NAT and bridges; VLAN interfaces created on demand, never removed |
| `internal/hostdev` | PCI and USB devices that can be passed through |
| `internal/stats`, `internal/system` | Host and VM CPU and memory samples |
| `internal/update` | Finds the latest release and fetches it with its signature checked |
| `internal/install` | Installs the systemd unit or launchd job, and removes it |
| `web/` | The React UI, built with Vite into `web/dist` |
| `e2e/` | Playwright tests against the real binary |

## Requests

Everything comes in through `api.Server.ServeHTTP`. Every request passes the
auth check there (cookie or `Authorization: Bearer` token), and
state-changing requests from another site are refused. The service manager
drives the same routes in-process, below that check, so it needs no
credentials.

The UI polls instead of holding a socket open: the VM list every 10 seconds
(every 3 while one boots), host load every 5. Only the consoles use
WebSockets.

## On disk

| Linux | macOS | What |
|---|---|---|
| `/usr/local/bin/nanocloud` | same | The binary |
| `/etc/nano-cloud/config.yaml` | same | Configuration, read once at start |
| `/var/lib/nano-cloud/` | `/Library/Application Support/nano-cloud/` | Spec, auth, settings, images, VM disks |
| systemd `nanocloud.service` | launchd `io.nanocloud.nanocloud` | The service |

The systemd unit is sandboxed (`ProtectSystem=strict`), so the daemon can
write only under `/var/lib/nano-cloud` and paths you add to `ReadWritePaths`.
