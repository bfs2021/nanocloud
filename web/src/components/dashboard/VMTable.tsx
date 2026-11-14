import type { VM } from '../../types';
import { getOS, formatUptime } from '../../design';
import { OsChip } from '../OsChip';
import { StatusBadge } from '../StatusBadge';
import { Button } from '../../ui/Button';
import { Card } from '../../ui/Card';
import { FORCE_OFF_HINT } from './shared';
import './dashboard.css';

export function VMTable({
  vms,
  leaseMap,
  forceOffIds,
  onStart,
  onStop,
  onForceStop,
  onDelete,
  onOpen,
}: {
  vms: VM[];
  leaseMap: Record<string, string>;
  forceOffIds?: Set<string>;
  onStart: (id: string) => void;
  onStop: (id: string) => void;
  onForceStop?: (id: string) => void;
  onDelete: (id: string) => void;
  onOpen: (id: string) => void;
}) {
  const cols = '2fr 1fr 1.3fr .8fr .9fr .9fr 1fr 1.4fr';
  const headerStyle: React.CSSProperties = {
    fontFamily: 'var(--nd-font-mono)',
    fontSize: 11,
    fontWeight: 500,
    color: 'var(--nd-muted)',
    textTransform: 'uppercase',
    letterSpacing: '0.04em',
  };

  return (
    <Card as="div" style={{ padding: 0, overflow: 'hidden' }}>
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: cols,
          gap: 10,
          padding: '12px 20px',
          borderBottom: '1px solid var(--nd-line-soft)',
        }}
      >
        {['Name', 'Status', 'IP', 'vCPU', 'RAM', 'Disk', 'Uptime', ''].map((h, i) => (
          <div key={i} style={{ ...headerStyle, textAlign: i === 7 ? 'right' : 'left' }}>
            {h || 'Actions'}
          </div>
        ))}
      </div>
      {vms.map(vm => {
        const running = vm.status === 'running';
        const ip = leaseMap[vm.name] || vm.ip || '—';
        const osInfo = getOS(vm.os);
        const canForceStop = !!onForceStop && !!forceOffIds?.has(vm.id);

        return (
          <div
            key={vm.id}
            style={{
              display: 'grid',
              gridTemplateColumns: cols,
              gap: 10,
              padding: '13px 20px',
              borderBottom: '1px solid var(--nd-line-soft)',
              alignItems: 'center',
              fontSize: 13,
            }}
          >
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 10,
                minWidth: 0,
                cursor: 'pointer',
              }}
              onClick={() => onOpen(vm.id)}
            >
              <OsChip os={vm.os} size={30} />
              <div style={{ minWidth: 0 }}>
                <div
                  style={{
                    fontWeight: 600,
                    whiteSpace: 'nowrap',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                  }}
                >
                  {vm.name}
                </div>
                <div style={{ fontSize: 11, color: 'var(--nd-muted)' }}>{osInfo.label}</div>
              </div>
            </div>
            <div>
              <StatusBadge status={vm.status} phase={vm.phase} />
            </div>
            <div style={{ fontFamily: 'var(--nd-font-mono)', fontSize: 12 }}>{ip}</div>
            <div style={{ fontFamily: 'var(--nd-font-mono)', fontSize: 12 }}>{vm.cpu_count}</div>
            <div style={{ fontFamily: 'var(--nd-font-mono)', fontSize: 12 }}>{Math.round(vm.memory_mb / 1024)} GB</div>
            <div style={{ fontFamily: 'var(--nd-font-mono)', fontSize: 12 }}>{vm.disk_gb ?? '—'} GB</div>
            <div style={{ fontFamily: 'var(--nd-font-mono)', fontSize: 12, color: 'var(--nd-text-2)' }}>
              {formatUptime(vm.uptime_seconds)}
            </div>
            <div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
              {running && canForceStop && (
                <Button variant="danger" size="sm" onClick={() => onForceStop?.(vm.id)} title={FORCE_OFF_HINT}>
                  Force off
                </Button>
              )}
              {running && !canForceStop && (
                <Button size="sm" onClick={() => onStop(vm.id)} disabled={vm.phase === 'stopping'}>
                  {vm.phase === 'stopping' ? 'Stopping…' : 'Stop'}
                </Button>
              )}
              {!running && (
                <Button size="sm" className="nd-dash-start" onClick={() => onStart(vm.id)}>
                  Start
                </Button>
              )}
              <Button
                size="sm"
                onClick={() => onDelete(vm.id)}
                aria-label={`Delete ${vm.name}`}
                title={`Delete ${vm.name}`}
                style={{ width: 32, padding: 0, color: 'var(--nd-muted)' }}
              >
                ✕
              </Button>
            </div>
          </div>
        );
      })}
    </Card>
  );
}
