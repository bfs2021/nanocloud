// Design system constants and helpers

import type { VM } from './types';

// OS_META is each distribution's name and brand colour, the ground its logo
// (osLogos.ts) sits on.
export const OS_META: Record<string, { color: string; label: string }> = {
  ubuntu: { color: '#E95420', label: 'Ubuntu' },
  debian: { color: '#A81D33', label: 'Debian' },
  fedora: { color: '#51A2DA', label: 'Fedora' },
  alpine: { color: '#0D597F', label: 'Alpine' },
  rocky: { color: '#10B981', label: 'Rocky Linux' },
  alma: { color: '#000000', label: 'AlmaLinux' },
  opensuse: { color: '#73BA25', label: 'openSUSE' },
};

// Status colours are theme tokens (index.css). Pulsing means progress and
// nothing else; a running VM glows steadily instead.
export const STATUS_META = {
  running: { label: 'Running', color: 'var(--nd-ready)', bg: 'var(--nd-ready-bg)', dot: 'var(--nd-ready)', pulse: false, glow: true },
  booting: { label: 'Booting', color: 'var(--nd-warn)', bg: 'var(--nd-warn-bg)', dot: 'var(--nd-warn)', pulse: true, glow: false },
  stopping: { label: 'Stopping', color: 'var(--nd-warn)', bg: 'var(--nd-warn-bg)', dot: 'var(--nd-warn)', pulse: true, glow: false },
  stopped: { label: 'Stopped', color: 'var(--nd-muted)', bg: 'var(--nd-surface-2)', dot: 'var(--nd-muted)', pulse: false, glow: false },
  error: { label: 'Error', color: 'var(--nd-danger)', bg: 'var(--nd-danger-bg)', dot: 'var(--nd-danger)', pulse: true, glow: false },
  // Running, but never got an address. A warning rather than a failure, since
  // the VM is up and its console works, and no pulse: nothing is in progress.
  'no-network': {
    label: 'No network',
    color: 'var(--nd-alert)',
    bg: 'var(--nd-alert-bg)',
    dot: 'var(--nd-alert)',
    pulse: false,
    glow: false,
  },
  // Reconcile-loop states. getStatus() falls back to "Stopped" for a missing
  // key, which would show a crash-looping VM as healthy.
  'crash-looping': {
    label: 'Crash looping',
    color: 'var(--nd-danger)',
    bg: 'var(--nd-danger-bg)',
    dot: 'var(--nd-danger)',
    pulse: true,
    glow: false,
  },
  'gave-up': { label: 'Gave up', color: 'var(--nd-alert)', bg: 'var(--nd-alert-bg)', dot: 'var(--nd-alert)', pulse: false, glow: false },
  // Feed-only (backend kind "vm.degraded"): a VM absent from libvirt entirely
  // has no card, so this never reaches getStatus() through a real VM. Kept
  // here so the events feed's badge for it shares the same color.
  degraded: { label: 'Degraded', color: 'var(--nd-alert)', bg: 'var(--nd-alert-bg)', dot: 'var(--nd-alert)', pulse: false, glow: false },
} as const;

// surfaceCard is the raised panel every card and table sits on.
export const surfaceCard = {
  background: 'var(--nd-surface)',
  border: '1px solid var(--nd-line)',
  borderRadius: 14,
  boxShadow: 'var(--nd-shadow)',
} as const;

export function getOS(os?: string) {
  return OS_META[(os || '').toLowerCase()] || { color: 'var(--nd-muted)', label: os || 'Linux' };
}

export function getStatus(status: string) {
  return STATUS_META[status as keyof typeof STATUS_META] || STATUS_META.stopped;
}

export function formatBytes(bytes: number | undefined | null, decimals = 1): string {
  if (bytes == null || isNaN(bytes) || bytes === 0) return '—';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), sizes.length - 1);
  if (i < 0) return '—';
  return parseFloat((bytes / Math.pow(k, i)).toFixed(decimals)) + ' ' + sizes[i];
}

export function formatGB(bytes: number, decimals = 1): string {
  return (bytes / 1024 / 1024 / 1024).toFixed(decimals);
}

export function formatUptime(seconds: number | undefined | null): string {
  if (!seconds || seconds <= 0) return '—';
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes > 0) return `${minutes}m`;
  return '<1m';
}

// formatClock renders a short duration as m:ss, for counters that tick.
export function formatClock(seconds: number | undefined | null): string {
  const s = Math.max(0, Math.floor(seconds || 0));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
}

// bootProgress says what a booting VM is waiting for, or null when it is not
// booting. It reads only what GET /vms reports: an address appears once the
// guest has one (its DHCP lease, or its own report on a Mac), and the phase
// turns "ready" once the backend has confirmed that address is live.
export function bootProgress(vm: Pick<VM, 'status' | 'phase' | 'ip' | 'uptime_seconds'>): string | null {
  if (vm.status !== 'running') return null;
  switch (vm.phase) {
    case 'booting':
      return vm.ip
        ? `Network up · finishing boot · ${formatClock(vm.uptime_seconds)}`
        : `Starting · waiting for network · ${formatClock(vm.uptime_seconds)}`;
    case 'no-network':
      return 'No network after 3 minutes · open the console to see why';
    default:
      return null;
  }
}

export function formatRelativeTime(iso: string): string {
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(iso).getTime()) / 1000));
  if (seconds < 60) return 'just now';
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

export function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  });
}
