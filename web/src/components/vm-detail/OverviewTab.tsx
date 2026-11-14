import type { ReactNode } from 'react';
import type { StatsSample, VM, VMStats } from '../../types';
import { formatUptime, getOS } from '../../design';
import { Card } from '../../ui/Card';
import { StatsChart } from '../StatsChart';
import { CopyFactRow, FactRow } from './FactRow';

interface OverviewTabProps {
  vm: VM;
  ip: string;
  stats: VMStats | null;
  history: StatsSample[];
  // False on a backend that cannot see inside the guest (Apple Virtualization).
  reportsStats: boolean;
  hypervisor: string;
}

function UsageCard({ title, value, children }: { title: string; value: string; children: ReactNode }) {
  return (
    <Card as="div" style={{ padding: '18px 20px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', marginBottom: 14 }}>
        <div style={{ fontSize: 13, color: 'var(--nd-text-2)', fontWeight: 500 }}>{title}</div>
        <div style={{ fontSize: 22, fontWeight: 600, letterSpacing: '-0.02em' }}>{value}</div>
      </div>
      {children}
    </Card>
  );
}

export function OverviewTab({ vm, ip, stats, history, reportsStats, hypervisor }: OverviewTabProps) {
  const running = vm.status === 'running';
  const cpuPct = running && stats ? Math.round(stats.cpu_percent) : 0;
  const memPct = running && stats ? Math.round((stats.memory_used_mb / stats.memory_total_mb) * 100) : 0;

  return (
    <div>
      {!reportsStats && (
        <div style={{ fontSize: 12.5, color: 'var(--nd-text-2)', marginBottom: 12 }}>
          Apple Virtualization does not report CPU or memory use inside a VM, so the charts below stay empty.
        </div>
      )}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 16 }}>
        <UsageCard title="CPU usage" value={running ? `${cpuPct}%` : '—'}>
          <StatsChart
            points={history.map(h => ({ at: h.at, value: h.cpu_percent }))}
            label="CPU usage"
            color="var(--nd-accent)"
            max={100}
          />
        </UsageCard>
        <UsageCard title="Memory usage" value={running ? `${memPct}%` : '—'}>
          <StatsChart
            points={history.map(h => ({ at: h.at, value: h.mem_used_mb }))}
            label="Memory usage"
            color="var(--nd-ready)"
            max={vm.memory_mb}
          />
        </UsageCard>
      </div>

      <Card as="div" style={{ padding: '6px 22px' }}>
        <FactRow k="OS" v={getOS(vm.os).label} />
        <FactRow k="IP" v={ip} />
        <FactRow k="vCPU / Memory" v={`${vm.cpu_count} vCPU / ${Math.round(vm.memory_mb / 1024)} GB`} />
        <FactRow k="Disk" v={vm.disk_gb ? `${vm.disk_gb} GB` : '—'} />
        <FactRow k="Uptime" v={formatUptime(vm.uptime_seconds)} />
        {/* Gated on the address shown above, not vm.phase, which waits for the
            backend to confirm the address. */}
        {running && ip !== '—' && vm.username && <CopyFactRow k="SSH" v={`ssh ${vm.username}@${ip}`} />}
        <FactRow k="Hypervisor" v={hypervisor} />
      </Card>
    </div>
  );
}
