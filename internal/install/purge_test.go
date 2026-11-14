package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/storage"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// A pool or metadata_path can be a directory the operator also uses, so
// purge must delete what nano-cloud wrote there and nothing else.
func TestPurgeFilesRemovesOnlyWhatNanoCloudWrote(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	shared := filepath.Join(root, "shared")   // a pool the operator also uses
	state := filepath.Join(root, "state")     // a custom metadata_path directory
	emptyPool := filepath.Join(root, "empty") // left empty by the purge
	images := filepath.Join(root, "isos")     // a custom image_cache_dir

	extra := filepath.Join(shared, "web-vdb"+storage.DiskExt)
	owned := []string{
		filepath.Join(home, "vm-disks", "db"+storage.DiskExt),
		storage.DiskPathFor(shared, "web"),
		filepath.Join(storage.SnapshotDir(storage.DiskPathFor(shared, "web")), "s1"+storage.DiskExt),
		storage.ISOPathFor(shared, "web"),
		filepath.Join(shared, "web.nvram"),
		extra,
		storage.DiskPathFor(emptyPool, "api"),
		filepath.Join(state, "metadata.json"),
		filepath.Join(state, "auth.json"),
		filepath.Join(state, "tls", "cert.pem"),
	}
	foreign := []string{
		filepath.Join(shared, "backup.tar"),
		storage.DiskPathFor(shared, "webserver"), // shares a prefix, not a VM
		filepath.Join(state, "notes.txt"),
		filepath.Join(images, "my.iso"),
	}
	for _, p := range append(owned, foreign...) {
		touch(t, p)
	}

	kept, err := purgeFiles(purgePlan{
		home: home, stateDir: state, imagesDir: images,
		pools: []string{shared, emptyPool},
		vms: []metadata.VMEntry{
			{ID: "web", PoolPath: shared, Disks: []metadata.Disk{{Target: "vdb", Path: extra}}},
			{ID: "db", PoolPath: filepath.Join(home, "vm-disks")},
			{ID: "api", PoolPath: emptyPool},
			{ID: "lost"}, // no pool recorded: nothing to compose a path from
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range append(owned, home, emptyPool) {
		if exists(p) {
			t.Errorf("%s survived the purge", p)
		}
	}
	for _, p := range foreign {
		if !exists(p) {
			t.Errorf("purge deleted %s, which nano-cloud did not write", p)
		}
	}
	if got := strings.Join(kept, ","); got != strings.Join([]string{shared, state, images}, ",") {
		t.Errorf("kept = %v, want the three directories still holding foreign files", kept)
	}
}

func TestConfirmPurgeNeedsTheWordTyped(t *testing.T) {
	plan := purgePlan{vms: []metadata.VMEntry{{ID: "web"}}}
	for in, want := range map[string]bool{
		"purge\n": true,
		"purge":   true,
		"y\n":     false,
		"yes\n":   false,
		"":        false, // stdin closed, as under `curl | sh` with no terminal
	} {
		if got := confirmPurge(plan, strings.NewReader(in)); got != want {
			t.Errorf("confirmPurge(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestReadPurgePlanFollowsTheConfig(t *testing.T) {
	dir := t.TempDir()
	state, pool := filepath.Join(dir, "state"), filepath.Join(dir, "pool")
	cfg := filepath.Join(dir, "config.yaml")
	body := "storage_pools:\n  - name: default\n    path: " + pool + "\n    default: true\n" +
		"metadata_path: " + filepath.Join(state, "metadata.json") + "\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := metadata.New(filepath.Join(state, "metadata.json")).Upsert("web", func(e *metadata.VMEntry) { e.PoolPath = pool }); err != nil {
		t.Fatal(err)
	}

	p, err := readPurgePlan(cfg, filepath.Join(dir, "home"), "/default/images")
	if err != nil {
		t.Fatal(err)
	}
	if p.stateDir != state || len(p.pools) != 1 || p.pools[0] != pool || p.imagesDir != "/default/images" {
		t.Errorf("plan = %+v", p)
	}
	if len(p.vms) != 1 || p.vms[0].ID != "web" {
		t.Errorf("vms = %+v, want web", p.vms)
	}

	// No config means the defaults, and no metadata.json — a host already
	// purged once — means no VMs rather than an error.
	p, err = readPurgePlan(filepath.Join(dir, "absent.yaml"), filepath.Join(dir, "gone"), "")
	if err != nil || len(p.vms) != 0 || p.stateDir != filepath.Join(dir, "gone") {
		t.Errorf("readPurgePlan with nothing installed = %+v, %v", p, err)
	}
	if exists(filepath.Join(dir, "gone")) {
		t.Error("reading the plan created the state directory it was asked about")
	}
	if err := os.WriteFile(cfg, []byte("storage_pools: [nonsense"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPurgePlan(cfg, dir, ""); err == nil {
		t.Error("an unreadable config must stop the purge: its pools could hold VMs")
	}
}
