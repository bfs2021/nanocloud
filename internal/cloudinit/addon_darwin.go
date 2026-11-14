package cloudinit

// platformAddon adds the guest halves of reboot and readiness reporting (see
// reboot_notify.go and ready_notify.go).
func platformAddon() ([]writeFileDoc, []string) { return guestNotifyAddon() }
