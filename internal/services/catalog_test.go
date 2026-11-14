package services

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nanocloud-io/nanocloud/internal/image"
)

func TestCatalog_ShipsBothWaves(t *testing.T) {
	var ids []string
	for _, r := range Catalog() {
		ids = append(ids, r.ID)
	}
	if got, want := strings.Join(ids, ","), "kubernetes,kubernetes_cluster,mysql,postgres,observability,s3"; got != want {
		t.Fatalf("catalog = %s, want %s", got, want)
	}
}

// Every image a recipe names must exist in the image catalog for both
// architectures.
func TestCatalog_ImageFamiliesResolve(t *testing.T) {
	for family, ids := range imageFamilies {
		arches := map[string]bool{}
		for _, id := range ids {
			e, ok := image.EntryByID(id)
			if !ok {
				t.Errorf("%s: image %q is not in the image catalog", family, id)
				continue
			}
			arches[e.Arch] = true
		}
		if !arches["amd64"] || !arches["arm64"] {
			t.Errorf("%s: covers %v, want amd64 and arm64", family, arches)
		}
	}
}

// The install scripts must be valid bash once rendered and wrapped.
func TestInstallScripts_AreValidBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	for _, r := range Catalog() {
		inst, err := newInstance(CreateRequest{Name: "svc", Recipe: r.ID, SSHPublicKey: testUserKey})
		if err != nil {
			t.Fatalf("%s: %v", r.ID, err)
		}
		for _, role := range r.roles() {
			script, err := installScript(r, role, inst)
			if err != nil {
				t.Fatalf("%s/%s: %v", r.ID, role.ID, err)
			}
			path := filepath.Join(t.TempDir(), r.ID+"-"+role.ID+".sh")
			if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(bash, "-n", path).CombinedOutput(); err != nil {
				t.Errorf("%s/%s: bash -n: %v\n%s\n%s", r.ID, role.ID, err, out, script)
			}
		}
	}
}

// The S3 install fetches one exact SeaweedFS release and unpacks it only once
// it matches the SHA-256 pinned for the VM's architecture. Whether a pin is
// the right hash only a real download can tell.
func TestS3Install_VerifiesThePinnedRelease(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	if _, err = exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum not installed")
	}
	r, _ := RecipeByID("s3")
	inst, err := newInstance(CreateRequest{Name: "s3", Recipe: r.ID, SSHPublicKey: testUserKey})
	if err != nil {
		t.Fatal(err)
	}
	body, err := render(r.Install, templateValues(inst, nil))
	if err != nil {
		t.Fatal(err)
	}
	download := []byte("not the release")
	downloadSum := fmt.Sprintf("%x", sha256.Sum256(download))
	for _, arch := range []string{"amd64", "arm64"} {
		pin := regexp.MustCompile(`\n\s*` + arch + `\) sum=([0-9a-f]{64}) ;;\n`).FindStringSubmatch(body)
		if pin == nil {
			t.Errorf("%s: no SHA-256 pinned", arch)
			continue
		}
		release := regexp.MustCompile(`^https://github\.com/seaweedfs/seaweedfs/releases/download/[0-9]+(\.[0-9]+)+/linux_` + arch + `\.tar\.gz$`)
		urls, out, unpacked := runS3Download(t, bash, body, arch, download)
		if !release.MatchString(urls) {
			t.Errorf("%s: downloaded %q, want one exact release", arch, urls)
		}
		if unpacked || !strings.HasPrefix(out, "failed because:") {
			t.Errorf("%s: a download that does not match the pin was not refused (unpacked: %v):\n%s", arch, unpacked, out)
		}
		matching := strings.Replace(body, pin[0], strings.Replace(pin[0], pin[1], downloadSum, 1), 1)
		if _, out, unpacked := runS3Download(t, bash, matching, arch, download); !unpacked {
			t.Errorf("%s: a download that matches the pin was not unpacked:\n%s", arch, out)
		}
	}
	if urls, out, _ := runS3Download(t, bash, body, "riscv64", download); urls != "" || !strings.HasPrefix(out, "failed because:") {
		t.Errorf("an architecture with no pin downloaded %q:\n%s", urls, out)
	}
}

// runS3Download runs an S3 install body on a VM of the given architecture,
// with curl serving download. tar stands in for unpacking and stops the
// script there, before it would set up the machine running the test.
func runS3Download(t *testing.T, bash, body, arch string, download []byte) (urls, out string, unpacked bool) {
	t.Helper()
	dir := t.TempDir()
	stubs := map[string]string{
		"dpkg": `echo "$ARCH"`,
		"curl": `while [ $# -gt 0 ]; do
  case $1 in https://*) echo "$1" >> "$STUBS/urls" ;; -o) cp "$STUBS/download" "$2" ;; esac
  shift
done`,
		"tar": `touch "$STUBS/unpacked"; exit 1`,
	}
	for name, script := range stubs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "download"), download, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, "-c", "set -Eeuo pipefail\nfail() { echo \"failed because: $*\"; exit 1; }\n"+body)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "STUBS="+dir, "ARCH="+arch, "TMPDIR="+dir)
	b, _ := cmd.CombinedOutput() // it always fails: tar stops it if nothing earlier does
	got, err := os.ReadFile(filepath.Join(dir, "urls"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	_, err = os.Stat(filepath.Join(dir, "unpacked"))
	return strings.TrimSpace(string(got)), strings.TrimSpace(string(b)), err == nil
}

func TestInstallScript_WrapsTheBodyInTheStateContract(t *testing.T) {
	r, _ := RecipeByID("postgres")
	inst, err := newInstance(CreateRequest{Name: "db", Recipe: "postgres", Params: map[string]string{"user": "shop"}, SSHPublicKey: testUserKey})
	if err != nil {
		t.Fatal(err)
	}
	script, err := installScript(r, r.roles()[0], inst)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"#!/bin/bash\n",
		"set -Eeuo pipefail",
		"echo installing > " + stateFile,
		`CREATE ROLE "shop" LOGIN PASSWORD '` + inst.Params["password"] + `'`,
		testUserKey,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if !strings.HasSuffix(script, "echo ready > "+stateFile+"\n") {
		t.Errorf("script must end by recording ready:\n%s", script)
	}
}

func TestLoadCatalog_RejectsBrokenRecipes(t *testing.T) {
	base := `id: demo
name: Demo
category: x
description: d
image: ubuntu-24.04
sizes: [{id: s, label: S, vcpus: 1, memory_mb: 512, disk_gb: 5}]
install: echo hi
`
	for name, extra := range map[string]string{
		"unknown template value":  "outputs: [{id: o, label: O, value: '{{.pasword}}'}]\n",
		"ip in install":           "",
		"default fails pattern":   "params: [{id: user, label: U, default: Bad, pattern: '^[a-z]+$'}]\n",
		"generated with default":  "params: [{id: pw, label: P, generate: 8, default: x}]\n",
		"unknown field":           "colour: blue\n",
		"two default sizes":       "",
		"endpoint off its ports":  "ports: [80]\nendpoints: [{id: web, label: Web, port: 81}]\n",
		"endpoint on a role":      "ports: [80]\nendpoints: [{id: web, label: Web, role: x, port: 80}]\n",
		"endpoint scheme":         "ports: [80]\nendpoints: [{id: web, label: Web, port: 80, scheme: ftp}]\n",
		"endpoint twice":          "ports: [80]\nendpoints: [{id: web, label: Web, port: 80}, {id: web, label: Again, port: 80}]\n",
		"endpoint in install":     "ports: [80]\nendpoints: [{id: web, label: Web, port: 80}]\n",
		"endpoint in ready_check": "ports: [80]\nendpoints: [{id: web, label: Web, port: 80}]\nready_check: curl {{.addr_web}}\n",
	} {
		t.Run(name, func(t *testing.T) {
			doc := base + extra
			switch name {
			case "ip in install":
				doc = strings.Replace(doc, "install: echo hi", "install: echo {{.ip}}", 1)
			case "endpoint in install":
				doc = strings.Replace(doc, "install: echo hi", "install: echo {{.port_web}}", 1)
			case "two default sizes":
				doc = strings.Replace(doc, "sizes: [{id: s, label: S, vcpus: 1, memory_mb: 512, disk_gb: 5}]",
					"sizes: [{id: s, label: S, vcpus: 1, memory_mb: 512, disk_gb: 5, default: true}, {id: m, label: M, vcpus: 1, memory_mb: 512, disk_gb: 5, default: true}]", 1)
			}
			fsys := fstest.MapFS{"catalog/demo.yaml": {Data: []byte(doc)}}
			if _, err := loadCatalog(fsys); err == nil {
				t.Fatalf("loaded a broken recipe:\n%s", doc)
			}
		})
	}
}

// Endpoint addresses are only for outputs and files, since they depend on
// the viewer.
func TestLoadCatalog_EndpointsInOutputsAndFiles(t *testing.T) {
	doc := `id: demo
name: Demo
category: x
description: d
image: ubuntu-24.04
sizes: [{id: s, label: S, vcpus: 1, memory_mb: 512, disk_gb: 5}]
install: echo hi
ports: [80]
endpoints: [{id: web, label: Web, port: 80, scheme: http}]
outputs: [{id: url, label: URL, value: 'http://{{.addr_web}} on {{.host_web}} port {{.port_web}}'}]
files: [{id: f, label: F, filename: f.txt, path: /f, replace: {'x': '{{.addr_web}}'}}]
`
	recipes, err := loadCatalog(fstest.MapFS{"catalog/demo.yaml": {Data: []byte(doc)}})
	if err != nil {
		t.Fatal(err)
	}
	if e := recipes[0].Endpoints; len(e) != 1 || e[0].role() != mainRole || e[0].Scheme != "http" {
		t.Errorf("endpoints = %+v", e)
	}
}

func TestRender(t *testing.T) {
	got, err := render("{{.a}} {{sh .b}}", map[string]string{"a": "x", "b": "it's"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `x 'it'\''s'`; got != want {
		t.Errorf("render = %q, want %q", got, want)
	}
	if _, err := render("{{.missing}}", map[string]string{}); err == nil {
		t.Error("a missing value rendered instead of failing")
	}
}

func TestRecipeSize(t *testing.T) {
	r, _ := RecipeByID("kubernetes")
	if s, _ := r.Size(""); s.ID != "medium" {
		t.Errorf("default size = %s, want medium", s.ID)
	}
	if s, ok := r.Size("large"); !ok || s.VCPUs != 4 {
		t.Errorf("large = %+v, %v", s, ok)
	}
	if _, ok := r.Size("huge"); ok {
		t.Error("found a size that does not exist")
	}
}
