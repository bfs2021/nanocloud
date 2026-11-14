// Live per-VM figures from GET /vms/{id}/stats.
export interface VMStats {
  cpu_percent: number;
  memory_used_mb: number;
  memory_total_mb: number;
}

export const FORCE_OFF_HINT = 'The guest has not responded to the shutdown request — force it off';
