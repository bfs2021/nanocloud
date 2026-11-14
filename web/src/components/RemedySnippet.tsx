import { useState } from 'react';

// Copy-pasteable shell command, shared by the dashboard readiness banner and
// the Settings readiness list so both surfaces present `remedy` identically.
export function RemedySnippet({ command, tone = 'neutral' }: { command: string; tone?: 'danger' | 'neutral' }) {
  const [copied, setCopied] = useState(false);

  function copy() {
    navigator.clipboard.writeText(command).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  }

  const palette =
    tone === 'danger'
      ? { border: 'var(--nd-danger-line)', bg: 'var(--nd-surface)', fg: 'var(--nd-danger)', copyFg: 'var(--nd-danger)' }
      : { border: 'var(--nd-line)', bg: 'var(--nd-surface-2)', fg: 'var(--nd-text)', copyFg: 'var(--nd-accent)' };

  return (
    <button
      onClick={copy}
      title="Copy command"
      aria-label={copied ? 'Command copied' : 'Copy command'}
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 8,
        width: '100%',
        marginTop: 6,
        background: palette.bg,
        border: `1px solid ${palette.border}`,
        borderRadius: 6,
        padding: '6px 10px',
        fontFamily: 'var(--nd-font-mono)',
        fontSize: 12,
        color: palette.fg,
        cursor: 'pointer',
        textAlign: 'left',
      }}
    >
      <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{command}</span>
      <span style={{ color: copied ? 'var(--nd-ready)' : palette.copyFg, flexShrink: 0, fontWeight: 600 }}>
        {copied ? 'Copied' : 'Copy'}
      </span>
    </button>
  );
}
