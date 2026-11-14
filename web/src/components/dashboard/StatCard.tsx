interface StatCardProps {
  label: string;
  value: string;
  sub?: string;
  accent?: string;
  dot?: string;
  barPct?: number;
  barColor?: string;
}

export function StatCard({ label, value, sub, accent, dot, barPct, barColor }: StatCardProps) {
  return (
    <div
      style={{
        background: 'var(--nd-surface)',
        border: '1px solid var(--nd-line)',
        borderRadius: 12,
        padding: '16px 18px',
        boxShadow: 'var(--nd-shadow)',
      }}
    >
      <div
        style={{
          fontSize: 12,
          color: 'var(--nd-text-2)',
          marginBottom: 8,
          display: 'flex',
          alignItems: 'center',
          gap: 6,
        }}
      >
        {dot && (
          <span
            style={{
              width: 7,
              height: 7,
              borderRadius: '50%',
              background: dot,
              display: 'inline-block',
            }}
          />
        )}
        {label}
      </div>
      <div
        style={{
          fontSize: 28,
          fontWeight: 600,
          letterSpacing: '-0.02em',
          color: accent,
        }}
      >
        {value}
      </div>
      {barPct !== undefined && barColor && (
        <div
          style={{
            height: 4,
            background: 'var(--nd-surface-2)',
            borderRadius: 3,
            overflow: 'hidden',
            marginTop: 8,
          }}
        >
          <div
            style={{
              width: `${barPct}%`,
              height: '100%',
              background: barColor,
              borderRadius: 3,
            }}
          />
        </div>
      )}
      {sub && (
        <div
          style={{
            fontFamily: 'var(--nd-font-mono)',
            fontSize: 11,
            color: 'var(--nd-muted)',
            marginTop: 3,
          }}
        >
          {sub}
        </div>
      )}
    </div>
  );
}
