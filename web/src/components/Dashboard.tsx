import { useState } from 'react';
import type { VM, StoragePool, HealthCheck, NanoEvent, Service } from '../types';
import { FirstRunChecklist } from './dashboard/FirstRunChecklist';
import { EventsFeed } from './EventsFeed';
import { StatsRow } from './dashboard/StatsRow';
import { VMList } from './dashboard/VMList';
import { ServiceGroup } from './dashboard/ServiceGroup';
import type { VMStats } from './dashboard/shared';

interface DashboardProps {
  vms: VM[];
  // Set when the VM list fetch failed. Rendered as an explicit error rather
  // than falling through to the empty state, which would make a backend
  // outage look like an install with no VMs.
  vmsError?: string | null;
  // The self-healing feed (GET /events). Optional; the feed shows its empty
  // state without it.
  events?: NanoEvent[];
  // VMs whose graceful shutdown has gone unanswered long enough to offer the
  // force-off escape hatch.
  forceOffIds?: Set<string>;
  onForceStop?: (id: string) => void;
  pools: StoragePool[];
  hostname: string;
  cpuPercent: number;
  memUsedMb: number;
  memTotalMb: number;
  leaseMap: Record<string, string>;
  statsMap: Record<string, VMStats>;
  readinessChecks: HealthCheck[];
  layout: 'cards' | 'table';
  onLayoutChange: (layout: 'cards' | 'table') => void;
  onStart: (id: string) => void;
  onStop: (id: string) => void;
  onDelete: (id: string) => void;
  onOpenDetail: (id: string) => void;
  onNewVM: () => void;
  // Settings' Host section, where the readiness checks are listed.
  onOpenSettings: () => void;
  onOpenImages?: () => void;
  // Downloaded images, for the first-run checklist.
  cachedImageCount?: number;
  // Opens the command palette (⌘K); the header shows its button only when set.
  onOpenPalette?: () => void;
  // Opens the service a VM belongs to, from the chip on its card.
  onOpenService?: (name: string) => void;
  // The services, for the headings of their VMs' groups.
  services?: Service[];
}

type VMFilter = 'all' | 'mine' | 'managed';

const sectionTitle: React.CSSProperties = {
  margin: 0,
  fontSize: 13,
  fontWeight: 600,
  color: 'var(--nd-muted)',
  textTransform: 'uppercase',
  letterSpacing: '0.06em',
};

export function Dashboard({
  vms,
  vmsError,
  events,
  forceOffIds,
  onForceStop,
  pools,
  hostname,
  cpuPercent,
  memUsedMb,
  memTotalMb,
  leaseMap,
  statsMap,
  readinessChecks,
  layout,
  onLayoutChange,
  onStart,
  onStop,
  onDelete,
  onOpenDetail,
  onNewVM,
  onOpenImages,
  cachedImageCount = 0,
  onOpenSettings,
  onOpenPalette,
  onOpenService,
  services = [],
}: DashboardProps) {
  const [filter, setFilter] = useState<VMFilter>('all');
  const booting = vms.filter(v => v.status === 'running' && v.phase === 'booting').length;
  const attention = vms.filter(v => v.phase === 'no-network' || v.phase === 'gave-up' || v.phase === 'crash-looping').length;
  const summary = [
    `${vms.length} VM${vms.length === 1 ? '' : 's'}`,
    booting ? `${booting} booting` : null,
    attention ? `${attention} need${attention === 1 ? 's' : ''} a look` : null,
  ]
    .filter(Boolean)
    .join(' · ');

  // A service's VMs are grouped under it, apart from the person's own.
  const mine = vms.filter(v => !v.service);
  const managed = vms.filter(v => v.service);
  const groups: [string, VM[]][] = [];
  for (const vm of managed) {
    const g = groups.find(([name]) => name === vm.service);
    if (g) g[1].push(vm);
    else groups.push([vm.service!, [vm]]);
  }

  const listProps = {
    layout,
    leaseMap,
    statsMap,
    forceOffIds,
    onStart,
    onStop,
    onForceStop,
    onDelete,
    onOpen: onOpenDetail,
    onOpenService,
  };

  const segBtn = (active: boolean): React.CSSProperties => ({
    background: active ? 'var(--nd-accent-soft)' : 'transparent',
    color: active ? 'var(--nd-accent)' : 'var(--nd-text-2)',
    border: 'none',
    borderRadius: 7,
    padding: '6px 14px',
    fontSize: 13,
    fontWeight: active ? 600 : 400,
    cursor: 'pointer',
  });

  return (
    <div style={{ padding: '28px clamp(16px, 3vw, 40px) 60px', maxWidth: 1320, width: '100%' }}>
      {/* Header */}
      <div
        style={{
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'center',
          gap: 14,
          marginBottom: 24,
        }}
      >
        <div style={{ flex: '1 1 280px', minWidth: 0 }}>
          <h1
            style={{
              margin: '0 0 4px',
              fontSize: 28,
              fontWeight: 650,
              letterSpacing: '-0.025em',
            }}
          >
            Virtual machines
          </h1>
          <p style={{ margin: 0, color: 'var(--nd-muted)', fontSize: 14 }}>
            {summary} on <span style={{ fontFamily: 'var(--nd-font-mono)' }}>{hostname}</span>
          </p>
        </div>
        {onOpenPalette && (
          <button
            onClick={onOpenPalette}
            title="Search or run a command (⌘K)"
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 10,
              minHeight: 40,
              minWidth: 230,
              padding: '0 10px 0 13px',
              borderRadius: 10,
              border: '1px solid var(--nd-line)',
              background: 'var(--nd-surface)',
              color: 'var(--nd-muted)',
              fontSize: 13.5,
              cursor: 'pointer',
            }}
          >
            <svg width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <circle cx="7" cy="7" r="4.6" stroke="currentColor" strokeWidth="1.5" />
              <path d="m10.5 10.5 3.5 3.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
            </svg>
            <span style={{ flex: 1, textAlign: 'left' }}>Search or run a command</span>
            <kbd
              style={{
                fontFamily: 'var(--nd-font-mono)',
                fontSize: 11.5,
                padding: '2px 6px',
                borderRadius: 6,
                border: '1px solid var(--nd-line)',
              }}
            >
              ⌘K
            </kbd>
          </button>
        )}
        <div
          style={{
            display: 'flex',
            gap: 2,
            background: 'var(--nd-surface)',
            border: '1px solid var(--nd-line)',
            borderRadius: 9,
            padding: 3,
          }}
        >
          <button style={segBtn(layout === 'cards')} onClick={() => onLayoutChange('cards')}>
            Cards
          </button>
          <button style={segBtn(layout === 'table')} onClick={() => onLayoutChange('table')}>
            Table
          </button>
        </div>
      </div>

      <StatsRow vms={vms} pools={pools} cpuPercent={cpuPercent} memUsedMb={memUsedMb} memTotalMb={memTotalMb} />

      <EventsFeed events={events ?? []} />

      {/* VM list, error, or empty state. The error branch comes first: a failed
          fetch leaves `vms` empty, which must not read as "No virtual machines yet". */}
      {vmsError ? (
        <div
          role="alert"
          style={{
            background: 'var(--nd-danger-bg)',
            border: '1px solid var(--nd-danger-line)',
            borderRadius: 16,
            padding: '40px 24px',
            textAlign: 'center',
          }}
        >
          <h3 style={{ margin: '0 0 6px', fontSize: 17, fontWeight: 600, color: 'var(--nd-danger)' }}>Couldn't load virtual machines</h3>
          <p style={{ margin: 0, color: 'var(--nd-danger)', fontSize: 13.5, wordBreak: 'break-word' }}>{vmsError}</p>
          <p style={{ margin: '10px 0 0', color: 'var(--nd-muted)', fontSize: 12.5 }}>Retrying automatically every 10 seconds.</p>
        </div>
      ) : vms.length === 0 ? (
        <FirstRunChecklist
          readinessChecks={readinessChecks}
          cachedImageCount={cachedImageCount}
          onOpenHost={onOpenSettings}
          onOpenImages={onOpenImages ?? (() => {})}
          onNewVM={onNewVM}
        />
      ) : managed.length === 0 ? (
        <VMList {...listProps} vms={vms} />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 22 }}>
          <div role="group" aria-label="Show" style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
            {(
              [
                ['all', 'All', vms.length],
                ['mine', 'Yours', mine.length],
                ['managed', 'Managed by services', managed.length],
              ] as [VMFilter, string, number][]
            ).map(([id, label, n]) => (
              <button
                key={id}
                aria-pressed={filter === id}
                onClick={() => setFilter(id)}
                style={{
                  minHeight: 36,
                  padding: '0 14px',
                  borderRadius: 999,
                  border: `1px solid ${filter === id ? 'var(--nd-accent-line)' : 'var(--nd-line)'}`,
                  background: filter === id ? 'var(--nd-accent-soft)' : 'var(--nd-surface)',
                  color: 'var(--nd-text)',
                  fontSize: 13.5,
                  cursor: 'pointer',
                }}
              >
                {label} <span style={{ fontFamily: 'var(--nd-font-mono)', color: 'var(--nd-muted)' }}>{n}</span>
              </button>
            ))}
          </div>

          {filter !== 'managed' && (
            <section aria-labelledby="vms-mine" style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <h2 id="vms-mine" style={sectionTitle}>
                Your VMs
              </h2>
              {mine.length > 0 ? (
                <VMList {...listProps} vms={mine} />
              ) : (
                <p style={{ margin: 0, fontSize: 13.5, color: 'var(--nd-muted)' }}>None yet: every VM here runs a service.</p>
              )}
            </section>
          )}

          {filter !== 'mine' && (
            <section aria-labelledby="vms-managed" style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'baseline', gap: 10 }}>
                <h2 id="vms-managed" style={sectionTitle}>
                  Managed by services
                </h2>
                <span style={{ fontSize: 12.5, color: 'var(--nd-muted)' }}>
                  Open, use and stop them like any VM. Delete or resize them through their service.
                </span>
              </div>
              {groups.map(([name, list]) => (
                <ServiceGroup key={name} name={name} service={services.find(sv => sv.name === name)} onOpenService={onOpenService}>
                  <VMList {...listProps} vms={list} inGroup />
                </ServiceGroup>
              ))}
            </section>
          )}
        </div>
      )}
    </div>
  );
}
