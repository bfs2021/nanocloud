import type { VM } from '../../types';
import { getOS, formatUptime, bootProgress } from '../../design';
import { OsChip } from '../OsChip';
import { StatusBadge } from '../StatusBadge';
import { BootProgressLine } from '../BootProgressLine';
import { useCopy } from '../useCopy';
import { MiniBar } from '../MiniBar';
import { BootRail, railSteps } from '../../ui/BootRail';
import { Button } from '../../ui/Button';
import { Card } from '../../ui/Card';
import { FORCE_OFF_HINT, type VMStats } from './shared';
import './dashboard.css';

export function VMCard({
  vm,
  stats,
  ip,
  canForceStop,
  onStart,
  onStop,
  onForceStop,
  onDelete,
  onOpen,
  onOpenService,
  showService = true,
}: {
  vm: VM;
  stats?: VMStats;
  ip?: string;
  canForceStop?: boolean;
  onStart: () => void;
  onStop: () => void;
  onForceStop?: () => void;
  onDelete: () => void;
  onOpen: () => void;
  onOpenService?: () => void;
  // Off inside a service's group, which already says it.
  showService?: boolean;
}) {
  const running = vm.status === 'running';
  const osInfo = getOS(vm.os);
  const cpuPct = running ? Math.round(stats?.cpu_percent ?? 0) : 0;
  const memPct = running && stats && stats.memory_total_mb > 0 ? Math.round((stats.memory_used_mb / stats.memory_total_mb) * 100) : 0;
  // Real used-vs-allocated figure, e.g. "1.2 / 4 GB".
  const memLabel =
    running && stats && stats.memory_total_mb > 0
      ? `${(stats.memory_used_mb / 1024).toFixed(1)} / ${(stats.memory_total_mb / 1024).toFixed(0)} GB`
      : '—';
  // vm.ip wins, as on the detail page: the backend resolves it live for this
  // boot, while the lease list is a fallback for a VM it has no answer for.
  const bestIp = vm.ip || ip;
  const displayIp = bestIp || '—';
  const netLabel = vm.network_mode === 'bridge' ? (vm.vlan_id ? `Bridge · VLAN ${vm.vlan_id}` : 'Bridge') : 'NAT';
  const sshTarget = vm.username && bestIp ? `ssh ${vm.username}@${bestIp}` : '';
  const { copied, copy } = useCopy();
  const progress = bootProgress(vm);
  const rail = railSteps(vm);

  return (
    <Card
      as="div"
      style={{
        padding: 18,
        display: 'flex',
        flexDirection: 'column',
        gap: 14,
        animation: 'ncfadein 0.2s ease',
      }}
    >
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <OsChip os={vm.os} size={40} />
        <div style={{ flex: 1, minWidth: 0, cursor: 'pointer' }} onClick={onOpen}>
          <div
            style={{
              fontWeight: 600,
              fontSize: 15,
              letterSpacing: '-0.01em',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
          >
            {vm.name}
          </div>
          <div style={{ fontSize: 12, color: 'var(--nd-muted)' }}>{osInfo.label}</div>
        </div>
        <StatusBadge status={vm.status} phase={vm.phase} />
      </div>

      {vm.service && showService && (
        <button
          onClick={onOpenService}
          disabled={!onOpenService}
          title={`This VM runs the service ${vm.service}`}
          style={{
            alignSelf: 'flex-start',
            display: 'inline-flex',
            alignItems: 'center',
            gap: 6,
            border: '1px solid var(--nd-accent-line)',
            background: 'var(--nd-accent-soft)',
            color: 'var(--nd-accent)',
            borderRadius: 20,
            padding: '3px 10px',
            fontSize: 12,
            cursor: onOpenService ? 'pointer' : 'default',
          }}
        >
          Part of service <span style={{ fontFamily: 'var(--nd-font-mono)' }}>{vm.service}</span>
        </button>
      )}

      {rail && <BootRail steps={rail} />}
      {progress && <BootProgressLine phase={vm.phase} text={progress} />}

      {/* Meta grid */}
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: '1fr 1fr',
          gap: '9px 16px',
          fontSize: 12.5,
        }}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between' }}>
          <span style={{ color: 'var(--nd-muted)', flexShrink: 0 }}>IP</span>
          <span style={{ fontFamily: 'var(--nd-font-mono)', whiteSpace: 'nowrap' }}>
            {displayIp}
            {displayIp !== '—' && <span style={{ color: 'var(--nd-muted)' }}> · {netLabel}</span>}
          </span>
        </div>
        <div style={{ display: 'flex', justifyContent: 'space-between' }}>
          <span style={{ color: 'var(--nd-muted)' }}>Uptime</span>
          <span style={{ fontFamily: 'var(--nd-font-mono)' }}>{formatUptime(vm.uptime_seconds)}</span>
        </div>
        <div style={{ display: 'flex', justifyContent: 'space-between' }}>
          <span style={{ color: 'var(--nd-muted)' }}>vCPU</span>
          <span style={{ fontFamily: 'var(--nd-font-mono)' }}>{vm.cpu_count}</span>
        </div>
        <div style={{ display: 'flex', justifyContent: 'space-between' }}>
          <span style={{ color: 'var(--nd-muted)' }}>Disk</span>
          <span style={{ fontFamily: 'var(--nd-font-mono)' }}>{vm.disk_gb ?? '—'} GB</span>
        </div>
      </div>

      {/* SSH helper, as soon as the VM has an address. Gated on sshTarget, not
          vm.phase, which waits for the backend to confirm the address. */}
      {sshTarget && (
        <button
          onClick={() => copy(sshTarget)}
          title="Copy SSH command"
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 8,
            background: 'var(--nd-surface-2)',
            border: '1px solid var(--nd-line)',
            borderRadius: 8,
            padding: '6px 10px',
            fontFamily: 'var(--nd-font-mono)',
            fontSize: 11.5,
            color: 'var(--nd-text-2)',
            cursor: 'pointer',
            textAlign: 'left',
            width: '100%',
          }}
        >
          <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{sshTarget}</span>
          <span style={{ color: copied ? 'var(--nd-ready)' : 'var(--nd-muted)', flexShrink: 0 }}>{copied ? 'Copied' : 'Copy'}</span>
        </button>
      )}

      {/* Mini progress bars */}
      <div style={{ display: 'flex', gap: 14 }}>
        <MiniBar label="CPU" value={cpuPct} color="var(--nd-accent)" valueLabel={running ? `${cpuPct}%` : '—'} />
        <MiniBar label="RAM" value={memPct} color="var(--nd-ready)" valueLabel={memLabel} />
      </div>

      {/* Actions */}
      <div
        style={{
          display: 'flex',
          gap: 7,
          borderTop: '1px solid var(--nd-line-soft)',
          paddingTop: 13,
        }}
      >
        {running && (
          <>
            {canForceStop && onForceStop ? (
              <Button variant="danger" size="sm" onClick={onForceStop} title={FORCE_OFF_HINT} style={{ flex: 1 }}>
                Force off
              </Button>
            ) : (
              <Button size="sm" onClick={onStop} disabled={vm.phase === 'stopping'} style={{ flex: 1 }}>
                <span
                  style={{
                    width: 8,
                    height: 8,
                    background: 'var(--nd-text-2)',
                    borderRadius: 1.5,
                    display: 'inline-block',
                  }}
                />
                {vm.phase === 'stopping' ? 'Stopping…' : 'Stop'}
              </Button>
            )}
            <Button size="sm" onClick={onOpen} style={{ flex: 1 }}>
              Manage
            </Button>
          </>
        )}
        {!running && (
          <Button size="sm" className="nd-dash-start" onClick={onStart} style={{ flex: 1 }}>
            <span
              style={{
                borderStyle: 'solid',
                borderWidth: '5px 0 5px 8px',
                borderColor: 'transparent transparent transparent var(--nd-accent)',
                display: 'inline-block',
              }}
            />
            Start
          </Button>
        )}
        <Button
          size="sm"
          onClick={onDelete}
          aria-label={`Delete ${vm.name}`}
          title={`Delete ${vm.name}`}
          style={{ width: 38, padding: 0, color: 'var(--nd-muted)' }}
        >
          ✕
        </Button>
      </div>
    </Card>
  );
}
