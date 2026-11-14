import type { HealthCheck } from '../../types';

// A firmware option the host may not be able to provide. `reason` is filled in
// exactly when the option is unavailable, and carries the whole explanation —
// a disabled control with no reason is indistinguishable from a broken one.
export interface Availability {
  available: boolean;
  reason?: string;
  remedy?: string;
}

// firmwareAvailability reads the same host checks Settings shows. A check the
// daemon doesn't report counts as unavailable: offering firmware the host
// can't boot fails only at define time, after the disk and seed ISO exist.
export function firmwareAvailability(checks: HealthCheck[], name: string, missing: string): Availability {
  const check = checks.find(c => c.name === name);
  if (!check) return { available: false, reason: missing };
  if (check.ok) return { available: true };
  return { available: false, reason: check.message, remedy: check.remedy };
}

// firmwareOptions is what the host lets this VM turn on, given its checks, the
// firmware chosen so far and the image's distro.
export function firmwareOptions(checks: HealthCheck[], firmware: 'bios' | 'uefi', distro: string | undefined) {
  const uefi = firmwareAvailability(
    checks,
    'ovmf',
    'This host has not reported whether UEFI firmware is installed, so it cannot be offered.',
  );
  const swtpm = firmwareAvailability(checks, 'swtpm', 'This host has not reported whether swtpm is installed, so a TPM cannot be offered.');
  return { uefi, swtpm, secureBoot: secureBootAvailability(uefi, firmware, distro) };
}

// secureBootAvailability: Alpine is the one catalog image with no signed
// bootloader. Its secure-boot domain would halt at the firmware with nothing
// in the daemon's logs, so it is refused here, with the reason.
export function secureBootAvailability(uefi: Availability, firmware: 'bios' | 'uefi', distro: string | undefined): Availability {
  if (!uefi.available) return uefi;
  if (firmware !== 'uefi') return { available: false, reason: 'Secure boot is a UEFI feature — turn on UEFI first.' };
  if (distro === 'alpine') {
    return {
      available: false,
      reason: 'Alpine does not ship a signed bootloader, so it cannot boot with secure boot enabled. UEFI on its own works.',
    };
  }
  return { available: true };
}
