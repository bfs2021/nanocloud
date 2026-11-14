// A hand-rolled SVG line and area chart fed by GET /stats/history, for the host
// series and any VM's. No charting library: bundle size matters in a single
// binary.

export interface ChartPoint {
  at: number; // unix seconds
  value: number;
}

interface StatsChartProps {
  points: ChartPoint[];
  label: string;
  color: string;
  // A fixed scale (e.g. 100 for a percentage) puts every chart on the same
  // axis, so a flat line pinned to the ceiling doesn't look identical to one
  // pinned to the floor. Omit to auto-scale to the series' own max.
  max?: number;
  height?: number;
}

const WIDTH = 240;

export function StatsChart({ points, label, color, max, height = 56 }: StatsChartProps) {
  if (points.length < 2) {
    return (
      <div style={{ height, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--nd-muted)', fontSize: 11 }}>
        Gathering data…
      </div>
    );
  }

  const hi = Math.max(max ?? Math.max(...points.map(p => p.value)), 1);
  const step = WIDTH / (points.length - 1);
  const y = (v: number) => height - (Math.min(Math.max(v, 0), hi) / hi) * height;

  const line = points.map((p, i) => `${i === 0 ? 'M' : 'L'} ${(i * step).toFixed(1)} ${y(p.value).toFixed(1)}`).join(' ');
  const area = `${line} L ${WIDTH} ${height} L 0 ${height} Z`;

  return (
    <svg
      role="img"
      aria-label={`${label} over time`}
      width="100%"
      height={height}
      viewBox={`0 0 ${WIDTH} ${height}`}
      preserveAspectRatio="none"
    >
      {/* Colours go through style, not the fill/stroke attributes: they are
          theme tokens (var(--nd-…)), which presentation attributes do not
          reliably resolve. */}
      <path d={area} style={{ fill: color }} opacity={0.12} stroke="none" />
      <path d={line} fill="none" style={{ stroke: color }} strokeWidth={1.6} strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  );
}
