package api

import (
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/nanocloud-io/nanocloud/internal/cloudinit"
	"github.com/nanocloud-io/nanocloud/internal/hypervisor"
	"github.com/nanocloud-io/nanocloud/internal/image"
)

// TestLoginShellFor checks that every distro gets bash as its login shell
// except Alpine, which has only busybox /bin/sh.
func TestLoginShellFor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		distro string
		want   string
	}{
		{"ubuntu", "/bin/bash"},
		{"debian", "/bin/bash"},
		{"fedora", "/bin/bash"},
		{"rocky", "/bin/bash"},
		{"alma", "/bin/bash"},
		{"opensuse", "/bin/bash"},
		{"alpine", ""},
	}

	for _, c := range cases {
		if got := loginShellFor(c.distro); got != c.want {
			t.Errorf("loginShellFor(%q) = %q, want %q", c.distro, got, c.want)
		}
	}
}

// vzBackend reports the macOS backend's capabilities and nothing else.
type vzBackend struct{ hypervisor.Backend }

func (vzBackend) Capabilities() hypervisor.Capabilities {
	return hypervisor.Capabilities{Backend: hypervisor.BackendVZ}
}

// TestSeed_InstallsGuestAgentOnEveryCatalogDistro renders each catalog image's
// seed as handleVMCreate does. Without the agent a bridged guest's IP is never
// confirmed, and the VM reads no-network while healthy.
func TestSeed_InstallsGuestAgentOnEveryCatalogDistro(t *testing.T) {
	libvirtSrv := &Server{}
	for _, entry := range image.Catalog {
		t.Run(entry.ID, func(t *testing.T) {
			wantStart := "systemctl start qemu-guest-agent"
			if entry.Distro == distroAlpine {
				wantStart = "rc-service qemu-guest-agent start"
			}
			out, err := cloudinit.GenerateUserData(cloudinit.Params{
				Hostname: "h", Username: "u", Password: "p",
				Shell:      loginShellFor(entry.Distro),
				GuestAgent: libvirtSrv.guestAgentFor(entry.Distro),
			})
			if err != nil {
				t.Fatalf("GenerateUserData: %v", err)
			}
			var doc struct {
				PackageUpdate bool     `yaml:"package_update"`
				Packages      []string `yaml:"packages"`
				RunCmd        []string `yaml:"runcmd"`
			}
			if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatalf("parse user-data: %v", err)
			}
			if !slices.Contains(doc.Packages, "qemu-guest-agent") || !doc.PackageUpdate {
				t.Errorf("packages = %v (package_update %v), want qemu-guest-agent installed against a fresh index", doc.Packages, doc.PackageUpdate)
			}
			if !slices.Contains(doc.RunCmd, wantStart) {
				t.Errorf("runcmd = %v, want %q", doc.RunCmd, wantStart)
			}
		})
	}

	// The vz backend has no agent channel: installing the daemon there only
	// slows the first boot.
	if got := (&Server{vmManager: vzBackend{}}).guestAgentFor("ubuntu"); got != "" {
		t.Errorf("guestAgentFor on the vz backend = %q, want none", got)
	}
}

// TestAdminGroupsFor: Alpine has no sudo, so wheel is its user's only way to
// root; everywhere else the seed must stay as it was.
func TestAdminGroupsFor(t *testing.T) {
	t.Parallel()

	for _, distro := range []string{"ubuntu", "debian", "fedora", "rocky", "alma", "opensuse"} {
		if got := adminGroupsFor(distro); got != nil {
			t.Errorf("adminGroupsFor(%q) = %q, want none", distro, got)
		}
	}
	if got := adminGroupsFor("alpine"); len(got) != 1 || got[0] != "wheel" {
		t.Errorf(`adminGroupsFor("alpine") = %q, want ["wheel"]`, got)
	}
}
