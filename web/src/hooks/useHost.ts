import { useCallback, useEffect, useState } from 'react';
import { getStatsHistory, getSystemDeps, getSystemInfo, getUpdateStatus } from '../api';
import { LIBVIRT_CAPABILITIES, sameCapabilities, type Capabilities } from '../capabilities';
import type { HealthCheck, StatsSample } from '../types';
import { useInterval } from './useInterval';

export interface HostStatus {
  hostname: string;
  cpuPercent: number;
  memUsedMb: number;
  memTotalMb: number;
  // 0 until the first answer, and when libvirt cannot be reached.
  cpus: number;
  // What the daemon's hypervisor can do; libvirt's set until /system says otherwise.
  capabilities: Capabilities;
  // Set when GET /system failed, so the sidebar doesn't present stale numbers.
  error: string | null;
  history: StatsSample[];
}

const INITIAL: HostStatus = {
  hostname: '...',
  cpuPercent: 0,
  memUsedMb: 0,
  memTotalMb: 0,
  cpus: 0,
  capabilities: LIBVIRT_CAPABILITIES,
  error: null,
  history: [],
};

// useHostStatus polls the host's load every 5s, the stats sampler's own
// cadence, so the trend chart never lags it.
export function useHostStatus(): HostStatus {
  const [status, setStatus] = useState(INITIAL);

  const refresh = useCallback(async () => {
    const [info, history] = await Promise.allSettled([getSystemInfo(), getStatsHistory()]);
    setStatus(prev => {
      const next = { ...prev };
      if (info.status === 'fulfilled') {
        const v = info.value;
        Object.assign(next, {
          hostname: v.hostname,
          cpuPercent: v.cpu_percent,
          memUsedMb: v.memory_used_mb,
          memTotalMb: v.memory_total_mb,
        });
        next.cpus = v.cpus ?? 0;
        next.error = null;
        // Kept identical when unchanged: every capabilities consumer re-renders otherwise.
        if (v.capabilities && !sameCapabilities(prev.capabilities, v.capabilities)) next.capabilities = v.capabilities;
      } else {
        next.error = info.reason instanceof Error ? info.reason.message : String(info.reason);
      }
      if (history.status === 'fulfilled') next.history = history.value;
      return next;
    });
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);
  useInterval(refresh, 5000);
  return status;
}

// useReadiness re-reads the host's readiness checks every 30s, so fixing the
// host while the page is open clears its banner without a reload.
export function useReadiness(): HealthCheck[] {
  const [checks, setChecks] = useState<HealthCheck[]>([]);
  const refresh = useCallback(() => {
    getSystemDeps().then(
      d => setChecks(d.checks),
      e => console.error('Failed to fetch system deps', e),
    );
  }, []);
  useEffect(refresh, [refresh]);
  useInterval(refresh, 30_000);
  return checks;
}

// useUpdateTag is a newer release's tag, or undefined. Asked once a session
// and every few hours after; the daemon caches GitHub's answer for longer, so
// this costs GitHub nothing.
export function useUpdateTag(): string | undefined {
  const [tag, setTag] = useState<string | undefined>();
  const check = useCallback(() => {
    getUpdateStatus().then(
      st => setTag(st.available ? st.latest?.tag : undefined),
      () => {},
    );
  }, []);
  useEffect(check, [check]);
  useInterval(check, 6 * 60 * 60 * 1000);
  return tag;
}
