import { createContext, useContext } from 'react';

// Capabilities says what the daemon's hypervisor can do, as GET /system
// reports it, so the UI hides what would only fail. Linux (libvirt/QEMU) has
// everything but a serial console; macOS (Apple Virtualization) has a serial
// console, snapshots and NAT networking, and boots every VM with UEFI.
export interface Capabilities {
  backend: string; // "libvirt" | "vz"
  snapshots: boolean;
  vnc_console: boolean;
  serial_console: boolean;
  passthrough: boolean;
  uefi_toggle: boolean;
  tpm: boolean;
  bridge: boolean;
  vlan: boolean;
  cpu_tuning: boolean;
  host_stats: boolean;
  vm_stats: boolean;
}

// LIBVIRT_CAPABILITIES is assumed until /system answers, and for a daemon too
// old to report capabilities at all — which was always a libvirt one.
export const LIBVIRT_CAPABILITIES: Capabilities = {
  backend: 'libvirt',
  snapshots: true,
  vnc_console: true,
  serial_console: false,
  passthrough: true,
  uefi_toggle: true,
  tpm: true,
  bridge: true,
  vlan: true,
  cpu_tuning: true,
  host_stats: true,
  vm_stats: true,
};

export const VZ_CAPABILITIES: Capabilities = {
  backend: 'vz',
  snapshots: true,
  vnc_console: false,
  serial_console: true,
  passthrough: false,
  uefi_toggle: false,
  tpm: false,
  bridge: false,
  vlan: false,
  cpu_tuning: false,
  host_stats: true,
  vm_stats: false,
};

export const CapabilitiesContext = createContext<Capabilities>(LIBVIRT_CAPABILITIES);

export function useCapabilities(): Capabilities {
  return useContext(CapabilitiesContext);
}

export function sameCapabilities(a: Capabilities, b: Capabilities): boolean {
  return (Object.keys(a) as (keyof Capabilities)[]).every(k => a[k] === b[k]);
}

// backendLabel names the hypervisor for people.
export function backendLabel(c: Capabilities): string {
  return c.backend === 'vz' ? 'Apple Virtualization (macOS)' : 'libvirt / QEMU-KVM (Linux)';
}
