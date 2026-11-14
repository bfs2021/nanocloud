package install

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nanocloud-io/nanocloud/internal/config"
	"github.com/nanocloud-io/nanocloud/internal/metadata"
	"github.com/nanocloud-io/nanocloud/internal/storage"
)

// UninstallOptions selects what `nanocloud uninstall` removes.
type UninstallOptions struct {
	// Purge also deletes every VM nano-cloud made, its disks, and all of
	// nano-cloud's state: images, login, certificates, logs.
	Purge bool
	// Yes skips the typed confirmation Purge otherwise asks for.
	Yes bool
	// In is where the confirmation is read from.
	In io.Reader
}

// stateFiles are what the daemon keeps beside metadata.json. They are listed
// by name because metadata_path may point at a directory shared with other
// files.
var stateFiles = []string{
	"metadata.json", "metadata.json.lock",
	"templates.json", "templates.json.lock",
	"services.json", "services.json.lock",
	"service_ed25519", "service_ed25519.pub",
	"auth.json", "auth.json.lock",
	"settings.json", "settings.json.lock",
	"tls",
}

// purgePlan is what --purge deletes beyond a plain uninstall. It is read
// before anything is removed, since the config and the spec say where
// everything is.
type purgePlan struct {
	libvirtURI string
	// home is nano-cloud's own data directory, removed whole. Elsewhere only
	// the files nano-cloud wrote are removed.
	home      string
	stateDir  string
	imagesDir string
	pools     []string
	vms       []metadata.VMEntry
}

func readPurgePlan(cfgPath, home, defaultImagesDir string) (purgePlan, error) {
	p := purgePlan{home: home, stateDir: home, imagesDir: defaultImagesDir}
	cfg, err := config.Load(cfgPath)
	switch {
	case err == nil:
		p.libvirtURI = cfg.LibvirtURI
		if cfg.MetadataPath != "" {
			p.stateDir = filepath.Dir(cfg.MetadataPath)
		}
		if cfg.ImageCacheDir != "" {
			p.imagesDir = cfg.ImageCacheDir
		}
		for _, pool := range cfg.StoragePools {
			p.pools = append(p.pools, pool.Path)
		}
	case !errors.Is(err, os.ErrNotExist):
		// A config naming pools we cannot read would leave their VMs behind.
		return p, fmt.Errorf("read %s to find what to purge: %w", cfgPath, err)
	}

	// Reading takes a lock file beside the spec, so skip it after an earlier
	// purge removed everything.
	specPath := filepath.Join(p.stateDir, "metadata.json")
	if _, err = os.Stat(specPath); errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	entries, err := metadata.New(specPath).Read()
	if err != nil {
		return p, fmt.Errorf("read the VM list to find what to purge: %w", err)
	}
	for id, e := range entries {
		e.ID = id // the map key; entries do not repeat it
		p.vms = append(p.vms, e)
	}
	sort.Slice(p.vms, func(i, j int) bool { return p.vms[i].ID < p.vms[j].ID })
	return p, nil
}

// confirmPurge says what --purge is about to delete and asks for it to be
// typed back. A closed or empty input is a no.
func confirmPurge(p purgePlan, in io.Reader) bool {
	fmt.Println("--purge deletes everything nano-cloud has made on this host:")
	if len(p.vms) == 0 {
		fmt.Println("  no VMs are recorded")
	} else {
		names := make([]string, len(p.vms))
		for i, e := range p.vms {
			names[i] = e.ID
		}
		fmt.Printf("  %d VM(s) and all their disks and snapshots: %s\n", len(p.vms), strings.Join(names, ", "))
	}
	fmt.Println("  downloaded images, the login password, API tokens, certificates and logs")
	fmt.Print("This cannot be undone. Type \"purge\" to continue: ")
	if in == nil {
		in = os.Stdin
	}
	line, _ := bufio.NewReader(in).ReadString('\n')
	return strings.TrimSpace(line) == "purge"
}

// vmFiles is every file nano-cloud writes for e on either backend: disks,
// snapshot clones, the seed ISO, and the macOS EFI store and machine ID.
func vmFiles(e metadata.VMEntry) []string {
	disk, iso, ok := storage.PathsFor(e.PoolPath, e.ID)
	if !ok {
		return nil
	}
	files := []string{
		disk, storage.SnapshotDir(disk), iso,
		filepath.Join(e.PoolPath, e.ID+".nvram"),
		filepath.Join(e.PoolPath, e.ID+".machineid"),
	}
	for _, d := range e.Disks {
		files = append(files, d.Path, storage.SnapshotDir(d.Path))
	}
	return files
}

// purgeFiles removes every VM's files, the state files and nano-cloud's data
// directory, then any configured directory left empty. Directories still
// holding other files are kept and returned.
func purgeFiles(p purgePlan) (kept []string, err error) {
	var paths []string
	for _, e := range p.vms {
		paths = append(paths, vmFiles(e)...)
	}
	for _, f := range stateFiles {
		paths = append(paths, filepath.Join(p.stateDir, f))
	}
	for _, path := range append(paths, p.home) {
		if err = os.RemoveAll(path); err != nil {
			return nil, fmt.Errorf("remove %s: %w", path, err)
		}
	}
	for _, dir := range append(p.pools, p.stateDir, p.imagesDir) {
		if err = os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			kept = append(kept, dir)
		}
	}
	return kept, nil
}

// printKept says what a plain uninstall left on disk and how to remove it.
func printKept(paths ...string) {
	var present []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			present = append(present, p)
		}
	}
	if len(present) == 0 {
		return
	}
	fmt.Println("Kept, so your VMs survive a reinstall:")
	for _, p := range present {
		fmt.Printf("  %s\n", p)
	}
	fmt.Println("To delete the VMs and everything above too:")
	fmt.Println("  curl -fsSL https://raw.githubusercontent.com/nanocloud-io/nanocloud/main/install.sh | sudo sh -s -- uninstall --purge")
}

func printPurged(p purgePlan, kept []string) {
	fmt.Printf("nanocloud purged: %d VM(s) deleted, along with its images and state.\n", len(p.vms))
	for _, dir := range kept {
		fmt.Printf("Left %s in place: it is not empty, and is not nano-cloud's own directory.\n", dir)
	}
}
