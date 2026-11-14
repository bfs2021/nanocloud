import { useCallback, useEffect, useState } from 'react';
import type { StatsSample, VMStats } from '../../types';
import { getStatsHistory, getVMStats } from '../../api';

// Polls while `active` and the VM is running. The state lives with the page,
// not the Overview tab, so the charts keep their last points when you switch
// tabs and back.
export function useVMStats(vmId: string, running: boolean, active: boolean) {
  const [stats, setStats] = useState<VMStats | null>(null);
  const [history, setHistory] = useState<StatsSample[]>([]);

  const poll = useCallback(async () => {
    if (!running) return;
    try {
      const [s, h] = await Promise.all([getVMStats(vmId), getStatsHistory(vmId)]);
      setStats(s);
      setHistory(h);
    } catch {
      // silently ignore poll errors
    }
  }, [vmId, running]);

  useEffect(() => {
    if (!active || !running) return;
    poll();
    const id = setInterval(poll, 2000);
    return () => clearInterval(id);
  }, [active, running, poll]);

  return { stats, history };
}
