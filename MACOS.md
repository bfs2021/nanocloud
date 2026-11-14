# Nano Cloud on macOS

On an Apple silicon Mac, Nano Cloud runs VMs with Apple's
**Virtualization.framework**, the hypervisor built into macOS. Only that layer
differs from Linux. The daemon, web UI, API, image catalog, cloud-init,
templates, clones and the reconcile loop that brings VMs back are all the
same.

Nothing gets installed besides Nano Cloud itself: no Homebrew, QEMU,
qemu-img, xorriso or libvirt. Everything the daemon needs ships with macOS
(Virtualization.framework, `hdiutil`, `codesign`, `launchd`).

## Requirements

- An Apple silicon Mac (M1 or later).
- macOS 13 or later. macOS 26 is recommended, because bridged networking will
  build on vmnet APIs that arrived in macOS 26.
- Guests are arm64 Linux. The image catalog on a Mac lists only arm64 images.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/nanocloud-io/nanocloud/main/install.sh | sudo sh
```

The installer verifies the release signature with `gpg`, which macOS doesn't
ship. Install it first (`brew install gnupg`), or accept the risk with
`NANOCLOUD_SKIP_GPG=1`.

To install a binary you built yourself:

```sh
make ui && make build          # on a Mac, build also signs the binary
sudo ./nanocloud install
nanocloud doctor
```

| What | Where |
|---|---|
| Binary | `/usr/local/bin/nanocloud` |
| Config | `/etc/nano-cloud/config.yaml` |
| Images, VM disks, metadata | `/Library/Application Support/nano-cloud` |
| Logs | `/Library/Logs/nanocloud/nanocloud.log` |
| launchd job | `/Library/LaunchDaemons/io.nanocloud.nanocloud.plist` |

The daemon runs as a root launchd job, starts at boot before anyone logs in,
and serves the UI on <https://localhost:8080>. The certificate is
self-signed, so the browser warns once; `install` prints its fingerprint for
you to compare. `sudo nanocloud uninstall` removes everything except your VM
disks and metadata.

### Code signing

Virtualization.framework won't run VMs for a binary that isn't signed with
the `com.apple.security.virtualization` entitlement. The release workflow
signs release binaries when it builds them on a Mac, `make build` on a Mac
signs too, and `nanocloud install` re-signs any binary that lacks the
entitlement. An ad-hoc signature is enough, so you don't need an Apple
developer account. `nanocloud doctor` reports a missing entitlement.

## How it works

`internal/vzvm` implements the same `hypervisor.Backend` interface as the
libvirt backend. Virtualization.framework keeps no definitions of its own, so
on startup the daemon registers every VM in `metadata.json`, and the
reconcile loop starts the ones whose spec says they should be running.

A framework VM dies with the process that owns it, so each running VM gets a
`nanocloud vz-machine` helper process, started in its own session. Restarting
or upgrading the daemon leaves guests running, and the new daemon picks them
back up. Helpers talk to the daemon over sockets in
`/var/run/nanocloud` and log to `/Library/Logs/nanocloud/vms/<name>.log`.
When the Mac shuts down, each helper powers its guest off cleanly, and the VM
starts again on the next boot.

The framework stops a VM whose guest resets, so from outside a reboot looks
like a poweroff. VMs created on a Mac therefore get a tiny guest unit
(through cloud-init) that prints an invisible marker on the console as the
guest starts to reboot. The helper sees it and boots the VM straight back up,
and the browser console stays attached throughout.

The framework also can't tell which address a guest got, or whether it has
finished booting, so those VMs get a second guest unit that prints another
invisible marker, with the address, as soon as the guest has one. The VM
turns *ready* (green) the moment it arrives, as it does on Linux once the
guest has an address; cloud-init may still be running. For VMs created
before this, the address comes from macOS's DHCP leases and counts once it
answers on the SSH port.

A service's endpoints are forwarded from ports on the Mac itself, such as
`localhost:30432` for PostgreSQL, and stay the same after every reboot.
Browsers and other apps reach those without the Local Network permission
that the VMs' own `192.168.64.x` addresses need. *Reachable from other
machines* on a service's page opens its ports to your network; with the macOS
firewall on, allow `nanocloud` under System Settings → Network → Firewall →
Options.

Snapshots are copy-on-write APFS clones of every disk and of the EFI
variables, kept beside each disk in `<disk>.snapshots/`. They're instant and
use space only for blocks that change afterwards. A running VM is paused for
the moment the clones are taken, so the snapshot is consistent. Memory isn't
saved: restoring a snapshot taken while running boots from it.

Disks are raw images, because the framework boots nothing else. A cloud image
is converted to raw once (pure Go, no qemu-img), and each VM's disk is an
APFS clone of it: instant, and sharing unchanged blocks with the base.

- Boot is always UEFI, with a per-VM EFI variable store (`<name>.nvram`) and
  machine identifier (`<name>.machineid`) beside the disk.
- The cloud-init NoCloud seed ISO is built with `hdiutil`.
- The console is a serial console in the browser, on the guest's
  `/dev/hvc0`. The VM's helper keeps the recent output, so reopening the tab
  shows the prompt. There's no graphical console.
- Host CPU and memory are reported. Per-VM CPU and memory aren't, because the
  framework doesn't expose them.

## Networking and VLANs

| Mode | Linux | macOS today | macOS next |
|---|---|---|---|
| NAT | libvirt `default` network | **Yes**, framework NAT, 192.168.64.0/24 | — |
| Bridge (untagged) | host bridge with a physical port | No | vmnet bridged mode, wired NIC only |
| VLAN | `eth0.<id>` + `nc-vlan<id>` built on demand | No | Experimental, after a spike |

NAT needs no setup. Every VM gets an address from macOS's own DHCP server
and reports it on its console once it's up (see
[How it works](#how-it-works)). The Mac can reach every VM directly
(`ssh user@192.168.64.x`); other machines on your LAN can't.

Bridged networking isn't available yet. Apple's built-in bridged attachment
needs an entitlement Apple grants only on request, and a self-signed binary
that carries it is killed at launch. The way around it is the vmnet
framework's bridged mode, which works for a process running as root, as
Nano Cloud's daemon does. It needs macOS 26 and a wired interface; Wi-Fi
can't be bridged.

VLANs have no API on macOS. macOS can create a host VLAN interface
(`ifconfig vlan20 create; ifconfig vlan20 vlan 20 vlandev en0`), but whether
vmnet will bridge onto it is still unknown, so this needs a spike before it's
promised. Until then the API refuses a VLAN on macOS with a clear error, and
VLANs stay a Linux feature.

## What is different from Linux

| Feature | macOS |
|---|---|
| Snapshots | Disk and EFI state, instant; memory is not saved, so restoring a running snapshot boots from it |
| VNC console | Serial console instead |
| PCI/USB passthrough | Not available |
| Firmware choice, secure boot, TPM | Always UEFI; no secure boot or TPM |
| CPU model, topology, pinning | Not configurable; the guest gets plain vCPUs |
| Daemon restart or upgrade | Guests keep running |
| In-guest `reboot` | Works for VMs created on a Mac with this version or later. Older VMs power off instead; a VM whose `restart_policy` is `always` comes straight back (set in `metadata.json`) |
| `nanocloud uninstall` | Shuts running VMs down cleanly first, since nothing would be left to manage them |

Every refusal happens before anything is written to disk, and the UI hides
what the backend can't do. `GET /system` reports the backend and its
capabilities.

## Verifying a build by hand

CI runs the macOS backend's tests on a macOS runner, and the release workflow
builds and signs the macOS binary there. GitHub's macOS runners can't boot a
guest, though, so booting is checked by hand on an Apple silicon Mac:

1. `make ui && make build && sudo ./nanocloud install`, then `nanocloud doctor`. Every required check should pass.
2. `curl -sk https://localhost:8080/system` should report `"backend":"vz"`.
3. In the UI, Images lists only arm64 images. Pull Ubuntu 24.04.
4. Create a VM with NAT, 2 vCPUs, 2 GB RAM and a 20 GB disk with your SSH key. Its card shows what it's waiting for, then turns green (*ready*) with a 192.168.64.x address within about a minute, and `ssh` works. In the guest, `df -h /` shows about 20 GB and `systemctl status nanocloud-ready` shows the ready unit ran.
5. The Console tab shows a login prompt, and typing works.
6. `sudo launchctl kickstart -k system/io.nanocloud.nanocloud` restarts the daemon. The VM keeps running throughout (`ssh` stays connected), and the UI shows it running again after the restart. `ps ax | grep vz-machine` shows its helper.
7. `sudo reboot` in the guest: the badge goes amber, then green again. The VM comes back by itself within a minute, the console stays open, and the IP is unchanged. A VM created with v0.1.58 also turns green once its SSH port answers.
8. `sudo poweroff` in the guest leaves the VM stopped, with no restart loop.
9. Take a snapshot while the VM runs, change a file in the guest, restore the snapshot: the VM reboots with the file as it was. Then take and restore one while it's stopped.
10. Attach a second disk, resize the boot disk (refused while snapshots exist), clone the stopped VM, then delete both.
11. Reboot the Mac. The VM starts again on its own once the daemon is up.
