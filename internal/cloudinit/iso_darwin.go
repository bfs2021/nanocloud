package cloudinit

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// hdiutil ships with macOS. Joliet keeps the lowercase file names NoCloud
// looks for, and the cidata volume label is how the guest finds the seed.

// Preflight checks that hdiutil is available on PATH.
func Preflight() error {
	if _, err := exec.LookPath("hdiutil"); err != nil {
		return errors.New("hdiutil not found: it ships with macOS in /usr/bin — check PATH")
	}
	return nil
}

// buildISO builds a NoCloud seed ISO at destPath from the files in srcDir.
func buildISO(srcDir, destPath string) error {
	cmd := exec.Command(
		"hdiutil", "makehybrid",
		"-iso", "-joliet",
		"-default-volume-name", "cidata",
		"-ov",
		"-o", destPath,
		srcDir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("hdiutil makehybrid failed: %w\noutput: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
