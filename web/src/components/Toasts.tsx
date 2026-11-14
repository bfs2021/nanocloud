import { useCallback, useRef, useState } from 'react';

export type ToastKind = 'error' | 'success';

export interface Toast {
  id: number;
  kind: ToastKind;
  message: string;
}

const DISMISS_AFTER_MS = 7000;

// useToasts is the app's failure-reporting channel. apiFetch throws an Error
// carrying the backend's response body, so messages are the server's own words.
export function useToasts() {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const nextId = useRef(0);

  const dismiss = useCallback((id: number) => {
    setToasts(prev => prev.filter(t => t.id !== id));
  }, []);

  const push = useCallback(
    (kind: ToastKind, message: string) => {
      const id = nextId.current++;
      setToasts(prev => [...prev, { id, kind, message }]);
      setTimeout(() => dismiss(id), DISMISS_AFTER_MS);
    },
    [dismiss],
  );

  // pushError accepts the raw unknown from a catch block so call sites stay
  // one line. `fallback` names the action, since a network-level failure
  // produces an opaque "Failed to fetch" with no context of its own.
  const pushError = useCallback(
    (fallback: string, err: unknown) => {
      const detail = err instanceof Error ? err.message : String(err);
      push('error', detail ? `${fallback}: ${detail}` : fallback);
    },
    [push],
  );

  return { toasts, push, pushError, dismiss };
}

interface ToastsProps {
  toasts: Toast[];
  onDismiss: (id: number) => void;
}

export function Toasts({ toasts, onDismiss }: ToastsProps) {
  if (toasts.length === 0) return null;

  return (
    <div
      // role=status rather than alert: these are consequences of an action the
      // user just took, so they should not interrupt a screen reader mid-word.
      role="status"
      aria-live="polite"
      style={{
        position: 'fixed',
        bottom: 20,
        right: 20,
        zIndex: 60,
        display: 'flex',
        flexDirection: 'column',
        gap: 8,
        maxWidth: 'min(420px, calc(100vw - 40px))',
      }}
    >
      {toasts.map(t => (
        <div
          key={t.id}
          style={{
            display: 'flex',
            alignItems: 'flex-start',
            gap: 10,
            background: t.kind === 'error' ? 'var(--nd-danger-bg)' : 'var(--nd-ready-bg)',
            border: `1px solid ${t.kind === 'error' ? 'var(--nd-danger-line)' : 'var(--nd-ready-line)'}`,
            color: t.kind === 'error' ? 'var(--nd-danger)' : 'var(--nd-ready)',
            borderRadius: 8,
            padding: '11px 12px',
            fontSize: 13,
            lineHeight: 1.45,
            boxShadow: 'var(--nd-shadow-lg)',
          }}
        >
          <span style={{ flex: 1, wordBreak: 'break-word' }}>{t.message}</span>
          <button
            onClick={() => onDismiss(t.id)}
            aria-label="Dismiss notification"
            style={{
              background: 'none',
              border: 'none',
              color: 'inherit',
              cursor: 'pointer',
              fontSize: 15,
              lineHeight: 1,
              padding: 0,
              opacity: 0.65,
              flexShrink: 0,
            }}
          >
            ✕
          </button>
        </div>
      ))}
    </div>
  );
}
