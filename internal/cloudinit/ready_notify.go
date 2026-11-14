package cloudinit

// Virtualization.framework cannot tell a macOS host a guest's address or
// whether it has booted. macOS seeds therefore install a unit that prints a
// ready marker with the address on the guest console (/dev/hvc0), which
// internal/vzvm reads. Linux seeds never include it.

// OSCPrefix starts every marker a guest prints for the host: an OSC escape
// sequence with a private number, which terminals ignore.
const OSCPrefix = "\x1b]5379;"

// ReadyCommand is the ready marker's command. The whole marker reads
//
//	ESC ] 5379 ; nanocloud-ready ; ip=<address> ; ci=<cloud-init status> BEL
//
// ip is empty if no address appeared within two minutes; ci is what
// `cloud-init status` reported at that moment. The marker means reachable,
// not provisioned, because provisioning may itself wait on the host.
const ReadyCommand = "nanocloud-ready"

const (
	readyNotifyScriptPath = "/usr/local/sbin/nanocloud-ready-notify"
	readyNotifyUnitPath   = "/etc/systemd/system/nanocloud-ready.service"
	readyNotifyUnit       = "nanocloud-ready.service"
)

// readyNotifyScript waits up to two minutes for an IPv4 source address, then
// prints the ready marker. `ip route get` sends no traffic.
const readyNotifyScript = `#!/bin/sh
# Installed by nano-cloud. On macOS hosts nothing else can tell the host that
# this guest is up, or which address it has, so this prints both on the console.
ip=""
i=0
while [ "$i" -lt 120 ]; do
	ip=$(ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p')
	[ -n "$ip" ] && break
	i=$((i + 1))
	sleep 1
done
ci=$(cloud-init status 2>/dev/null | sed -n 's/^status: *\([a-z]*\).*/\1/p')
printf '\033]5379;nanocloud-ready;ip=%s;ci=%s\007' "$ip" "$ci" > /dev/hvc0 2>/dev/null || true
`

// readyNotifyService runs the script on every boot once the network is up.
// It must not wait for cloud-final.service: provisioning can wait on the
// host, and the host waits for this marker first. DefaultDependencies=no
// keeps the boot from waiting up to two minutes on it without a network.
const readyNotifyService = `[Unit]
Description=Tell the nano-cloud host this guest is up, and its address
DefaultDependencies=no
Wants=network-online.target
After=network-online.target
Conflicts=shutdown.target
Before=shutdown.target

[Service]
Type=oneshot
ExecStart=` + readyNotifyScriptPath + `

[Install]
WantedBy=multi-user.target
`

// readyNotifyAddon installs and enables the ready unit. On first boot
// cloud-final is already running, so it is also started, without blocking.
func readyNotifyAddon() (files []writeFileDoc, cmds []string) {
	files = []writeFileDoc{
		{Path: readyNotifyScriptPath, Content: readyNotifyScript, Permissions: "0755"},
		{Path: readyNotifyUnitPath, Content: readyNotifyService, Permissions: "0644"},
	}
	cmds = []string{
		"systemctl daemon-reload",
		"systemctl enable " + readyNotifyUnit,
		"systemctl start --no-block " + readyNotifyUnit,
	}
	return files, cmds
}

// guestNotifyAddon is everything a macOS-built seed adds: the reboot unit and
// the ready unit, with a single daemon-reload ahead of the enables.
func guestNotifyAddon() (files []writeFileDoc, cmds []string) {
	rebootFiles, rebootCmds := rebootNotifyAddon()
	readyFiles, readyCmds := readyNotifyAddon()
	files = append(rebootFiles, readyFiles...)
	cmds = append(rebootCmds, readyCmds[1:]...) // both lists start with daemon-reload
	return files, cmds
}
