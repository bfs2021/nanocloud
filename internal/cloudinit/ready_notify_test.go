package cloudinit

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runReadyScript runs the guest's ready script with fake `ip` and
// `cloud-init` commands and returns what it printed on the console.
func runReadyScript(t *testing.T, ipOut, ciOut string) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err = os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := func(name, out string) {
		script := "#!/bin/sh\ncat <<'EOF'\n" + out + "\nEOF\n"
		if werr := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); werr != nil {
			t.Fatal(werr)
		}
	}
	fake("ip", ipOut)
	fake("cloud-init", ciOut)

	console := filepath.Join(dir, "hvc0")
	script := strings.ReplaceAll(readyNotifyScript, "/dev/hvc0", console)
	cmd := exec.Command(sh, "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, rerr := cmd.CombinedOutput(); rerr != nil {
		t.Fatalf("script: %v\n%s", rerr, out)
	}
	got, err := os.ReadFile(console)
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

// The script prints exactly the marker the host parses.
func TestReadyNotifyScriptPrintsAddressAndStatus(t *testing.T) {
	route := "1.1.1.1 via 192.168.64.1 dev enp0s1 src 192.168.64.7 uid 0\n    cache"
	for ci, want := range map[string]string{
		"status: running": "running",
		"status: done":    "done",
		"status: error":   "error",
	} {
		got := runReadyScript(t, route, ci)
		if exp := OSCPrefix + ReadyCommand + ";ip=192.168.64.7;ci=" + want + "\x07"; got != exp {
			t.Errorf("cloud-init %q: printed %q, want %q", ci, got, exp)
		}
	}
}

func TestReadyNotifyUnitWaitsForTheNetworkWithoutHoldingUpBoot(t *testing.T) {
	if strings.Contains(readyNotifyService, "cloud-final") {
		t.Error("the ready unit waits for cloud-final, so a guest whose provisioning waits on the host never reports its address")
	}
	for _, want := range []string{
		"DefaultDependencies=no", // see readyNotifyService: the boot never waits for it
		"After=network-online.target\n",
		"Type=oneshot",
		"ExecStart=" + readyNotifyScriptPath,
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(readyNotifyService, want) {
			t.Errorf("unit lacks %q", want)
		}
	}
}

// orderingCycle reports whether systemd-analyze finds an ordering cycle with
// the given ready unit beside a cloud-final.service ordered after
// multi-user.target, as on Ubuntu and Debian. ok is false if it cannot tell.
func orderingCycle(t *testing.T, unit string) (cycle, ok bool) {
	t.Helper()
	analyze, err := exec.LookPath("systemd-analyze")
	if err != nil {
		return false, false
	}
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) {
		if werr := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); werr != nil {
			t.Fatal(werr)
		}
	}
	script := filepath.Join(dir, "nanocloud-ready-notify")
	write("nanocloud-ready-notify", readyNotifyScript, 0o755)
	write(readyNotifyUnit, strings.ReplaceAll(unit, readyNotifyScriptPath, script), 0o644)
	write("cloud-final.service", "[Unit]\nAfter=network-online.target multi-user.target\n"+
		"[Service]\nType=oneshot\nExecStart=/bin/true\n[Install]\nWantedBy=multi-user.target\n", 0o644)
	wants := filepath.Join(dir, "multi-user.target.wants")
	if err = os.Mkdir(wants, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{readyNotifyUnit, "cloud-final.service"} {
		if err = os.Symlink(filepath.Join("..", u), filepath.Join(wants, u)); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(analyze, "verify", "--man=no", "multi-user.target")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "SYSTEMD_UNIT_PATH="+dir+":")
	out, _ := cmd.CombinedOutput() // verify's exit status also reflects unrelated host units
	return strings.Contains(string(out), "ordering cycle"), true
}

// A ready unit wanted by multi-user.target and ordered after cloud-final
// would form a cycle that systemd breaks by dropping it at boot. The control
// case proves this host can detect that cycle.
func TestReadyNotifyUnitHasNoOrderingCycle(t *testing.T) {
	control := strings.Replace(readyNotifyService, "DefaultDependencies=no\n", "", 1)
	control = strings.Replace(control, "After=network-online.target\n", "After=network-online.target cloud-final.service\n", 1)
	cycle, ok := orderingCycle(t, control)
	if !ok || !cycle {
		t.Skip("this host's systemd-analyze cannot detect the ordering cycle")
	}
	if cycle, _ = orderingCycle(t, readyNotifyService); cycle {
		t.Fatal("the ready unit forms an ordering cycle with cloud-final.service")
	}
}

// macOS seeds carry both units, reload systemd once, and start the ready unit
// on first boot.
func TestGuestNotifyAddonShape(t *testing.T) {
	files, cmds := guestNotifyAddon()
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	wantPaths := []string{rebootNotifyScriptPath, rebootNotifyUnitPath, readyNotifyScriptPath, readyNotifyUnitPath}
	if strings.Join(paths, " ") != strings.Join(wantPaths, " ") {
		t.Errorf("files = %v, want %v", paths, wantPaths)
	}
	wantCmds := []string{
		"systemctl daemon-reload",
		"systemctl enable " + rebootNotifyUnit,
		"systemctl enable " + readyNotifyUnit,
		"systemctl start --no-block " + readyNotifyUnit,
	}
	if strings.Join(cmds, "|") != strings.Join(wantCmds, "|") {
		t.Errorf("cmds = %q, want %q", cmds, wantCmds)
	}
}

func TestUserDataCarriesTheReadyUnitOnlyOnMacOS(t *testing.T) {
	out, err := GenerateUserData(Params{Hostname: "web", Username: "ada", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	has := strings.Contains(out, readyNotifyUnit)
	if runtime.GOOS == "darwin" && !has {
		t.Fatal("macOS user-data lacks the ready unit")
	}
	if runtime.GOOS != "darwin" && (has || strings.Contains(out, ReadyCommand)) {
		t.Fatalf("Linux user-data mentions the macOS ready unit:\n%s", out)
	}
}
