//go:build darwin && cgo

package system

/*
#include <mach/mach.h>
#include <mach/mach_host.h>

static mach_port_t nc_host(void) {
	// mach_host_self() hands out a new send right on every call; keep one.
	static mach_port_t host = MACH_PORT_NULL;
	if (host == MACH_PORT_NULL) {
		host = mach_host_self();
	}
	return host;
}

// nc_cpu_ticks fills user, nice, system, idle ticks summed over every CPU.
static int nc_cpu_ticks(unsigned long long out[4]) {
	host_cpu_load_info_data_t info;
	mach_msg_type_number_t count = HOST_CPU_LOAD_INFO_COUNT;
	if (host_statistics(nc_host(), HOST_CPU_LOAD_INFO, (host_info_t)&info, &count) != KERN_SUCCESS) {
		return -1;
	}
	out[0] = info.cpu_ticks[CPU_STATE_USER];
	out[1] = info.cpu_ticks[CPU_STATE_NICE];
	out[2] = info.cpu_ticks[CPU_STATE_SYSTEM];
	out[3] = info.cpu_ticks[CPU_STATE_IDLE];
	return 0;
}

// nc_used_bytes is memory in use the way Activity Monitor counts it:
// app memory (anonymous, minus purgeable) + wired + compressed.
static int nc_used_bytes(unsigned long long *out) {
	vm_statistics64_data_t vs;
	mach_msg_type_number_t count = HOST_VM_INFO64_COUNT;
	if (host_statistics64(nc_host(), HOST_VM_INFO64, (host_info64_t)&vs, &count) != KERN_SUCCESS) {
		return -1;
	}
	vm_size_t page = 0;
	if (host_page_size(nc_host(), &page) != KERN_SUCCESS) {
		return -1;
	}
	unsigned long long pages = (unsigned long long)vs.internal_page_count
		- (unsigned long long)vs.purgeable_count
		+ (unsigned long long)vs.wire_count
		+ (unsigned long long)vs.compressor_page_count;
	*out = pages * (unsigned long long)page;
	return 0;
}
*/
import "C"

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The macOS reader takes CPU ticks and used memory from Mach host statistics
// and total memory from sysctl, with the same contract as the Linux reader.

// Reader reads host metrics.
type Reader struct{}

// New returns a Reader.
func New() *Reader { return &Reader{} }

// Hostname is the host's name, or "unknown".
func (r *Reader) Hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}

// MemoryStats reports memory in use and installed, in MiB.
func (r *Reader) MemoryStats() (usedMB, totalMB uint64, err error) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0, 0, err
	}
	var used uint64
	if C.nc_used_bytes((*C.ulonglong)(unsafe.Pointer(&used))) != 0 {
		return 0, 0, errors.New("system: host_statistics64(HOST_VM_INFO64) failed")
	}
	return used >> 20, total >> 20, nil
}

// Ticks reports cumulative CPU ticks across all CPUs. macOS has no iowait,
// irq or steal accounting, so those stay zero.
func (r *Reader) Ticks() (CPUTicks, error) {
	var t [4]uint64
	if C.nc_cpu_ticks((*C.ulonglong)(unsafe.Pointer(&t[0]))) != 0 {
		return CPUTicks{}, errors.New("system: host_statistics(HOST_CPU_LOAD_INFO) failed")
	}
	return CPUTicks{User: t[0], Nice: t[1], System: t[2], Idle: t[3]}, nil
}
