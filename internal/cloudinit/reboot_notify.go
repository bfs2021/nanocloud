package cloudinit

// Virtualization.framework stops a VM whose guest resets, so on a macOS host a
// reboot looks like a poweroff. macOS seeds install a unit that prints
// RebootMarker on the console as the guest reboots, and internal/vzvm boots
// the VM back up. Linux seeds never include it.

// RebootCommand is the reboot marker's command and RebootMarker the whole
// marker.
const (
	RebootCommand = "nanocloud-reboot"
	RebootMarker  = OSCPrefix + RebootCommand + "\x07"
)

const (
	rebootNotifyScriptPath = "/usr/local/sbin/nanocloud-reboot-notify"
	rebootNotifyUnitPath   = "/etc/systemd/system/nanocloud-reboot-notify.service"
	rebootNotifyUnit       = "nanocloud-reboot-notify.service"
)

// rebootNotifyScript prints RebootMarker.
const rebootNotifyScript = `#!/bin/sh
# Installed by nano-cloud. On macOS hosts a guest reboot stops the VM, so this
# tells the host "this shutdown is a reboot" and it starts the VM again.
printf '\033]5379;nanocloud-reboot\007' > /dev/hvc0 2>/dev/null || true
`

// rebootNotifyService runs the script only when reboot.target is reached, so
// a poweroff stays a poweroff. DefaultDependencies=no keeps the shutdown
// ordering from stopping it first.
const rebootNotifyService = `[Unit]
Description=Tell the nano-cloud host that this shutdown is a reboot
DefaultDependencies=no
Before=shutdown.target

[Service]
Type=oneshot
ExecStart=` + rebootNotifyScriptPath + `

[Install]
WantedBy=reboot.target
`

// addon is platformAddon; tests swap it to render macOS user-data anywhere.
var addon = platformAddon

// rebootNotifyAddon installs and enables the reboot unit.
func rebootNotifyAddon() (files []writeFileDoc, cmds []string) {
	files = []writeFileDoc{
		{Path: rebootNotifyScriptPath, Content: rebootNotifyScript, Permissions: "0755"},
		{Path: rebootNotifyUnitPath, Content: rebootNotifyService, Permissions: "0644"},
	}
	cmds = []string{
		"systemctl daemon-reload",
		"systemctl enable " + rebootNotifyUnit,
	}
	return files, cmds
}
