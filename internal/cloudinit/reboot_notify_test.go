package cloudinit

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
)

// The guest script must print exactly the bytes the host looks for.
func TestRebootNotifyScriptPrintsTheMarker(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	out := filepath.Join(t.TempDir(), "hvc0")
	script := strings.ReplaceAll(rebootNotifyScript, "/dev/hvc0", out)
	if err = exec.Command(sh, "-c", script).Run(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != RebootMarker {
		t.Fatalf("script printed %q, want %q", got, RebootMarker)
	}
}

func TestRebootNotifyUnitRunsOnlyOnReboot(t *testing.T) {
	for _, want := range []string{"WantedBy=reboot.target", "DefaultDependencies=no", "ExecStart=" + rebootNotifyScriptPath} {
		if !strings.Contains(rebootNotifyService, want) {
			t.Errorf("unit lacks %q", want)
		}
	}
	if strings.Contains(rebootNotifyService, "poweroff") || strings.Contains(rebootNotifyService, "halt") {
		t.Error("unit would also run on poweroff")
	}
	files, cmds := rebootNotifyAddon()
	if len(files) != 2 || files[0].Permissions != "0755" {
		t.Errorf("files = %+v", files)
	}
	if strings.Join(cmds, ";") != "systemctl daemon-reload;systemctl enable "+rebootNotifyUnit {
		t.Errorf("cmds = %v", cmds)
	}
}

// Only macOS seeds carry the reboot helper.
func TestUserDataCarriesTheRebootHelperOnlyOnMacOS(t *testing.T) {
	p := Params{Hostname: "web", Username: "ada", Password: "pw", RunCmd: []string{"echo user"}}
	out, err := GenerateUserData(p)
	if err != nil {
		t.Fatal(err)
	}
	has := strings.Contains(out, rebootNotifyUnit)
	if runtime.GOOS == "darwin" {
		if !has {
			t.Fatal("macOS user-data lacks the reboot helper")
		}
		if strings.Index(out, "systemctl enable "+rebootNotifyUnit) > strings.Index(out, "echo user") {
			t.Error("the user's commands run before the reboot helper is enabled")
		}
		return
	}
	if has || strings.Contains(out, "nanocloud-reboot") {
		t.Fatalf("Linux user-data mentions the macOS reboot helper:\n%s", out)
	}
}

// TestMacOSUserDataShape renders macOS user-data on any platform and checks
// the files and commands arrive in order.
func TestMacOSUserDataShape(t *testing.T) {
	saved := addon
	addon = rebootNotifyAddon
	defer func() { addon = saved }()

	p := Params{Hostname: "web", Username: "ada", Password: "pw", RunCmd: []string{"echo user"},
		WriteFiles: []metadata.WriteFile{{Path: "/etc/motd", Content: "hi"}}}
	out, err := GenerateUserData(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "#cloud-config\n") {
		t.Fatal("lost the #cloud-config header")
	}
	var doc struct {
		WriteFiles []struct {
			Path, Content, Permissions string
		} `yaml:"write_files"`
		RunCmd []string `yaml:"runcmd"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.WriteFiles) != 3 || doc.WriteFiles[0].Path != "/etc/motd" ||
		doc.WriteFiles[1].Path != rebootNotifyScriptPath || doc.WriteFiles[1].Permissions != "0755" ||
		doc.WriteFiles[2].Path != rebootNotifyUnitPath || doc.WriteFiles[1].Content != rebootNotifyScript {
		t.Fatalf("write_files = %+v", doc.WriteFiles)
	}
	want := []string{"systemctl daemon-reload", "systemctl enable " + rebootNotifyUnit, "echo user"}
	if strings.Join(doc.RunCmd, "|") != strings.Join(want, "|") {
		t.Fatalf("runcmd = %q, want %q", doc.RunCmd, want)
	}
	t.Logf("macOS user-data:\n%s", out)
}
