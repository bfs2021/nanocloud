import { useId, useState } from 'react';
import { getStatus, formatRelativeTime } from '../design';
import type { NanoEvent, EventLevel } from '../types';

// Severity must not rely on colour alone, so warn and error also get a text
// tag; "info" needs no extra ink.
const LEVEL_DOT: Record<EventLevel, string> = {
  info: 'var(--nd-accent)',
  warn: 'var(--nd-warn)',
  error: 'var(--nd-danger)',
};

// OPEN_KEY remembers, in this browser, whether the feed is unfolded. It
// starts folded: a line saying what happened last is enough most days.
const OPEN_KEY = 'nanocloud.activity';

function readOpen(): boolean {
  try {
    return localStorage.getItem(OPEN_KEY) === 'open';
  } catch {
    return false;
  }
}

interface EventsFeedProps {
  events: NanoEvent[];
  // The feed can hold far more than fits here; App keeps a bounded backlog,
  // this just caps how much of it renders.
  limit?: number;
}

export function EventsFeed({ events, limit = 8 }: EventsFeedProps) {
  const [open, setOpen] = useState(readOpen);
  const listId = useId();
  // App stores events oldest-first (append order); show newest first.
  const shown = [...events].slice(-limit).reverse();
  const latest = shown[0];
  const serious = shown.filter(e => e.level !== 'info').length;

  function toggle() {
    const next = !open;
    setOpen(next);
    try {
      localStorage.setItem(OPEN_KEY, next ? 'open' : 'closed');
    } catch {
      // Storage can be off (private windows); it just will not be remembered.
    }
  }

  return (
    <div
      style={{
        background: 'var(--nd-surface)',
        border: '1px solid var(--nd-line)',
        borderRadius: 14,
        marginBottom: 28,
        overflow: 'hidden',
      }}
    >
      <button
        type="button"
        onClick={toggle}
        aria-expanded={open}
        aria-controls={listId}
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 10,
          width: '100%',
          minHeight: 46,
          padding: '0 20px',
          border: 'none',
          borderBottom: open ? '1px solid var(--nd-line-soft)' : 'none',
          background: 'none',
          color: 'var(--nd-text)',
          cursor: 'pointer',
          textAlign: 'left',
        }}
      >
        <svg
          width="12"
          height="12"
          viewBox="0 0 12 12"
          fill="none"
          aria-hidden="true"
          style={{ flexShrink: 0, transform: open ? 'rotate(90deg)' : 'none', transition: 'transform 0.15s ease' }}
        >
          <path d="M4.5 2.5 8 6l-3.5 3.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
        <span style={{ fontSize: 13, fontWeight: 600 }}>Activity</span>
        {serious > 0 && (
          <span style={{ fontSize: 11, fontWeight: 600, color: shown.some(e => e.level === 'error') ? LEVEL_DOT.error : LEVEL_DOT.warn }}>
            {serious} {serious === 1 ? 'needs' : 'need'} a look
          </span>
        )}
        {!open && (
          <span
            style={{
              flex: 1,
              minWidth: 0,
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
              fontSize: 12.5,
              color: 'var(--nd-muted)',
            }}
          >
            {latest ? `${latest.vm ? `${latest.vm} · ` : ''}${latest.message}` : 'Nothing to report'}
          </span>
        )}
        {!open && latest && (
          <span style={{ color: 'var(--nd-muted)', fontSize: 11, whiteSpace: 'nowrap', flexShrink: 0, marginLeft: 'auto' }}>
            {formatRelativeTime(latest.at)}
          </span>
        )}
      </button>
      {open && (
        <div id={listId}>
          {shown.length === 0 ? (
            <div style={{ padding: '22px 20px', color: 'var(--nd-muted)', fontSize: 12.5 }}>
              Nothing to report — nano-cloud hasn't had to step in.
            </div>
          ) : (
            <ul style={{ listStyle: 'none', margin: 0, padding: 0, maxHeight: 240, overflowY: 'auto' }}>
              {shown.map(ev => {
                const dotColor = ev.kind === 'vm.degraded' ? getStatus('degraded').dot : LEVEL_DOT[ev.level];
                return (
                  <li
                    key={ev.seq}
                    style={{
                      display: 'flex',
                      gap: 10,
                      alignItems: 'flex-start',
                      padding: '10px 20px',
                      borderBottom: '1px solid var(--nd-line-soft)',
                      fontSize: 12.5,
                    }}
                  >
                    <span
                      role="img"
                      aria-label={ev.level}
                      title={ev.level}
                      style={{ width: 7, height: 7, borderRadius: '50%', background: dotColor, marginTop: 5, flexShrink: 0 }}
                    />
                    <div style={{ flex: 1, minWidth: 0 }}>
                      {ev.level !== 'info' && (
                        <span style={{ textTransform: 'uppercase', fontSize: 9.5, fontWeight: 700, color: dotColor, marginRight: 6 }}>
                          {ev.level}
                        </span>
                      )}
                      {ev.vm && <span style={{ fontFamily: 'var(--nd-font-mono)', fontWeight: 600, marginRight: 6 }}>{ev.vm}</span>}
                      {ev.message}
                      {/* The detail is the only lasting record of why a restart failed. */}
                      {ev.detail && (
                        <div
                          style={{
                            marginTop: 3,
                            fontFamily: 'var(--nd-font-mono)',
                            fontSize: 11.5,
                            color: 'var(--nd-text-2)',
                            overflowWrap: 'anywhere',
                          }}
                        >
                          {ev.detail}
                        </div>
                      )}
                    </div>
                    <span style={{ color: 'var(--nd-muted)', fontSize: 11, whiteSpace: 'nowrap', flexShrink: 0 }}>
                      {formatRelativeTime(ev.at)}
                    </span>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}
