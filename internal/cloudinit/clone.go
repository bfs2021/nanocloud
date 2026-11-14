package cloudinit

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// CloneParams describes the seed a cloned VM boots with. The disk is a copy of
// an already provisioned machine, so the seed gives it a new identity: a new
// instance-id, which makes cloud-init re-run its per-instance modules, and a
// new hostname.
//
// It carries none of the source's provisioning, which already ran on the
// copied disk, and no password, since only its hash was ever kept. The user
// is still named with its keys, so cloning a VM whose first boot never
// finished still yields an account to log into.
type CloneParams struct {
	// Hostname is the clone's name.
	Hostname string
	// Username and SSHKeys are the source's, recorded in its spec.
	Username string
	SSHKeys  []string
	// Shell and Groups are resolved per distro, as at create time.
	Shell  string
	Groups []string
	// GuestAgent is resolved the same way. It is platform setup rather than the
	// user's provisioning, so a clone of a VM created without it gains it.
	GuestAgent GuestAgent
}

// cloneDataDoc has no hashed_passwd, so the clone keeps the password on its
// disk.
type cloneDataDoc struct {
	Hostname string         `yaml:"hostname"`
	Users    []cloneUserDoc `yaml:"users,omitempty"`
	// Restated because a VM cloned before its first boot finished never had it
	// applied.
	SSHPwauth     bool     `yaml:"ssh_pwauth"`
	PackageUpdate bool     `yaml:"package_update,omitempty"`
	Packages      []string `yaml:"packages,omitempty"`
	RunCmd        []string `yaml:"runcmd,omitempty"`
}

type cloneUserDoc struct {
	Name string `yaml:"name"`
	Sudo string `yaml:"sudo"`
	// LockPasswd must be stated: create_user still applies it to an existing
	// user, defaulting to true, which would lock the account the clone is reached
	// through.
	LockPasswd        bool     `yaml:"lock_passwd"`
	Groups            []string `yaml:"groups,omitempty"`
	Shell             string   `yaml:"shell,omitempty"`
	SSHAuthorizedKeys []string `yaml:"ssh_authorized_keys,omitempty"`
}

// BuildClone writes a clone's seed ISO to destPath.
func BuildClone(p CloneParams, destPath string) error {
	userData, err := generateCloneUserData(p)
	if err != nil {
		return err
	}
	metaData, err := generateMetaData(p.Hostname)
	if err != nil {
		return fmt.Errorf("generating meta-data: %w", err)
	}
	return writeSeed(userData, metaData, destPath)
}

func generateCloneUserData(p CloneParams) (string, error) {
	if strings.TrimSpace(p.Hostname) == "" {
		return "", errors.New("cloudinit: hostname is required")
	}

	doc := cloneDataDoc{Hostname: p.Hostname, SSHPwauth: true}
	if user := strings.TrimSpace(p.Username); user != "" {
		doc.Users = []cloneUserDoc{{
			Name:              user,
			Sudo:              "ALL=(ALL) NOPASSWD:ALL",
			LockPasswd:        false,
			Groups:            nonEmpty(p.Groups),
			Shell:             strings.TrimSpace(p.Shell),
			SSHAuthorizedKeys: nonEmpty(p.SSHKeys),
		}}
	}

	doc.Packages, doc.RunCmd = withGuestAgent(p.GuestAgent, nil, nil)
	doc.PackageUpdate = len(doc.Packages) > 0

	body, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("rendering clone user-data: %w", err)
	}
	return "#cloud-config\n" + string(body), nil
}
