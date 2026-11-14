package cloudinit

// GuestAgent names the init system that starts qemu-guest-agent. The zero
// value installs nothing, as on macOS, which has no agent channel.
type GuestAgent string

// The two init systems the catalog's distros use.
const (
	GuestAgentSystemd GuestAgent = "systemd"
	GuestAgentOpenRC  GuestAgent = "openrc"
)

// guestAgentPackage has the same name in every catalog distro.
const guestAgentPackage = "qemu-guest-agent"

// guestAgentAddon returns what a seed adds so the guest answers on the agent
// channel, which is how the host learns a bridged guest's IP. The package
// goes through cloud-init's packages module, which waits out the dpkg lock
// Ubuntu holds on first boot. enable and start are separate commands because
// Debian's unit has no [Install] section and a failed enable must not skip
// the start.
func guestAgentAddon(g GuestAgent) (packages, cmds []string) {
	switch g {
	case GuestAgentSystemd:
		return []string{guestAgentPackage}, []string{
			"systemctl enable " + guestAgentPackage,
			"systemctl start " + guestAgentPackage,
		}
	case GuestAgentOpenRC:
		return []string{guestAgentPackage}, []string{
			"rc-update add " + guestAgentPackage + " default",
			"rc-service " + guestAgentPackage + " start",
		}
	}
	return nil, nil
}

// withGuestAgent puts the agent ahead of the caller's packages and commands,
// so a long runcmd does not delay the address. A package already named is not
// listed twice.
func withGuestAgent(g GuestAgent, packages, cmds []string) ([]string, []string) {
	agentPkgs, agentCmds := guestAgentAddon(g)
	if len(agentPkgs) == 0 {
		return packages, cmds
	}
	for _, p := range packages {
		if p == guestAgentPackage {
			agentPkgs = nil
		}
	}
	return append(agentPkgs, packages...), append(agentCmds, cmds...)
}
