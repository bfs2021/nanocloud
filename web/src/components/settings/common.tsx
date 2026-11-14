import { useCallback, useEffect, useId, useState, type ReactNode } from 'react';
import { getSettings } from '../../api';
import type { Settings } from '../../types';
import { Card } from '../../ui/Card';
import { Switch } from '../../ui/Switch';

export const messageOf = (e: unknown) => (e instanceof Error ? e.message : String(e));

// useSettingsData loads GET /settings once for the whole page. apply runs a
// change and takes the daemon's answer as the new state; it rethrows, so the
// control that asked can say what went wrong beside itself.
export function useSettingsData() {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    getSettings().then(setSettings, e => setError(messageOf(e)));
  }, []);

  const apply = useCallback(async (change: () => Promise<Settings>) => {
    setSettings(await change());
  }, []);

  return { settings, error, apply };
}

export type Apply = ReturnType<typeof useSettingsData>['apply'];

export function SettingsCard({ title, children }: { title: string; children: ReactNode }) {
  const id = useId();
  return (
    <Card aria-labelledby={id}>
      <h2 id={id} className="nd-card-title">
        {title}
      </h2>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nd-space-4)', marginTop: 'var(--nd-space-3)' }}>{children}</div>
    </Card>
  );
}

// SwitchRow is one on/off setting, named by its visible title. The switch is
// held off while the change is in flight, and a refusal is shown beneath it.
export function SwitchRow({
  title,
  description,
  checked,
  onChange,
}: {
  title: string;
  description: ReactNode;
  checked: boolean;
  onChange: (on: boolean) => Promise<void>;
}) {
  const id = useId();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function flip(on: boolean) {
    setBusy(true);
    setError(null);
    try {
      await onChange(on);
    } catch (e) {
      setError(messageOf(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="nd-setting-row">
      <div>
        <div id={`${id}-t`} style={{ fontWeight: 600, fontSize: 'var(--nd-text-md)' }}>
          {title}
        </div>
        <div id={`${id}-d`} className="nd-card-text" style={{ marginTop: 2 }}>
          {description}
        </div>
        {error && (
          <div role="alert" className="nd-field-error" style={{ marginTop: 6 }}>
            {error}
          </div>
        )}
      </div>
      <Switch checked={checked} onChange={flip} disabled={busy} aria-labelledby={`${id}-t`} aria-describedby={`${id}-d`} />
    </div>
  );
}

// FactList is label/value pairs read off the daemon, such as file-only config.
export function FactList({ facts }: { facts: [string, ReactNode][] }) {
  return (
    <dl className="nd-rows" style={{ margin: 0 }}>
      {facts.map(([k, v]) => (
        <div key={k} style={{ display: 'grid', gridTemplateColumns: 'minmax(120px, 34%) 1fr', gap: 'var(--nd-space-3)' }}>
          <dt className="nd-card-text">{k}</dt>
          <dd style={{ margin: 0, fontSize: 'var(--nd-text-base)', minWidth: 0 }}>{v}</dd>
        </div>
      ))}
    </dl>
  );
}
