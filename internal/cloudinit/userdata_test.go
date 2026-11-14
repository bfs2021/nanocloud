package cloudinit

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

func base() Params {
	return Params{Hostname: "test-host", Username: "ubuntu", Password: "s3cr3t"}
}

// parseUserData renders the document and parses it back. Only a parsed read
// proves escaping; a substring check passes for a value YAML splits.
func parseUserData(t *testing.T, p Params) map[string]any {
	t.Helper()

	out, err := GenerateUserData(p)
	if err != nil {
		t.Fatalf("GenerateUserData: %v", err)
	}
	if !strings.HasPrefix(out, "#cloud-config\n") {
		t.Fatalf("user-data does not start with the header cloud-init identifies the format by:\n%s", out)
	}

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("rendered user-data is not valid YAML: %v\n%s", err, out)
	}
	return doc
}

// linuxSeed disables the macOS platform additions, so tests that pin exact
// output also pass on a Mac.
func linuxSeed(t *testing.T) {
	t.Helper()
	saved := addon
	addon = func() ([]writeFileDoc, []string) { return nil, nil }
	t.Cleanup(func() { addon = saved })
}

// TestGenerateUserData_SurvivesValuesThatBreakYAML guards marshalling over
// concatenation: each value below breaks a hand-built document.
func TestGenerateUserData_SurvivesValuesThatBreakYAML(t *testing.T) {
	linuxSeed(t)
	const script = `echo "hello: world" # not a comment`
	const conf = "server {\n  listen 80;\n}\n"

	p := base()
	p.Hostname = "host: with a colon"
	p.RunCmd = []string{script, "curl -fsSL https://example.com | sh"}
	p.Packages = []string{"nginx", "git"}
	p.WriteFiles = []metadata.WriteFile{{Path: "/etc/nginx/nginx.conf", Content: conf, Permissions: "0644"}}

	doc := parseUserData(t, p)

	if got := doc["hostname"]; got != p.Hostname {
		t.Errorf("hostname = %q, want %q — a colon in a value must not end the key", got, p.Hostname)
	}
	runcmd, _ := doc["runcmd"].([]any)
	if len(runcmd) != 2 || runcmd[0] != script {
		t.Errorf("runcmd[0] = %q, want %q — the '#' must not be read as a comment", runcmd, script)
	}
	files, _ := doc["write_files"].([]any)
	if len(files) != 1 {
		t.Fatalf("write_files = %v, want one entry", files)
	}
	if got := files[0].(map[string]any)["content"]; got != conf {
		t.Errorf("content = %q, want %q — a multi-line file must arrive whole", got, conf)
	}
}

// TestGenerateUserData_PackagesBringTheirIndexUpdate: packages imply
// package_update, since a stale index is the usual install failure.
func TestGenerateUserData_PackagesBringTheirIndexUpdate(t *testing.T) {
	linuxSeed(t)
	withPackages := parseUserData(t, func() Params { p := base(); p.Packages = []string{"nginx"}; return p }())
	if withPackages["package_update"] != true {
		t.Errorf("package_update = %v, want true whenever packages are named", withPackages["package_update"])
	}

	// Absent otherwise, or cloud-init runs a package stage for nothing.
	plain := parseUserData(t, base())
	for _, key := range []string{"package_update", "packages", "runcmd", "write_files"} {
		if _, ok := plain[key]; ok {
			t.Errorf("%s is present on a VM that asked for none: %v", key, plain[key])
		}
	}
}

// TestGenerateUserData_LockPasswdSurvivesOmitempty guards the explicit
// lock_passwd: false that keeps cloud-init from locking the account.
func TestGenerateUserData_LockPasswdSurvivesOmitempty(t *testing.T) {
	doc := parseUserData(t, base())

	users, _ := doc["users"].([]any)
	if len(users) != 1 {
		t.Fatalf("users = %v, want exactly one", users)
	}
	user := users[0].(map[string]any)
	if locked, ok := user["lock_passwd"]; !ok || locked != false {
		t.Errorf("lock_passwd = %v (present: %v), want an explicit false", locked, ok)
	}
	if doc["ssh_pwauth"] != true {
		t.Error("ssh_pwauth must be top-level and true, or stock cloud images reject the password")
	}
}

// TestGenerateUserData_PasswordReachesABuiltInUser covers users the image
// already ships as well as one cloud-init creates.
func TestGenerateUserData_PasswordReachesABuiltInUser(t *testing.T) {
	for _, name := range []string{"alpine", "ubuntu", "debian", "demo"} {
		t.Run(name, func(t *testing.T) {
			p := base()
			p.Username = name
			users, _ := parseUserData(t, p)["users"].([]any)
			if len(users) != 1 {
				t.Fatalf("users = %v, want exactly one", users)
			}
			user := users[0].(map[string]any)

			hash, _ := user["hashed_passwd"].(string)
			if bcrypt.CompareHashAndPassword([]byte(hash), []byte(p.Password)) != nil {
				t.Errorf("hashed_passwd = %q, want a hash of the password: %#v", hash, user)
			}
			if locked, ok := user["lock_passwd"]; !ok || locked != false {
				t.Errorf("lock_passwd = %v (present: %v), want an explicit false", locked, ok)
			}
		})
	}
}

// TestGenerateUserData_GroupsOnlyWhenAsked: groups reach the document, and a
// seed asking for none does not grow an empty key.
func TestGenerateUserData_GroupsOnlyWhenAsked(t *testing.T) {
	p := base()
	p.Groups = []string{"wheel"}
	user := parseUserData(t, p)["users"].([]any)[0].(map[string]any)
	if groups, _ := user["groups"].([]any); len(groups) != 1 || groups[0] != "wheel" {
		t.Errorf("groups = %#v, want [wheel]", user["groups"])
	}

	user = parseUserData(t, base())["users"].([]any)[0].(map[string]any)
	if _, present := user["groups"]; present {
		t.Errorf("groups = %#v on a seed that asked for none", user["groups"])
	}
}

// TestValidateParams_RefusesFilesCloudInitWouldMisplace: a relative path or a
// decimal mode both fail silently inside the guest.
func TestValidateParams_RefusesFilesCloudInitWouldMisplace(t *testing.T) {
	tests := []struct {
		name string
		file metadata.WriteFile
		ok   bool
	}{
		{name: "absolute path", file: metadata.WriteFile{Path: "/etc/motd", Content: "hi"}, ok: true},
		{name: "with an octal mode", file: metadata.WriteFile{Path: "/etc/motd", Permissions: "0600"}, ok: true},
		{name: "relative path", file: metadata.WriteFile{Path: "etc/motd"}},
		{name: "no path at all", file: metadata.WriteFile{Content: "hi"}},
		{name: "a decimal-looking mode", file: metadata.WriteFile{Path: "/etc/motd", Permissions: "644"}},
		{name: "not a mode", file: metadata.WriteFile{Path: "/etc/motd", Permissions: "rw-r--r--"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := base()
			p.WriteFiles = []metadata.WriteFile{tt.file}

			_, err := GenerateUserData(p)
			if (err == nil) != tt.ok {
				t.Errorf("GenerateUserData = %v, want ok %v", err, tt.ok)
			}
		})
	}
}

// TestFetchGitHubKeys checks that each failure yields a reason, not an empty
// list.
func TestFetchGitHubKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/octocat.keys":
			w.Write([]byte("ssh-rsa AAAAB3 one\n\nssh-ed25519 AAAAC3 two\n")) //nolint:errcheck // test server
		case "/keyless.keys":
			w.WriteHeader(http.StatusOK)
		case "/ghost.keys":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	old := githubKeysBase
	githubKeysBase = srv.URL
	defer func() { githubKeysBase = old }()

	t.Run("keys, with the blank line dropped", func(t *testing.T) {
		keys, err := FetchGitHubKeys(t.Context(), "octocat")
		if err != nil {
			t.Fatalf("FetchGitHubKeys: %v", err)
		}
		if len(keys) != 2 || keys[0] != "ssh-rsa AAAAB3 one" {
			t.Errorf("keys = %q, want the two real ones", keys)
		}
	})

	for _, tt := range []struct{ name, user, wantIn string }{
		// No keys is not a transport failure; there is nothing to retry.
		{name: "an account with no keys", user: "keyless", wantIn: "no public SSH keys"},
		{name: "no such account", user: "ghost", wantIn: "no account named"},
		{name: "anything else", user: "teapot", wantIn: "GitHub answered"},
		// Refused before any request is made.
		{name: "not a username", user: "not a username", wantIn: "is not a GitHub username"},
		{name: "a doubled hyphen", user: "a--b", wantIn: "is not a GitHub username"},
		{name: "a trailing hyphen", user: "trailing-", wantIn: "is not a GitHub username"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := FetchGitHubKeys(t.Context(), tt.user)
			if err == nil || !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("error = %v, want one mentioning %q", err, tt.wantIn)
			}
		})
	}
}

// TestGenerateUserData_GuestAgentGoesFirst: the agent's commands run before
// the user's, and a package the user already listed is not added twice.
func TestGenerateUserData_GuestAgentGoesFirst(t *testing.T) {
	linuxSeed(t)
	p := base()
	p.GuestAgent = GuestAgentSystemd
	p.Packages = []string{"nginx", "qemu-guest-agent"}
	p.RunCmd = []string{"curl -sfL https://get.k3s.io | sh -"}

	doc := parseUserData(t, p)
	pkgs := fmt.Sprint(doc["packages"])
	if pkgs != "[nginx qemu-guest-agent]" {
		t.Errorf("packages = %s, want the user's list with the agent once", pkgs)
	}
	cmds := fmt.Sprint(doc["runcmd"])
	want := "[systemctl enable qemu-guest-agent systemctl start qemu-guest-agent curl -sfL https://get.k3s.io | sh -]"
	if cmds != want {
		t.Errorf("runcmd = %s\nwant      %s", cmds, want)
	}
}
