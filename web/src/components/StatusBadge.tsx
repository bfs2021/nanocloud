import { getStatus } from '../design';
import type { VMPhase } from '../types';

interface StatusBadgeProps {
  status: string;
  // While the VM is running, phase refines the badge: "Booting" until it has
  // an IP, "stopping" once a shutdown has been requested but not honoured.
  phase?: VMPhase;
}

// Phases that override the libvirt status while the VM is running. With
// "no-network" a VM that came up without an address says so.
const RUNNING_PHASE_OVERRIDES: VMPhase[] = ['booting', 'stopping', 'no-network'];
// "gave-up" and "crash-looping" only ever arrive alongside a non-running
// status (the backend gates them the same way), so they override on that
// side instead.
const STOPPED_PHASE_OVERRIDES: VMPhase[] = ['gave-up', 'crash-looping'];

export function StatusBadge({ status, phase }: StatusBadgeProps) {
  const overrides = status === 'running' ? RUNNING_PHASE_OVERRIDES : STOPPED_PHASE_OVERRIDES;
  const refined = phase && overrides.includes(phase);
  return <Pill s={getStatus(refined ? (phase as string) : status)} />;
}

// Pill is the badge itself, shared with a service's state badge.
export function Pill({ s }: { s: { label: string; color: string; bg: string; dot: string; pulse: boolean; glow: boolean } }) {
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 6,
        background: s.bg,
        color: s.color,
        fontSize: 12,
        fontWeight: 500,
        padding: '4px 10px',
        borderRadius: 20,
        flexShrink: 0,
      }}
    >
      <span
        style={{
          width: 7,
          height: 7,
          borderRadius: '50%',
          background: s.dot,
          display: 'inline-block',
          boxShadow: s.glow ? 'var(--nd-ready-glow)' : 'none',
          animation: s.pulse ? 'ncpulse 1.6s ease-in-out infinite' : 'none',
        }}
      />
      {s.label}
    </span>
  );
}
