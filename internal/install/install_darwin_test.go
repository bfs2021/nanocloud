package install

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type owned struct {
	uid  uint32
	perm fs.FileMode
}

var rootDir = owned{0, 0o755}

// fakeFS is an ownerFS over a fixed tree. A symlink is listed in tree with
// its target's owner, as Stat reports it, and in links with its target.
type fakeFS struct {
	tree  map[string]owned
	links map[string]string
}

func (f fakeFS) owner(path string) (uint32, fs.FileMode, error) {
	o, ok := f.tree[path]
	if !ok {
		return 0, 0, fs.ErrNotExist
	}
	return o.uid, o.perm, nil
}

func (f fakeFS) resolve(path string) (string, error) {
	if target, ok := f.links[path]; ok {
		return target, nil
	}
	if _, ok := f.tree[path]; !ok {
		return "", fs.ErrNotExist
	}
	return path, nil
}

// installChain is a stock Mac's chain down to binDst, with overrides applied.
func installChain(overrides map[string]owned) map[string]owned {
	tree := map[string]owned{"/": rootDir, "/usr": rootDir, "/usr/local": rootDir, "/usr/local/bin": rootDir, binDst: rootDir}
	for p, o := range overrides {
		tree[p] = o
	}
	return tree
}

func TestCheckRootOnly(t *testing.T) {
	binDir := filepath.Dir(binDst)
	cases := []struct {
		name string
		path string
		fs   fakeFS
		// unsafe is the path the refusal must name and why; empty when allowed.
		unsafe, why string
	}{
		{name: "root-owned 0755 chain", path: binDst, fs: fakeFS{tree: installChain(nil)}},
		{
			name: "bin dir not created yet",
			path: binDir,
			fs:   fakeFS{tree: map[string]owned{"/": rootDir, "/usr": rootDir, "/usr/local": rootDir}},
		},
		{
			name:   "user-owned /usr/local/bin",
			path:   binDir,
			fs:     fakeFS{tree: installChain(map[string]owned{binDir: {501, 0o755}})},
			unsafe: binDir, why: "owned by uid 501",
		},
		{
			name:   "group-writable dir",
			path:   binDir,
			fs:     fakeFS{tree: installChain(map[string]owned{binDir: {0, 0o775}})},
			unsafe: binDir, why: "writable by group or others (mode 775)",
		},
		{
			name:   "user-owned /usr/local with root-owned bin",
			path:   binDst,
			fs:     fakeFS{tree: installChain(map[string]owned{"/usr/local": {501, 0o755}})},
			unsafe: "/usr/local", why: "owned by uid 501",
		},
		{
			name:   "user-owned binary",
			path:   binDst,
			fs:     fakeFS{tree: installChain(map[string]owned{binDst: {501, 0o755}})},
			unsafe: binDst, why: "owned by uid 501",
		},
		{
			name: "bin dir linked into a user-owned tree",
			path: binDir,
			fs: fakeFS{
				tree: installChain(map[string]owned{
					"/Users": rootDir, "/Users/me": {501, 0o755}, "/Users/me/bin": rootDir,
				}),
				links: map[string]string{binDir: "/Users/me/bin"},
			},
			unsafe: "/Users/me", why: "owned by uid 501",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkRootOnly(c.path, c.fs)
			if c.unsafe == "" {
				if err != nil {
					t.Fatalf("refused a safe chain: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("allowed an install path that %s can be changed under", c.unsafe)
			}
			msg := err.Error()
			fix := "sudo chown root:wheel " + c.unsafe + " && sudo chmod 755 " + c.unsafe
			if !strings.HasPrefix(msg, c.unsafe+" is ") || !strings.Contains(msg, c.why) || !strings.Contains(msg, fix) {
				t.Errorf("message should name %s, say it is %s, and give %q; got:\n%s", c.unsafe, c.why, fix, msg)
			}
		})
	}
}

// hostFS must read owners the way the fake does: the sealed system volume's
// /usr/bin is root's, and a test's temp directory is not.
func TestHostFSReadsRealOwners(t *testing.T) {
	if err := checkRootOnly("/usr/bin", hostFS{}); err != nil {
		t.Errorf("refused /usr/bin: %v", err)
	}
	if os.Getuid() == 0 {
		t.Skip("running as root: the temp directory is root's too")
	}
	if err := checkRootOnly(t.TempDir(), hostFS{}); err == nil {
		t.Error("allowed a directory this user owns")
	}
}
