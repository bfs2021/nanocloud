package cloudinit

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A clone's seed changes the guest's identity only: no provisioning keys and
// no passwd, but an explicit lock_passwd: false.
func TestGenerateCloneUserData_CarriesIdentityOnly(t *testing.T) {
	got, err := generateCloneUserData(CloneParams{
		Hostname: "web-02",
		Username: "alex",
		SSHKeys:  []string{"ssh-ed25519 AAAA"},
		Shell:    "/bin/bash",
		Groups:   []string{"wheel"},
	})
	if err != nil {
		t.Fatalf("generateCloneUserData: %v", err)
	}
	if !strings.HasPrefix(got, "#cloud-config\n") {
		t.Fatalf("missing the header cloud-init identifies the format by:\n%s", got)
	}

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("clone user-data is not valid YAML: %v\n%s", err, got)
	}
	for _, key := range []string{"packages", "package_update", "runcmd", "write_files"} {
		if _, present := doc[key]; present {
			t.Errorf("%q must not appear in a clone's seed — it already ran on the source's disk:\n%s", key, got)
		}
	}

	users, _ := doc["users"].([]any)
	if len(users) != 1 {
		t.Fatalf("want the source's login user carried over, got %#v", doc["users"])
	}
	user, _ := users[0].(map[string]any)
	for _, key := range []string{"passwd", "hashed_passwd"} {
		if _, present := user[key]; present {
			t.Errorf("%s must not appear: the source's password is bcrypt-hashed into its own seed and never stored, so any value here is invented:\n%s", key, got)
		}
	}
	// create_user still applies lock_passwd, default true, to an existing user.
	if locked, present := user["lock_passwd"]; !present || locked != false {
		t.Errorf("lock_passwd = %v (present: %v), want an explicit false:\n%s", locked, present, got)
	}
	if user["name"] != "alex" || doc["hostname"] != "web-02" {
		t.Errorf("want user alex and hostname web-02, got %#v", doc)
	}
	if groups, _ := user["groups"].([]any); len(groups) != 1 || groups[0] != "wheel" {
		t.Errorf("groups should carry over for a user the clone seed may still create, got %#v", user["groups"])
	}
	if keys, _ := user["ssh_authorized_keys"].([]any); len(keys) != 1 {
		t.Errorf("the recorded SSH keys should carry over, got %#v", user["ssh_authorized_keys"])
	}
}

// With no recorded login user, naming an empty one would create a user "".
func TestGenerateCloneUserData_OmitsUsersWhenNoneRecorded(t *testing.T) {
	got, err := generateCloneUserData(CloneParams{Hostname: "web-02"})
	if err != nil {
		t.Fatalf("generateCloneUserData: %v", err)
	}
	if strings.Contains(got, "users:") {
		t.Errorf("want no users block when no user is recorded, got:\n%s", got)
	}
}

// A new instance-id is what makes cloud-init re-run per-instance modules,
// set_hostname included, on the copied disk.
func TestCloneSeed_GetsAFreshInstanceID(t *testing.T) {
	first, err := generateMetaData("web-02")
	if err != nil {
		t.Fatalf("generateMetaData: %v", err)
	}
	second, err := generateMetaData("web-02")
	if err != nil {
		t.Fatalf("generateMetaData: %v", err)
	}
	if first == second {
		t.Errorf("two seeds share an instance-id, so a clone would keep the source's hostname:\n%s", first)
	}
}

// The guest agent is platform setup, so a clone gets it, and nothing else of
// the source's provisioning.
func TestGenerateCloneUserData_InstallsGuestAgent(t *testing.T) {
	got, err := generateCloneUserData(CloneParams{Hostname: "web-02", Username: "alex", GuestAgent: GuestAgentOpenRC})
	if err != nil {
		t.Fatalf("generateCloneUserData: %v", err)
	}
	var doc struct {
		PackageUpdate bool     `yaml:"package_update"`
		Packages      []string `yaml:"packages"`
		RunCmd        []string `yaml:"runcmd"`
	}
	if err := yaml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("clone user-data is not valid YAML: %v\n%s", err, got)
	}
	if !doc.PackageUpdate || len(doc.Packages) != 1 || doc.Packages[0] != "qemu-guest-agent" {
		t.Errorf("packages = %v (package_update %v), want only qemu-guest-agent against a fresh index:\n%s", doc.Packages, doc.PackageUpdate, got)
	}
	want := []string{"rc-update add qemu-guest-agent default", "rc-service qemu-guest-agent start"}
	if strings.Join(doc.RunCmd, "\n") != strings.Join(want, "\n") {
		t.Errorf("runcmd = %q, want %q", doc.RunCmd, want)
	}
}
