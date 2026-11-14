import { useState } from 'react';
import { useCopy } from '../components/useCopy';

// CopyField is one value a person will paste somewhere else: a connection
// string, a key, a command. A secret shows as dots until asked for, so a
// screen share does not leak it, but it copies the same either way.
export function CopyField({ label, value, secret = false }: { label: string; value: string; secret?: boolean }) {
  const [revealed, setRevealed] = useState(false);
  const { copied, copy } = useCopy();
  const shown = secret && !revealed ? '•'.repeat(Math.min(value.length, 24)) : value;

  const button: React.CSSProperties = {
    border: '1px solid var(--nd-line)',
    background: 'transparent',
    color: 'var(--nd-text-2)',
    borderRadius: 7,
    padding: '0 10px',
    minHeight: 30,
    fontSize: 12,
    cursor: 'pointer',
    flexShrink: 0,
  };

  return (
    <div>
      <div style={{ fontSize: 12, color: 'var(--nd-muted)', marginBottom: 5 }}>{label}</div>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          background: 'var(--nd-surface-2)',
          border: '1px solid var(--nd-line-soft)',
          borderRadius: 9,
          padding: '6px 6px 6px 12px',
        }}
      >
        <code
          aria-label={secret && !revealed ? `${label} (hidden)` : undefined}
          style={{
            flex: 1,
            minWidth: 0,
            fontFamily: 'var(--nd-font-mono)',
            fontSize: 12.5,
            color: 'var(--nd-text)',
            overflowWrap: 'anywhere',
            letterSpacing: secret && !revealed ? '0.08em' : undefined,
          }}
        >
          {shown}
        </code>
        {secret && (
          <button type="button" style={button} onClick={() => setRevealed(r => !r)} aria-pressed={revealed}>
            {revealed ? 'Hide' : 'Show'}
          </button>
        )}
        <button type="button" style={button} onClick={() => copy(value)} aria-label={`Copy ${label}`}>
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
    </div>
  );
}
