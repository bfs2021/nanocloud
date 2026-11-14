import { useEffect, useState } from 'react';
import type { Snapshot } from '../../types';
import { formatDate } from '../../design';
import { createSnapshot, deleteSnapshot, listSnapshots, restoreSnapshot } from '../../api';
import { Button } from '../../ui/Button';
import { Card } from '../../ui/Card';
import { useConfirm } from '../../ui/ConfirmDialog';
import { Input } from '../../ui/Field';

interface SnapshotsPanelProps {
  vmId: string;
  // False where the backend cannot take snapshots: the panel stays mounted
  // but hidden, and never asks for the list.
  enabled: boolean;
  onError?: (fallback: string, err: unknown) => void;
  // The backend always leaves the VM stopped after a restore, and the `vm`
  // prop is owned by App, so the page needs this to show it.
  onRefreshVMs: () => void;
}

const message = { padding: 34, textAlign: 'center', fontSize: 13 } as const;

export function SnapshotsPanel({ vmId, enabled, onError, onRefreshVMs }: SnapshotsPanelProps) {
  const [snapshots, setSnapshots] = useState<Snapshot[]>([]);
  const [nameInput, setNameInput] = useState('');
  const [showNameInput, setShowNameInput] = useState(false);
  const [creating, setCreating] = useState(false);
  const [loading, setLoading] = useState(false);
  // Tells "this VM has no snapshots" apart from "we couldn't ask".
  const [loadError, setLoadError] = useState<string | null>(null);
  // Tracked by name (not a bool) so only that row shows a loading state.
  const [restoring, setRestoring] = useState<string | null>(null);
  const [confirm, confirmDialog] = useConfirm();

  useEffect(() => {
    if (!enabled) return;
    setLoading(true);
    listSnapshots(vmId)
      .then(snaps => {
        setSnapshots(snaps);
        setLoadError(null);
      })
      .catch(e => {
        setSnapshots([]);
        setLoadError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => setLoading(false));
  }, [vmId, enabled]);

  async function handleCreate() {
    const name = nameInput.trim();
    if (!name) return;
    setCreating(true);
    try {
      await createSnapshot(vmId, name);
      setNameInput('');
      setShowNameInput(false);
      setSnapshots(await listSnapshots(vmId));
    } catch (e) {
      onError?.(`Failed to create snapshot "${name}"`, e);
    } finally {
      setCreating(false);
    }
  }

  async function handleDelete(name: string) {
    const ok = await confirm({
      title: `Delete snapshot ${name}?`,
      body: 'The VM keeps running as it is; only this restore point is gone. This cannot be undone.',
      confirmLabel: 'Delete snapshot',
    });
    if (!ok) return;
    try {
      await deleteSnapshot(vmId, name);
      setSnapshots(prev => prev.filter(s => s.name !== name));
    } catch (e) {
      onError?.(`Failed to delete snapshot "${name}"`, e);
    }
  }

  async function handleRestore(name: string) {
    const confirmed = await confirm({
      title: `Restore snapshot ${name}?`,
      body: 'The VM’s disks go back to how they were when it was taken, and everything written since is lost. The VM is off afterwards; start it again yourself.',
      confirmLabel: 'Restore snapshot',
    });
    if (!confirmed) return;

    setRestoring(name);
    try {
      await restoreSnapshot(vmId, name);
      setSnapshots(await listSnapshots(vmId));
      onRefreshVMs();
    } catch (e) {
      onError?.(`Failed to restore snapshot "${name}"`, e);
    } finally {
      setRestoring(null);
    }
  }

  return (
    <>
      {confirmDialog}
      <div hidden={!enabled}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 11 }}>
          <div
            style={{
              fontFamily: 'var(--nd-font-mono)',
              fontSize: 11,
              letterSpacing: '0.05em',
              textTransform: 'uppercase',
              color: 'var(--nd-muted)',
            }}
          >
            Snapshots
          </div>
          <Button size="sm" onClick={() => setShowNameInput(v => !v)}>
            + Create snapshot
          </Button>
        </div>

        {showNameInput && (
          <div style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
            <Input
              value={nameInput}
              onChange={e => setNameInput(e.target.value)}
              placeholder="snapshot-name"
              aria-label="Snapshot name"
              onKeyDown={e => e.key === 'Enter' && handleCreate()}
              style={{ flex: 1, fontFamily: 'var(--nd-font-mono)' }}
            />
            <Button variant="primary" onClick={handleCreate} busy={creating} disabled={!nameInput.trim()}>
              Create
            </Button>
          </div>
        )}

        <Card as="div" style={{ padding: 0, overflow: 'hidden' }}>
          {loading ? (
            <div style={{ ...message, color: 'var(--nd-muted)' }}>Loading…</div>
          ) : loadError ? (
            <div role="alert" style={{ ...message, color: 'var(--nd-danger)' }}>
              Couldn't load snapshots: {loadError}
            </div>
          ) : snapshots.length === 0 ? (
            <div style={{ ...message, color: 'var(--nd-muted)' }}>No snapshots yet for this VM.</div>
          ) : (
            snapshots.map(s => {
              const isRestoring = restoring === s.name;
              const rowBusy = restoring !== null;
              return (
                <div
                  key={s.name}
                  style={{
                    display: 'grid',
                    gridTemplateColumns: '1.1fr .8fr 1.4fr',
                    gap: 12,
                    padding: '14px 20px',
                    borderBottom: '1px solid var(--nd-line-soft)',
                    alignItems: 'center',
                    fontSize: 13,
                    opacity: rowBusy && !isRestoring ? 0.5 : 1,
                    transition: 'opacity 0.15s',
                  }}
                >
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                    <span
                      style={{
                        width: 8,
                        height: 8,
                        background: 'var(--nd-violet)',
                        borderRadius: 2,
                        transform: 'rotate(45deg)',
                        display: 'inline-block',
                        flexShrink: 0,
                      }}
                    />
                    <span style={{ fontFamily: 'var(--nd-font-mono)', fontSize: 12.5, fontWeight: 500 }}>{s.name}</span>
                  </div>
                  <div style={{ fontFamily: 'var(--nd-font-mono)', fontSize: 12, color: 'var(--nd-muted)' }}>
                    {formatDate(s.created_at)}
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
                    <Button
                      variant="primary"
                      size="sm"
                      onClick={() => handleRestore(s.name)}
                      disabled={rowBusy}
                      busy={isRestoring}
                      title="Roll back the VM's disk to this snapshot and power it off"
                    >
                      {isRestoring ? 'Restoring…' : 'Restore'}
                    </Button>
                    <Button size="sm" onClick={() => handleDelete(s.name)} disabled={rowBusy}>
                      Delete
                    </Button>
                  </div>
                </div>
              );
            })
          )}
        </Card>
      </div>
    </>
  );
}
