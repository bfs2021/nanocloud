import { useCopy } from '../useCopy';

const row = {
  display: 'flex',
  justifyContent: 'space-between',
  padding: '13px 0',
  borderBottom: '1px solid var(--nd-line-soft)',
  fontSize: 13.5,
} as const;

export function FactRow({ k, v }: { k: string; v: string }) {
  return (
    <div style={row}>
      <span style={{ color: 'var(--nd-muted)' }}>{k}</span>
      <span style={{ fontFamily: 'var(--nd-font-mono)' }}>{v}</span>
    </div>
  );
}

// CopyFactRow is a FactRow whose value is a command worth copying.
export function CopyFactRow({ k, v }: { k: string; v: string }) {
  const { copied, copy } = useCopy();
  return (
    <div style={{ ...row, alignItems: 'center', gap: 12 }}>
      <span style={{ color: 'var(--nd-muted)' }}>{k}</span>
      <span style={{ display: 'flex', alignItems: 'center', gap: 10, minWidth: 0 }}>
        <span style={{ fontFamily: 'var(--nd-font-mono)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{v}</span>
        <button
          onClick={() => copy(v)}
          title={`Copy ${k} command`}
          style={{
            border: '1px solid var(--nd-line)',
            background: 'var(--nd-surface-2)',
            borderRadius: 6,
            padding: '2px 8px',
            fontSize: 12,
            color: copied ? 'var(--nd-ready)' : 'var(--nd-text-2)',
            cursor: 'pointer',
            flexShrink: 0,
          }}
        >
          {copied ? 'Copied' : 'Copy'}
        </button>
      </span>
    </div>
  );
}
