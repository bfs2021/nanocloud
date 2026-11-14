package network

// LeaseSource is where a Manager reads DHCP leases from: libvirt's dnsmasq
// status file on Linux, bootpd's lease file on macOS.
type LeaseSource interface {
	Read() ([]DHCPLease, error)
}

// NewWithLeases creates a Manager that reads DHCP leases from src.
func NewWithLeases(src LeaseSource) *Manager {
	return &Manager{leases: src}
}
