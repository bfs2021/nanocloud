import { useEffect, useState } from 'react';
import type { HealthCheck } from '../../types';
import { getSystemDeps } from '../../api';
import { RemedySnippet } from '../RemedySnippet';
import { CheckBadge, type BadgeVariant } from '../CheckBadge';
import { HostDevices } from '../HostDevices';
import { TemplatesCard } from '../TemplatesCard';
import { Spinner } from '../../ui/Spinner';
import { messageOf, SettingsCard } from './common';

// A check is either required (and must pass) or informational (never a
// failure, e.g. the host bridge — NAT works fine without it). Badge + tone follow
// that distinction rather than raw `ok`, so an informational check reads as
// neutral even when its `ok` happens to be false.
function CheckRow({ check }: { check: HealthCheck }) {
  const variant: BadgeVariant = !check.required ? 'info' : check.ok ? 'ok' : 'attention';
  const badgeLabel = !check.required ? (check.ok ? 'Available' : 'Not present') : check.ok ? 'OK' : 'Needs attention';

  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'flex-start',
        justifyContent: 'space-between',
        gap: 16,
        padding: '14px 0',
        borderBottom: '1px solid var(--nd-line-soft)',
      }}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0, flex: 1 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <code
            style={{
              fontFamily: 'var(--nd-font-mono)',
              fontSize: 13.5,
              color: 'var(--nd-text)',
              background: 'var(--nd-surface-2)',
              padding: '1px 6px',
              borderRadius: 5,
              letterSpacing: '0.01em',
            }}
          >
            {check.name}
          </code>
          {!check.required && (
            <span
              style={{
                fontSize: 10.5,
                color: 'var(--nd-muted)',
                fontFamily: 'var(--nd-font-mono)',
                letterSpacing: '0.05em',
                textTransform: 'uppercase',
              }}
            >
              optional
            </span>
          )}
        </div>
        <div style={{ fontSize: 12.5, color: 'var(--nd-text-2)', marginTop: 2 }}>{check.message}</div>
        {check.remedy && <RemedySnippet command={check.remedy} tone={check.required ? 'danger' : 'neutral'} />}
      </div>
      <div style={{ flexShrink: 0, marginTop: 1 }}>
        <CheckBadge variant={variant} label={badgeLabel} />
      </div>
    </div>
  );
}

export function HostSection() {
  return (
    <>
      <ReadinessCard />
      <HostDevices />
      <TemplatesCard />
    </>
  );
}

function ReadinessCard() {
  const [deps, setDeps] = useState<HealthCheck[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    getSystemDeps().then(
      data => setDeps(data.checks),
      e => setError(messageOf(e)),
    );
  }, []);

  return (
    <SettingsCard title="Readiness">
      <p className="nd-card-text">What nano-cloud needs from this host: tools, the hypervisor and networking.</p>
      {!deps && !error && (
        <div className="nd-card-text" style={{ display: 'flex', alignItems: 'center', gap: 9 }}>
          <Spinner size={15} shrink />
          Running host checks…
        </div>
      )}
      {error && (
        <p role="alert" className="nd-field-error">
          Could not run the host checks: {error}
        </p>
      )}
      {deps && deps.length === 0 && <p className="nd-card-text">No checks reported.</p>}
      {deps && deps.length > 0 && (
        <div>
          {deps.map(check => (
            <CheckRow key={check.name} check={check} />
          ))}
        </div>
      )}
    </SettingsCard>
  );
}
