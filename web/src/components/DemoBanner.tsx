// DemoBanner heads every page of the public demo, so nobody takes the sample
// fleet for a real one or expects their changes to survive a reload.
export function DemoBanner() {
  return (
    <div
      role="note"
      data-demo-banner
      style={{
        margin: '20px clamp(16px, 3vw, 40px) 0',
        padding: '10px 14px',
        background: 'var(--nd-accent-soft)',
        border: '1px solid var(--nd-accent-line)',
        borderRadius: 'var(--nd-radius-md)',
        fontSize: 'var(--nd-text-sm)',
        color: 'var(--nd-text)',
        display: 'flex',
        flexWrap: 'wrap',
        alignItems: 'baseline',
        gap: '4px 12px',
      }}
    >
      <span>
        <strong>Demo with sample data.</strong> No VM runs here, and your changes last until you reload the page.
      </span>
      <a href="../" style={{ color: 'var(--nd-accent)', fontWeight: 600 }}>
        Install Nano Cloud
      </a>
    </div>
  );
}
