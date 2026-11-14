import { useEffect, useState } from 'react';
import { addStoragePool, listStoragePools, patchSettings, removeStoragePool } from '../../api';
import { formatBytes } from '../../design';
import type { Settings, SettingsPool, StoragePool } from '../../types';
import { Button } from '../../ui/Button';
import { useConfirm } from '../../ui/ConfirmDialog';
import { Field, Input } from '../../ui/Field';
import { FactList, messageOf, SettingsCard, type Apply } from './common';

export function StorageSection({ settings, apply }: { settings: Settings; apply: Apply }) {
  // Free space comes from /storage/pools; it is reread whenever the list changes.
  const [space, setSpace] = useState<Record<string, StoragePool>>({});
  const [error, setError] = useState<string | null>(null);
  const [confirm, confirmDialog] = useConfirm();
  const poolKey = settings.pools.map(p => p.name).join('\n');

  useEffect(() => {
    listStoragePools().then(
      pools => setSpace(Object.fromEntries(pools.map(p => [p.name, p]))),
      () => setSpace({}),
    );
  }, [poolKey]);

  async function run(change: () => Promise<Settings>) {
    setError(null);
    try {
      await apply(change);
    } catch (e) {
      setError(messageOf(e));
    }
  }

  async function remove(p: SettingsPool) {
    const ok = await confirm({
      title: `Remove ${p.name}?`,
      body: `New VMs can no longer be put in it. The directory ${p.path} and anything in it stay where they are.`,
      confirmLabel: 'Remove pool',
    });
    if (ok) await run(() => removeStoragePool(p.name));
  }

  return (
    <>
      {confirmDialog}
      <SettingsCard title="Storage pools">
        <p className="nd-card-text">Directories VM disks are kept in. A new VM goes in the default pool unless you pick another.</p>
        <ul className="nd-rows" style={{ listStyle: 'none', margin: 0, padding: 0 }}>
          {settings.pools.map(p => (
            <li key={p.name} className="nd-setting-row" style={{ alignItems: 'center', flexWrap: 'wrap' }}>
              <div style={{ minWidth: 0, flex: '1 1 260px' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--nd-space-2)' }}>
                  <span style={{ fontWeight: 600, fontSize: 'var(--nd-text-md)' }}>{p.name}</span>
                  {p.default && <span className="nd-chip">Default</span>}
                </div>
                <div className="nd-mono" style={{ color: 'var(--nd-text-2)' }}>
                  {p.path}
                </div>
                <div className="nd-card-text">{freeText(space[p.name])}</div>
              </div>
              <div style={{ display: 'flex', gap: 'var(--nd-space-2)' }}>
                {!p.default && (
                  <Button size="sm" onClick={() => void run(() => patchSettings({ default_pool: p.name }))}>
                    Make default
                  </Button>
                )}
                {p.removable ? (
                  <Button size="sm" variant="danger" onClick={() => void remove(p)} aria-label={`Remove ${p.name}`}>
                    Remove
                  </Button>
                ) : (
                  <span className="nd-card-text" title={`Declared in ${settings.file.config_path}`}>
                    In config.yaml
                  </span>
                )}
              </div>
            </li>
          ))}
        </ul>
        {error && (
          <p role="alert" className="nd-field-error" style={{ whiteSpace: 'pre-wrap' }}>
            {error}
          </p>
        )}
      </SettingsCard>
      <AddPoolCard apply={apply} />
      <SettingsCard title="Image cache">
        <FactList facts={[['Location', <span className="nd-mono">{settings.file.image_cache_dir}</span>]]} />
        <p className="nd-card-text">
          Set by <code className="nd-mono">image_cache_dir</code> in {settings.file.config_path}.
        </p>
      </SettingsCard>
    </>
  );
}

function freeText(p?: StoragePool) {
  if (!p || !p.total_bytes) return 'Free space unknown';
  return `${formatBytes(p.free_bytes)} free of ${formatBytes(p.total_bytes)}`;
}

// The daemon creates the directory and writes to it before accepting it, so
// a path it cannot use is refused here, with the reason, not at the first VM.
function AddPoolCard({ apply }: { apply: Apply }) {
  const [name, setName] = useState('');
  const [path, setPath] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const pathProblem = path !== '' && !path.startsWith('/') ? 'An absolute path, starting with /.' : undefined;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!name || !path || pathProblem || busy) return;
    setBusy(true);
    setError(null);
    try {
      await apply(() => addStoragePool(name.trim(), path.trim()));
      setName('');
      setPath('');
    } catch (err) {
      setError(messageOf(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <SettingsCard title="Add a pool">
      <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--nd-space-3)', maxWidth: 480 }}>
        <Field label="Name">
          <Input value={name} onChange={e => setName(e.target.value)} placeholder="fast-nvme" maxLength={63} />
        </Field>
        <Field label="Directory" hint="Created if it does not exist." error={pathProblem}>
          <Input value={path} onChange={e => setPath(e.target.value)} placeholder="/mnt/nvme/vms" className="nd-mono" />
        </Field>
        <div>
          <Button type="submit" variant="primary" disabled={!name || !path || !!pathProblem} busy={busy}>
            Add pool
          </Button>
        </div>
        {error && (
          <p role="alert" className="nd-field-error" style={{ whiteSpace: 'pre-wrap' }}>
            {error}
          </p>
        )}
      </form>
    </SettingsCard>
  );
}
