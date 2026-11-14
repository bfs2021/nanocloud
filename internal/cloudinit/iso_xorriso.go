//go:build !darwin

package cloudinit

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Preflight checks that xorriso is on PATH.
func Preflight() error {
	_, err := exec.LookPath("xorriso")
	if err != nil {
		return errors.New("xorriso not found: install with 'apt install xorriso' or 'dnf install xorriso'")
	}
	return nil
}

// buildISO builds a NoCloud seed ISO at destPath from the files in srcDir.
func buildISO(srcDir, destPath string) error {
	cmd := exec.Command(
		"xorriso",
		"-as", "mkisofs",
		"-o", destPath,
		"-V", "cidata",
		"-joliet",
		"-rock",
		srcDir,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("xorriso failed: %w\noutput: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
