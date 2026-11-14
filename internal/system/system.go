//go:build linux

// Package system provides host-level metrics read from /proc on Linux.
package system

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Reader collects host-level system metrics.
type Reader struct{}

// New returns a Reader.
func New() *Reader { return &Reader{} }

// Hostname returns the machine hostname, falling back to "unknown" on error.
func (r *Reader) Hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

// MemoryStats reads /proc/meminfo and returns used and total memory in MB,
// where used is total minus MemAvailable.
func (r *Reader) MemoryStats() (usedMB, totalMB uint64, err error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, fmt.Errorf("open /proc/meminfo: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only proc file; close error is not actionable

	var totalKB, availableKB uint64
	found := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() && found < 2 {
		line := sc.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			totalKB = v
			found++
		case "MemAvailable:":
			availableKB = v
			found++
		}
	}
	if err := sc.Err(); err != nil {
		return 0, 0, fmt.Errorf("scan /proc/meminfo: %w", err)
	}
	if found < 2 {
		return 0, 0, fmt.Errorf("missing MemTotal or MemAvailable in /proc/meminfo")
	}

	totalMB = totalKB / 1024
	var usedKB uint64
	if totalKB > availableKB {
		usedKB = totalKB - availableKB
	}
	usedMB = usedKB / 1024
	return usedMB, totalMB, nil
}

// Ticks reads /proc/stat once and returns the aggregate CPU tick counters.
func (r *Reader) Ticks() (CPUTicks, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return CPUTicks{}, fmt.Errorf("open /proc/stat: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only proc file; close error is not actionable

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		// fields[0] = "cpu", fields[1..] = tick counters.
		if len(fields) < 5 {
			return CPUTicks{}, fmt.Errorf("unexpected cpu line in /proc/stat: %q", line)
		}
		parse := func(i int) uint64 {
			if i >= len(fields) {
				return 0
			}
			v, _ := strconv.ParseUint(fields[i], 10, 64)
			return v
		}
		return CPUTicks{
			User:    parse(1),
			Nice:    parse(2),
			System:  parse(3),
			Idle:    parse(4),
			IOWait:  parse(5),
			IRQ:     parse(6),
			SoftIRQ: parse(7),
			Steal:   parse(8),
		}, nil
	}
	if err := sc.Err(); err != nil {
		return CPUTicks{}, fmt.Errorf("scan /proc/stat: %w", err)
	}
	return CPUTicks{}, fmt.Errorf("cpu aggregate line not found in /proc/stat")
}
