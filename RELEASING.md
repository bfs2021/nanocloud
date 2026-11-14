# Releasing Nano Cloud

Every merge to `main` is a release, and nobody tags by hand.

## What a merge does

`.github/workflows/release.yml` runs on every push to `main`. It:

1. Takes the newest `vX.Y.Z` tag and bumps the patch number. A repository
   with no tags starts at `v0.2.0`, so hosts on the old v0.1.x line see it as
   newer. If that tag already exists, the run stops.
2. Runs the whole CI suite from `ci.yml` (Go tests, UI tests, E2E, lint,
   security). A failure here publishes nothing.
3. Builds `nanocloud-darwin-arm64` with cgo on a macOS runner and signs it ad
   hoc with the Virtualization entitlement. If this fails, the release fails:
   it never ships without the Mac binary.
4. Cross-compiles `nanocloud-linux-amd64` and `nanocloud-linux-arm64`.
5. Writes `sha256sums.txt` for all three binaries and signs it with the
   release key (`sha256sums.txt.sig`).
6. Attaches an SPDX SBOM and, on a public repository, records a
   build-provenance attestation for each binary.
7. Creates the tag and the GitHub Release, with generated notes.

A run that fails, or one lost to a GitHub outage, takes no number. The next
successful merge publishes the same version.

## The release key

Two copies of one public key check every release. `install.sh`
(`RELEASE_PUBKEY`) checks a fresh install, and
`internal/update/release.asc`, compiled into the binary, checks
`nanocloud update`. `TestReleaseKeyMatchesInstallScript` fails if the two
differ.

The private key lives only in the `GPG_PRIVATE_KEY` Actions secret and in an
offline backup. The workflow signs with an empty passphrase, so the key must
have none; the secret store is what protects it.

### Setting it up

```sh
gpg --batch --gen-key <<'EOF'
Key-Type: eddsa
Key-Curve: ed25519
Key-Usage: sign
Name-Real: nano-cloud releases
Name-Email: releases@nanocloud.io
Expire-Date: 2y
%no-protection
EOF

gpg --list-secret-keys --keyid-format=long "nano-cloud releases"
gpg --armor --export KEYID          # into install.sh and internal/update/release.asc
gpg --armor --export-secret-keys KEYID   # into the GPG_PRIVATE_KEY secret
```

### Rotating it

`nanocloud update` checks a release against the key inside the binary that's
already running, so a release signed only with a new key fails on every
existing host. Rotate in two releases:

1. Put the new public key in both places and merge. This release is still
   signed with the old key, so every host can install it, and it carries the
   new key.
2. Replace the `GPG_PRIVATE_KEY` secret with the new private key. Releases
   from then on are signed with it.

If the old key leaks, step 1 can't be trusted. Publish the new key, announce
the leak, and ask users to reinstall with `install.sh`, which fetches the key
from the repository.

### Before it expires

The current key, fingerprint `5289 9456 DD0B C728 383E 1910 39DE 3648 2CBD 9253`,
expires on 2028-07-03. Well before then, extend it with
`gpg --quick-set-expire <fingerprint> 2y` and do step 1 above with the
re-exported public key, so every host carries the extended key before the
old date passes.

## Checking a release yourself

```sh
gpg --verify sha256sums.txt.sig sha256sums.txt
sha256sum -c --ignore-missing sha256sums.txt
gh attestation verify nanocloud-linux-amd64 -R nanocloud-io/nanocloud
```

## Keeping the image catalog fresh

`internal/image/catalog.go` lists the cloud images offered in the Create VM
wizard. A pinned URL breaks as soon as the distro ships a respin and removes
the old file, so when you add or change an entry, use the first of these the
distro supports:

1. Resolve it at pull time. Set `IndexURL`, `FilePattern` and
   `ChecksumType`, and `DirIndexResolver` picks the newest matching file from
   the directory listing. Ubuntu, Debian, Alpine and Fedora work this way.
2. Point at the distro's own "latest" alias. Some, such as Rocky and
   AlmaLinux, publish a fixed filename and move it to each new build
   themselves.
3. Pin the URL, only when neither of those exists, and expect to update it.

Check every entry against the live mirrors before merging:

```sh
go test -tags=netcheck ./internal/image/... -run TestNetcheck -v
```
