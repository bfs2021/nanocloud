//go:build !darwin

package cloudinit

// platformAddon adds nothing outside macOS; libvirt handles reboots itself.
func platformAddon() ([]writeFileDoc, []string) { return nil, nil }
