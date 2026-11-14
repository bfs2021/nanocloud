# Security

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub's
[private vulnerability reporting](https://github.com/nanocloud-io/nanocloud/security/advisories/new),
not in a public issue. Include the version (`nanocloud version`), the
platform and the steps to reproduce.

You'll get an answer within a week. Every merge to `main` is released, so a
fix ships as a normal release, and the advisory is published once it's out.
Only the latest release receives fixes.

## Threat model

Nano Cloud manages VMs on the machine it runs on, so the daemon runs as
**root**: it defines domains, writes disks and creates network interfaces.
Whoever controls the daemon controls the host, and everything below follows
from that.

It's built for one administrator: one password and any number of API tokens,
all with full access. There are no users, roles or read-only access, and
there won't be. Don't give a login to anyone you wouldn't give root on the
host.

### What the login protects

- Every API route and console needs a login, except `/health`, the login
  endpoints and the static UI. Until `sudo nanocloud passwd` sets a password,
  the daemon refuses everything.
- The password is stored as a bcrypt hash, and sessions and API tokens only
  as SHA-256 hashes of 32 random bytes, so reading `auth.json` (mode 0600)
  doesn't let anyone sign in.
- A browser session is split in two. A cookie (`HttpOnly`, `Secure`,
  `SameSite=Strict`) carries one half; the page keeps the other in its own
  storage and sends it with each request. Browsers send a cookie to every
  port on the host, including the service ports forwarded into your VMs, so
  the cookie alone signs nothing in. A session lasts 30 days, renewed while
  it's used.
- State-changing requests and every console WebSocket are rejected when
  `Sec-Fetch-Site` or `Origin` says they came from another site. A web page
  you visit can't drive the API or open a console with your cookie.
- Creating or revoking an API token and changing the password need a browser
  session, so a leaked token can't mint its own replacements. Revoke tokens
  in Settings → Account & security.
- Password guessing is slowed per client address, and an IPv6 client counts
  as its whole /64. After a few failures each one doubles the wait, up to 15
  minutes, and only a few passwords are checked at once.
- Traffic is encrypted. The daemon serves HTTPS with a certificate it
  generates on first start, or one you configure (`cert` and `key` under
  `tls:` in `config.yaml`). The installer prints the self-signed
  certificate's fingerprint, and Settings shows it, so you can check it
  against what your browser is shown.
- `nanocloud update` and `install.sh` check a release's GPG signature against
  a key built into them, then each binary's SHA-256, before replacing
  anything.

### What it does not protect

Anyone with root owns the box, and today so does any local account. VM disks
and cloud-init seed ISOs are readable by every account on the host, and a
service's seed carries its database passwords, S3 keys or cluster token in
plain text. Disks aren't encrypted at rest. Run Nano Cloud on a host where
every login belongs to someone you'd trust with root. Restricting these files
to the daemon and QEMU is planned.

Also outside what it protects:

- A guest isn't a sandbox from its own network. VMs on the NAT network can
  reach each other, and a bridged VM is on your LAN like any other machine.
- The VMs themselves. Their passwords, SSH keys, open ports and updates are
  yours to manage. A VM's login password is hashed before it's written to the
  seed ISO, though the plaintext passes through the API; a service's
  generated passwords aren't hashed (see above).
- With `tls: {enabled: false}`, meant for a reverse proxy or local
  development, the password and session cookie cross the network in clear
  unless something in front adds TLS.
- A self-signed certificate protects against eavesdropping, but only checking
  its fingerprint protects against an attacker in the middle on first use.
- The daemon listens on every interface and isn't hardened for the open
  internet. Keep port 8080 to your LAN or a VPN.

### Release signing key

Releases are signed with this key. `install.sh` and `nanocloud update` carry
it and check every release against it.

```
nano-cloud releases <releases@nanocloud.io>
ed25519, fingerprint 5289 9456 DD0B C728 383E  1910 39DE 3648 2CBD 9253
expires 2028-07-03
```

The public key is in the repository at `internal/update/release.asc`.

### Outbound connections

The daemon checks GitHub for a newer release at most every 12 hours (turn it
off under Settings → Updates). Anything else it fetches, you asked for: cloud
images from each distribution's mirror, and SSH keys from GitHub when you
name a GitHub user. There's no telemetry.
