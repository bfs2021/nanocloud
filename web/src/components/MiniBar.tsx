interface MiniBarProps {
  label: string;
  value: number; // 0–100 percent
  color: string;
  valueLabel: string;
}

export function MiniBar({ label, value, color, valueLabel }: MiniBarProps) {
  return (
    <div style={{ flex: 1 }}>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          fontSize: 11,
          marginBottom: 4,
        }}
      >
        <span style={{ color: 'var(--nd-muted)' }}>{label}</span>
        <span style={{ fontFamily: 'var(--nd-font-mono)', color: 'var(--nd-text-2)' }}>{valueLabel}</span>
      </div>
      <div
        style={{
          height: 5,
          background: 'var(--nd-surface-2)',
          borderRadius: 3,
          overflow: 'hidden',
        }}
      >
        <div
          style={{
            width: `${value}%`,
            height: '100%',
            background: color,
            borderRadius: 3,
            transition: 'width 0.4s ease',
          }}
        />
      </div>
    </div>
  );
}
