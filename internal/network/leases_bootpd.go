package network

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultBootpdLeasesPath is where macOS's DHCP server, bootpd, records leases,
// including those on Virtualization.framework's NAT network.
const DefaultBootpdLeasesPath = "/var/db/dhcpd_leases"

// BootpdLeases is a LeaseSource backed by a bootpd lease file.
type BootpdLeases string

// Read implements LeaseSource.
func (p BootpdLeases) Read() ([]DHCPLease, error) {
	return ReadBootpdLeases(string(p))
}

// ReadBootpdLeases parses bootpd's lease file, dropping expired leases. A
// missing file is not an error. The format is brace-delimited blocks of
// key=value lines:
//
//	{
//		name=web
//		ip_address=192.168.64.3
//		hw_address=1,52:54:0:ab:cd:ef
//		identifier=1,52:54:0:ab:cd:ef
//		lease=0x6720f1a2
//	}
//
// hw_address has an ARP hardware type prefix ("1," is Ethernet) and unpadded
// octets, so MACAddress is normalised. lease is the expiry in hex Unix seconds.
func ReadBootpdLeases(path string) ([]DHCPLease, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []DHCPLease{}, nil
		}
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only file; close error is not actionable
	return parseBootpdLeases(f, time.Now())
}

func parseBootpdLeases(r io.Reader, now time.Time) ([]DHCPLease, error) {
	leases := []DHCPLease{}
	var cur *DHCPLease
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "{":
			cur = &DHCPLease{}
		case line == "}":
			if cur != nil && cur.IPAddress != "" && cur.MACAddress != "" &&
				(cur.ExpiryTime == 0 || cur.ExpiryTime > now.Unix()) {
				leases = append(leases, *cur)
			}
			cur = nil
		case cur != nil:
			key, val, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch key {
			case "name":
				cur.Hostname = val
			case "ip_address":
				cur.IPAddress = val
			case "hw_address":
				// "1,52:54:0:ab:cd:ef": drop the hardware-type prefix.
				if _, mac, found := strings.Cut(val, ","); found {
					val = mac
				}
				cur.MACAddress = NormalizeMAC(val)
			case "lease":
				if t, err := strconv.ParseInt(strings.TrimPrefix(val, "0x"), 16, 64); err == nil {
					cur.ExpiryTime = t
				}
			}
		}
	}
	return leases, sc.Err()
}

// NormalizeMAC renders a MAC address as six lowercase, zero-padded octets, so
// "52:54:0:AB:c:ef" equals "52:54:00:ab:0c:ef". Other input is only
// lowercased.
func NormalizeMAC(mac string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(mac)), ":")
	if len(parts) != 6 {
		return strings.ToLower(strings.TrimSpace(mac))
	}
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 16, 8)
		if err != nil {
			return strings.ToLower(strings.TrimSpace(mac))
		}
		parts[i] = strconv.FormatUint(n|0x100, 16)[1:]
	}
	return strings.Join(parts, ":")
}
