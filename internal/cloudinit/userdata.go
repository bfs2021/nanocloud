package cloudinit

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// These documents are marshalled with yaml.v3, never concatenated: a colon,
// '#' or newline in a command or file would otherwise produce a document
// cloud-init rejects, leaving a guest with no login user. Field order is
// emission order.

type userDataDoc struct {
	Hostname string    `yaml:"hostname"`
	Users    []userDoc `yaml:"users"`
	// ssh_pwauth must be top-level: stock Ubuntu/Debian images ship
	// PasswordAuthentication no, and under the user the key does nothing.
	SSHPwauth     bool           `yaml:"ssh_pwauth"`
	PackageUpdate bool           `yaml:"package_update,omitempty"`
	Packages      []string       `yaml:"packages,omitempty"`
	WriteFiles    []writeFileDoc `yaml:"write_files,omitempty"`
	RunCmd        []string       `yaml:"runcmd,omitempty"`
}

type userDoc struct {
	Name string `yaml:"name"`
	// HashedPasswd is the bcrypt hash; the plaintext never reaches disk. Not
	// `passwd`, which only reaches useradd and so never sets a user the image
	// already ships.
	HashedPasswd string `yaml:"hashed_passwd"`
	// LockPasswd has no omitempty: it must emit false, or cloud-init locks the
	// account it just set a password for.
	LockPasswd bool     `yaml:"lock_passwd"`
	Sudo       string   `yaml:"sudo"`
	Groups     []string `yaml:"groups,omitempty"`
	// Shell is omitted when empty. Pinning /bin/bash breaks images without it
	// (Alpine's busybox), while omitting it gets dash on Debian/Ubuntu, so callers
	// resolve it per distro.
	Shell             string   `yaml:"shell,omitempty"`
	SSHAuthorizedKeys []string `yaml:"ssh_authorized_keys,omitempty"`
}

// writeFileDoc mirrors metadata.WriteFile so the two can change separately.
// The conversion in GenerateUserData stops compiling if they diverge.
type writeFileDoc struct {
	Path        string `yaml:"path"`
	Content     string `yaml:"content"`
	Permissions string `yaml:"permissions,omitempty"`
	Owner       string `yaml:"owner,omitempty"`
}

type metaDataDoc struct {
	InstanceID    string `yaml:"instance-id"`
	LocalHostname string `yaml:"local-hostname"`
}

// GenerateUserData renders the cloud-config user-data for params, with the
// password bcrypt-hashed.
func GenerateUserData(params Params) (string, error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(params.Password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}

	doc := userDataDoc{
		Hostname: params.Hostname,
		Users: []userDoc{{
			Name:              params.Username,
			HashedPasswd:      string(hash),
			LockPasswd:        false,
			Sudo:              "ALL=(ALL) NOPASSWD:ALL",
			Groups:            nonEmpty(params.Groups),
			Shell:             strings.TrimSpace(params.Shell),
			SSHAuthorizedKeys: nonEmpty(params.SSHKeys),
		}},
		SSHPwauth: true,
	}
	doc.Packages, doc.RunCmd = withGuestAgent(params.GuestAgent, nonEmpty(params.Packages), nonEmpty(params.RunCmd))
	// package_update follows packages: installing against a stale index is the
	// usual failure.
	doc.PackageUpdate = len(doc.Packages) > 0

	for _, f := range params.WriteFiles {
		doc.WriteFiles = append(doc.WriteFiles, writeFileDoc(f))
	}

	// Platform additions (the reboot and ready units on macOS, nothing on Linux)
	// run before the user's commands.
	if files, cmds := addon(); len(files)+len(cmds) > 0 {
		doc.WriteFiles = append(doc.WriteFiles, files...)
		doc.RunCmd = append(cmds, doc.RunCmd...)
	}

	body, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("rendering user-data: %w", err)
	}
	// cloud-init silently ignores a file without this exact first line.
	return "#cloud-config\n" + string(body), nil
}

// generateMetaData renders meta-data with a fresh random instance-id.
func generateMetaData(hostname string) (string, error) {
	id, err := newUUIDv4()
	if err != nil {
		return "", fmt.Errorf("generating instance-id: %w", err)
	}

	body, err := yaml.Marshal(metaDataDoc{InstanceID: id, LocalHostname: hostname})
	if err != nil {
		return "", fmt.Errorf("rendering meta-data: %w", err)
	}
	return string(body), nil
}

// nonEmpty drops blank entries and returns nil if none are left, so omitempty
// omits the key. `packages: []` would still run cloud-init's package stage.
func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
