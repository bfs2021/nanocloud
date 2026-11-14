import { useEffect, useState } from 'react';
import type { UpdateStatus } from '../types';
import { getUpdateStatus } from '../api';
import { CopyField } from '../ui/CopyField';
import { SettingsCard } from './settings/common';

// UpdatesCard says whether a newer release exists and how to install it. The
// daemon never updates itself: replacing a root daemon's binary is a step the
// person at the host takes, with a command that verifies the release first.
export function UpdatesCard() {
  const [status, setStatus] = useState<UpdateStatus | null>(null);
  const [failed, setFailed] = useState<string | null>(null);

  useEffect(() => {
    getUpdateStatus().then(setStatus, e => setFailed(e instanceof Error ? e.message : String(e)));
  }, []);

  return (
    <SettingsCard title="Updates">
      <Body status={status} failed={failed} />
    </SettingsCard>
  );
}

function Body({ status, failed }: { status: UpdateStatus | null; failed: string | null }) {
  if (failed) return <p className="nd-card-text">Could not ask the daemon: {failed}</p>;
  if (!status) return <p className="nd-card-text">Checking…</p>;

  const installed = <span style={{ fontFamily: 'var(--nd-font-mono)' }}>{status.current}</span>;
  if (status.disabled) {
    return <p className="nd-card-text">Running {installed}. Checking for updates is off, so nano-cloud never asks GitHub.</p>;
  }
  if (status.error) {
    return (
      <p className="nd-card-text">
        Running {installed}. The last check for a newer release failed: {status.error}
      </p>
    );
  }
  if (!status.available || !status.latest) {
    return <p className="nd-card-text">Running {installed}, the latest release.</p>;
  }
  const published = new Date(status.latest.published_at);
  return (
    <div style={{ display: 'grid', gap: 14 }}>
      <p className="nd-card-text">
        <strong style={{ color: 'var(--nd-text)' }}>{status.latest.tag}</strong> is available
        {Number.isNaN(published.getTime()) ? '' : ` (released ${published.toLocaleDateString()})`}; you are running {installed}.{' '}
        <a href={status.latest.url} target="_blank" rel="noreferrer" style={{ color: 'var(--nd-accent)' }}>
          Release notes
        </a>
      </p>
      <CopyField
        label="Run on the host — it verifies the release's signature, and rolls back if the new version does not start"
        value="sudo nanocloud update"
      />
    </div>
  );
}
