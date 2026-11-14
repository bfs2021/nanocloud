// Verdict pill shared by the Settings readiness list and the host device
// readout, so both surfaces say "fine" / "look at this" in the same colours.
export type BadgeVariant = 'ok' | 'attention' | 'warn' | 'info';

// `warn` is amber, not red: it marks a call the operator still gets to make,
// and reuses STATUS_META's amber so the app has one caution colour.
const PALETTE: Record<BadgeVariant, { bg: string; fg: string }> = {
  ok: { bg: 'var(--nd-ready-bg)', fg: 'var(--nd-ready)' },
  attention: { bg: 'var(--nd-danger-bg)', fg: 'var(--nd-danger)' },
  warn: { bg: 'var(--nd-warn-bg)', fg: 'var(--nd-warn)' },
  info: { bg: 'var(--nd-surface-2)', fg: 'var(--nd-text-2)' },
};

export function CheckBadge({ variant, label }: { variant: BadgeVariant; label: string }) {
  const palette = PALETTE[variant];

  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 5,
        padding: '2px 9px',
        borderRadius: 999,
        background: palette.bg,
        color: palette.fg,
        fontSize: 12,
        fontWeight: 600,
      }}
    >
      {variant === 'ok' && (
        <svg width="10" height="10" viewBox="0 0 10 10" fill="none">
          <path d="M2 5.2l2 2 4-4" style={{ stroke: palette.fg }} strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      )}
      {variant === 'attention' && (
        <svg width="10" height="10" viewBox="0 0 10 10" fill="none">
          <path d="M3 3l4 4M7 3l-4 4" style={{ stroke: palette.fg }} strokeWidth="1.5" strokeLinecap="round" />
        </svg>
      )}
      {variant === 'warn' && (
        <svg width="10" height="10" viewBox="0 0 10 10" fill="none">
          <path d="M5 1.6l3.8 6.8H1.2L5 1.6z" style={{ stroke: palette.fg }} strokeWidth="1.1" strokeLinejoin="round" />
          <path d="M5 4.3v1.6" style={{ stroke: palette.fg }} strokeWidth="1.1" strokeLinecap="round" />
        </svg>
      )}
      {variant === 'info' && <span style={{ width: 6, height: 6, borderRadius: '50%', background: palette.fg, display: 'inline-block' }} />}
      {label}
    </span>
  );
}
