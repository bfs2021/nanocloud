import { useState } from 'react';
import type { HealthCheck } from '../types';
import { RemedySnippet } from './RemedySnippet';

interface HostReadinessBannerProps {
  checks: HealthCheck[];
  onOpenSettings?: () => void;
  style?: React.CSSProperties;
}

// Dashboard-level warning when the host isn't ready to run VMs. Checks with
// `required: false` are informational and ignored. Dismissal is keyed on which
// checks are failing, so a different failure later shows the banner again.
export function HostReadinessBanner({ checks, onOpenSettings, style }: HostReadinessBannerProps) {
  const failing = checks.filter(c => c.required && !c.ok);
  const failingKey = failing.map(c => c.name).join('|');
  const [dismissedKey, setDismissedKey] = useState<string | null>(null);

  if (failing.length === 0 || dismissedKey === failingKey) return null;

  const headline =
    failing.length === 1
      ? `Nano Cloud can't create VMs: ${failing[0].message}`
      : `Nano Cloud can't create VMs — ${failing.length} host requirements need attention`;

  return (
    <div
      role="alert"
      style={{
        background: 'var(--nd-danger-bg)',
        border: '1px solid var(--nd-danger-line)',
        borderRadius: 8,
        padding: '14px 16px',
        display: 'flex',
        alignItems: 'flex-start',
        gap: 10,
        ...style,
      }}
    >
      <svg width="16" height="16" viewBox="0 0 15 15" fill="none" style={{ flexShrink: 0, marginTop: 2, color: 'var(--nd-danger)' }}>
        <circle cx="7.5" cy="7.5" r="6.5" stroke="currentColor" strokeWidth="1.3" />
        <path d="M7.5 4.5v3.5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
        <circle cx="7.5" cy="10.5" r="0.8" fill="currentColor" />
      </svg>

      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 13.5, fontWeight: 600, color: 'var(--nd-danger)' }}>{headline}</div>

        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: 10,
            marginTop: failing.some(c => c.remedy) || failing.length > 1 ? 10 : 0,
          }}
        >
          {failing.map(c => (
            <div key={c.name}>
              {failing.length > 1 && (
                <div style={{ fontSize: 12.5, color: 'var(--nd-danger)' }}>
                  <span style={{ fontWeight: 600 }}>{c.name}</span>: {c.message}
                </div>
              )}
              {c.remedy && <RemedySnippet command={c.remedy} tone="danger" />}
            </div>
          ))}
        </div>

        {onOpenSettings && (
          <button
            onClick={onOpenSettings}
            style={{
              marginTop: 10,
              background: 'none',
              border: 'none',
              padding: 0,
              color: 'var(--nd-danger)',
              fontSize: 12.5,
              fontWeight: 600,
              cursor: 'pointer',
              textDecoration: 'underline',
            }}
          >
            View details in Settings
          </button>
        )}
      </div>

      <button
        onClick={() => setDismissedKey(failingKey)}
        aria-label="Dismiss"
        title="Dismiss"
        style={{
          background: 'none',
          border: 'none',
          color: 'var(--nd-danger)',
          fontSize: 15,
          lineHeight: 1,
          cursor: 'pointer',
          padding: 2,
          flexShrink: 0,
        }}
      >
        ✕
      </button>
    </div>
  );
}
