import type { StoragePool, VM } from '../../types';
import { StatCard } from './StatCard';

interface StatsRowProps {
  vms: VM[];
  pools: StoragePool[];
  cpuPercent: number;
  memUsedMb: number;
  memTotalMb: number;
}

export function StatsRow({ vms, pools, cpuPercent, memUsedMb, memTotalMb }: StatsRowProps) {
  const runningVMs = vms.filter(v => v.status === 'running');
  const running = runningVMs.length;
  const stopped = vms.length - running;
  const activeCpus = runningVMs.reduce((a, v) => a + v.cpu_count, 0);

  const memUsedGb = memUsedMb / 1024;
  const memTotalGb = memTotalMb / 1024;

  const totalFreeBytes = pools.reduce((a, p) => a + p.free_bytes, 0);
  const totalTotalBytes = pools.reduce((a, p) => a + p.total_bytes, 0);
  const diskUsedBytes = totalTotalBytes - totalFreeBytes;
  const diskTotalTb = totalTotalBytes / 1024 ** 4;
  const useGB = diskTotalTb < 1;
  const diskUsedDisplay = useGB
    ? (diskUsedBytes / 1024 ** 3).toFixed(0)
    : diskTotalTb < 10
      ? diskTotalTb.toFixed(2)
      : diskTotalTb.toFixed(1);
  const diskTotalDisplay = useGB
    ? (totalTotalBytes / 1024 ** 3).toFixed(0)
    : diskTotalTb < 10
      ? diskTotalTb.toFixed(2)
      : diskTotalTb.toFixed(1);
  const diskUnit = useGB ? 'GB' : 'TB';
  const diskPct = totalTotalBytes > 0 ? (diskUsedBytes / totalTotalBytes) * 100 : 0;

  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fill, minmax(136px, 1fr))',
        gap: 14,
        marginBottom: 28,
      }}
    >
      <StatCard label="Total VMs" value={String(vms.length)} />
      <StatCard label="Running" value={String(running)} accent="var(--nd-ready)" dot="var(--nd-ready)" />
      <StatCard label="Stopped" value={String(stopped)} accent="var(--nd-text-2)" dot="var(--nd-muted)" />
      <StatCard label="RAM Used" value={`${memUsedGb.toFixed(0)}`} sub={`of ${memTotalGb.toFixed(0)} GB`} />
      <StatCard label="CPU Load" value={`${Math.round(cpuPercent)}%`} sub={`${activeCpus} vCPU active`} />
      <StatCard
        label="Storage"
        value={diskUsedDisplay}
        sub={`of ${diskTotalDisplay} ${diskUnit}`}
        barPct={diskPct}
        barColor="var(--nd-violet)"
      />
    </div>
  );
}
