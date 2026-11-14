package image

// CatalogEntry describes a cloud image available to pull. Static entries use
// URL/ChecksumURL/Filename directly; entries with an IndexURL are resolved at
// pull time by DirIndexResolver.
type CatalogEntry struct {
	ID      string `json:"id"`                // unique slug, e.g. "ubuntu-2404-standard"
	Name    string `json:"name"`              // display name
	Distro  string `json:"os"`                // distro tag: ubuntu, debian, fedora, alpine, rocky, alma, opensuse
	Arch    string `json:"arch"`              // cpu architecture, e.g. "amd64"
	Variant string `json:"variant,omitempty"` // "standard", "minimal", or ""
	Version string `json:"version"`

	// Static download fields, used directly when IndexURL is empty.
	URL         string `json:"url"`
	ChecksumURL string `json:"checksum_url"`
	Filename    string `json:"filename"`
	ApproxBytes int64  `json:"size_bytes"` // rough size for progress estimation

	// Dynamic resolution fields. When IndexURL is set, the resolved values
	// replace URL/ChecksumURL/Filename at pull time.
	IndexURL     string `json:"-"` // directory-listing URL (Apache/Nginx autoindex)
	FilePattern  string `json:"-"` // regexp matching the target image filename
	ChecksumType string `json:"-"` // preferred checksum kind: "sha256" or "sha512"
}

// Catalog is the built-in list of supported cloud images, all cloud-init-ready
// and from official distro mirrors. Never pin a URL whose filename carries a
// build or point-release number: it 404s on the next respin. Use IndexURL
// resolution, or a server-side "latest" alias the distro repoints itself.
var Catalog = []CatalogEntry{
	{
		ID:          "ubuntu-2404-standard",
		Name:        "Ubuntu 24.04 LTS",
		Distro:      "ubuntu",
		Arch:        "amd64",
		Variant:     "standard",
		Version:     "24.04 LTS",
		ApproxBytes: 650 * 1024 * 1024,
		// Dynamic resolution.
		IndexURL:     "https://cloud-images.ubuntu.com/releases/24.04/release/",
		FilePattern:  `^ubuntu-24\.04-server-cloudimg-amd64\.img$`,
		ChecksumType: string(ChecksumSHA256),
		// Static fallback.
		URL:         "https://cloud-images.ubuntu.com/releases/24.04/release/ubuntu-24.04-server-cloudimg-amd64.img",
		ChecksumURL: "https://cloud-images.ubuntu.com/releases/24.04/release/SHA256SUMS",
		Filename:    "ubuntu-24.04-server-cloudimg-amd64.img",
	},
	{
		ID:           "ubuntu-2204-standard",
		Name:         "Ubuntu 22.04 LTS",
		Distro:       "ubuntu",
		Arch:         "amd64",
		Variant:      "standard",
		Version:      "22.04 LTS",
		ApproxBytes:  600 * 1024 * 1024,
		IndexURL:     "https://cloud-images.ubuntu.com/releases/22.04/release/",
		FilePattern:  `^ubuntu-22\.04-server-cloudimg-amd64\.img$`,
		ChecksumType: string(ChecksumSHA256),
		URL:          "https://cloud-images.ubuntu.com/releases/22.04/release/ubuntu-22.04-server-cloudimg-amd64.img",
		ChecksumURL:  "https://cloud-images.ubuntu.com/releases/22.04/release/SHA256SUMS",
		Filename:     "ubuntu-22.04-server-cloudimg-amd64.img",
	},
	// Debian publishes SHA512SUMS, not SHA256SUMS.
	{
		ID:           "debian-12",
		Name:         "Debian 12 (Bookworm)",
		Distro:       "debian",
		Arch:         "amd64",
		Version:      "12",
		ApproxBytes:  550 * 1024 * 1024,
		IndexURL:     "https://cloud.debian.org/images/cloud/bookworm/latest/",
		FilePattern:  `^debian-12-genericcloud-amd64\.qcow2$`,
		ChecksumType: string(ChecksumSHA512),
		// Static fallback.
		URL:         "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2",
		ChecksumURL: "https://cloud.debian.org/images/cloud/bookworm/latest/SHA512SUMS",
		Filename:    "debian-12-genericcloud-amd64.qcow2",
	},
	// Alpine publishes per-file .sha512 sidecars.
	{
		ID:           "alpine-3.20",
		Name:         "Alpine Linux 3.20",
		Distro:       "alpine",
		Arch:         "amd64",
		Version:      "3.20",
		ApproxBytes:  180 * 1024 * 1024,
		IndexURL:     "https://dl-cdn.alpinelinux.org/alpine/v3.20/releases/cloud/",
		FilePattern:  `^nocloud_alpine-3\.\d+\.\d+-x86_64-bios-cloudinit-r\d+\.qcow2$`,
		ChecksumType: string(ChecksumSHA512),
		// Static fallback, pinned to 3.20.0.
		URL:         "https://dl-cdn.alpinelinux.org/alpine/v3.20/releases/cloud/nocloud_alpine-3.20.0-x86_64-bios-cloudinit-r0.qcow2",
		ChecksumURL: "https://dl-cdn.alpinelinux.org/alpine/v3.20/releases/cloud/nocloud_alpine-3.20.0-x86_64-bios-cloudinit-r0.qcow2.sha512",
		Filename:    "nocloud_alpine-3.20.0-x86_64-bios-cloudinit-r0.qcow2",
	},
	// Fedora puts the respin build number ("41-1.4") in the image and CHECKSUM
	// filenames, so only IndexURL resolution stays current. Its BSD-format
	// CHECKSUM file is found by the resolver's "*-CHECKSUM" fallback.
	{
		ID:          "fedora-41",
		Name:        "Fedora Cloud 41",
		Distro:      "fedora",
		Arch:        "amd64",
		Version:     "41",
		ApproxBytes: 600 * 1024 * 1024,
		// Dynamic resolution.
		IndexURL:     "https://download.fedoraproject.org/pub/fedora/linux/releases/41/Cloud/x86_64/images/",
		FilePattern:  `^Fedora-Cloud-Base-Generic-41-\d+\.\d+\.x86_64\.qcow2$`,
		ChecksumType: string(ChecksumSHA256),
		// Static fallback. The cache key stays this Filename even when a newer
		// respin is downloaded; see puller.go.
		URL:         "https://download.fedoraproject.org/pub/fedora/linux/releases/41/Cloud/x86_64/images/Fedora-Cloud-Base-Generic-41-1.4.x86_64.qcow2",
		ChecksumURL: "https://download.fedoraproject.org/pub/fedora/linux/releases/41/Cloud/x86_64/images/Fedora-Cloud-41-1.4-x86_64-CHECKSUM",
		Filename:    "Fedora-Cloud-Base-Generic-41-1.4.x86_64.qcow2",
	},
	// ".latest." is a server-side alias Rocky repoints on every respin.
	{
		ID:          "rocky-9",
		Name:        "Rocky Linux 9",
		Distro:      "rocky",
		Arch:        "amd64",
		Version:     "9",
		ApproxBytes: 1200 * 1024 * 1024,
		URL:         "https://download.rockylinux.org/pub/rocky/9/images/x86_64/Rocky-9-GenericCloud-Base.latest.x86_64.qcow2",
		ChecksumURL: "https://download.rockylinux.org/pub/rocky/9/images/x86_64/Rocky-9-GenericCloud-Base.latest.x86_64.qcow2.CHECKSUM",
		Filename:    "Rocky-9-GenericCloud-Base.latest.x86_64.qcow2",
	},
	// "-latest." is a server-side alias, as for Rocky.
	{
		ID:          "almalinux-9",
		Name:        "AlmaLinux 9",
		Distro:      "alma",
		Arch:        "amd64",
		Version:     "9",
		ApproxBytes: 1000 * 1024 * 1024,
		URL:         "https://repo.almalinux.org/almalinux/9/cloud/x86_64/images/AlmaLinux-9-GenericCloud-latest.x86_64.qcow2",
		ChecksumURL: "https://repo.almalinux.org/almalinux/9/cloud/x86_64/images/CHECKSUM",
		Filename:    "AlmaLinux-9-GenericCloud-latest.x86_64.qcow2",
	},
	// openSUSE repoints this unversioned filename and its .sha256 sidecar on
	// every 15.6 respin.
	{
		ID:          "opensuse-leap-156",
		Name:        "openSUSE Leap 15.6",
		Distro:      "opensuse",
		Arch:        "amd64",
		Version:     "15.6",
		ApproxBytes: 600 * 1024 * 1024,
		URL:         "https://download.opensuse.org/distribution/leap/15.6/appliances/openSUSE-Leap-15.6-Minimal-VM.x86_64-Cloud.qcow2",
		ChecksumURL: "https://download.opensuse.org/distribution/leap/15.6/appliances/openSUSE-Leap-15.6-Minimal-VM.x86_64-Cloud.qcow2.sha256",
		Filename:    "openSUSE-Leap-15.6-Minimal-VM.x86_64-Cloud.qcow2",
	},
}

// EntryByID returns the catalog entry with the given ID, or false if not found.
func EntryByID(id string) (CatalogEntry, bool) {
	for _, e := range Catalog {
		if e.ID == id {
			return e, true
		}
	}
	return CatalogEntry{}, false
}
