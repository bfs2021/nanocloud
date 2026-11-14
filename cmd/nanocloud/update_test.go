package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSwapRollsBackUnlessTheNewReleaseStarts(t *testing.T) {
	boom := errors.New("boom")
	for name, tc := range map[string]struct {
		installErr map[string]error // by binary
		confirmErr error
		installs   []string
		wantErr    string
	}{
		"new release comes up":   {installs: []string{"new"}},
		"new install fails":      {installErr: map[string]error{"new": boom}, installs: []string{"new", "prev"}, wantErr: "rolled back"},
		"daemon reports old tag": {confirmErr: boom, installs: []string{"new", "prev"}, wantErr: "rolled back"},
		"rollback fails too": {
			installErr: map[string]error{"new": boom, "prev": boom},
			installs:   []string{"new", "prev"},
			wantErr:    "the rollback failed too",
		},
	} {
		var installs []string
		install := func(bin string) error {
			installs = append(installs, bin)
			return tc.installErr[bin]
		}
		confirmed := false
		confirm := func() error { confirmed = true; return tc.confirmErr }

		err := swap("new", "prev", install, confirm)

		if !slices.Equal(installs, tc.installs) {
			t.Errorf("%s: installed %v, want %v", name, installs, tc.installs)
		}
		if tc.installErr["new"] != nil && confirmed {
			t.Errorf("%s: confirmed a release that failed to install", name)
		}
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%s: err = %v, want it to say %q", name, err, tc.wantErr)
		}
	}
}

func TestCopyExecutableKeepsItRunnable(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "nanocloud"), filepath.Join(dir, "nanocloud.prev")
	if err := os.WriteFile(src, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("an older, longer binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := copyExecutable(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "binary" {
		t.Errorf("copy holds %q (%v), want %q", got, err, "binary")
	}
	if fi, err := os.Stat(dst); err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("copy is not executable: %v %v", fi.Mode(), err)
	}
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Error("the temporary file was left behind")
	}
}
