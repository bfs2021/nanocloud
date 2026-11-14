import { useState } from 'react';
import type { VMDisk, VMSpec, VMStatus } from '../types';
import { attachDisk, detachDisk, resizeDisk } from '../api';
import { surfaceCard } from '../design';

// The disks half of the Storage tab, above snapshots. It states two things the
// obvious reading gets wrong: an attached disk reaches the guest at its next
// boot, and growing a disk doesn't grow the filesystem inside the guest.

const card: React.CSSProperties = {
  ...surfaceCard,
};

const sectionLabel: React.CSSProperties = {
  fontFamily: 'var(--nd-font-mono)',
  fontSize: 11,
  letterSpacing: '0.05em',
  textTransform: 'uppercase',
  color: 'var(--nd-muted)',
};

const mono: React.CSSProperties = { fontFamily: 'var(--nd-font-mono)' };

const quietButton: React.CSSProperties = {
  background: 'none',
  border: '1px solid var(--nd-line)',
  borderRadius: 7,
  padding: '4px 10px',
  fontSize: 12,
  color: 'var(--nd-text)',
  cursor: 'pointer',
};

const numberInput: React.CSSProperties = {
  width: 88,
  padding: '5px 8px',
  border: '1px solid var(--nd-line)',
  borderRadius: 7,
  fontSize: 12.5,
  ...mono,
  outline: 'none',
};

const note: React.CSSProperties = { fontSize: 12, color: 'var(--nd-muted)', lineHeight: 1.5, margin: 0 };

// A row's open editor, if any. Only one is open at a time: two half-filled
// forms on one screen invite applying the wrong one.
type RowMode = { target: string; kind: 'resize' | 'detach' } | null;

export function DisksPanel({
  vmId,
  status,
  spec,
  onChanged,
  onError,
}: {
  vmId: string;
  // Resizing needs the VM off — qemu-img cannot write to an image QEMU holds
  // open — so the button explains itself rather than failing on click.
  status: VMStatus;
  spec: VMSpec | null;
  onChanged: (next: VMSpec) => void;
  onError?: (fallback: string, err: unknown) => void;
}) {
  const [mode, setMode] = useState<RowMode>(null);
  const [sizeInput, setSizeInput] = useState(10);
  const [adding, setAdding] = useState(false);
  const [busy, setBusy] = useState(false);

  const run = async (fallback: string, op: () => Promise<VMSpec>) => {
    setBusy(true);
    try {
      onChanged(await op());
      setMode(null);
      setAdding(false);
    } catch (e) {
      onError?.(fallback, e);
    } finally {
      setBusy(false);
    }
  };

  const openResize = (disk: VMDisk) => {
    // Prefilled one step up from where it is: the field is for a new size, and
    // starting at the current one only ever produces a refusal.
    setSizeInput(disk.size_gb + 10);
    setMode({ target: disk.target, kind: 'resize' });
  };

  const running = status === 'running';
  const disks = spec?.disks ?? [];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 11 }}>
        <div style={sectionLabel}>Disks</div>
        <button
          onClick={() => {
            setSizeInput(10);
            setAdding(v => !v);
            setMode(null);
          }}
          style={{
            background: 'var(--nd-accent-soft)',
            border: '1px solid var(--nd-accent-line)',
            borderRadius: 8,
            padding: '6px 13px',
            fontSize: 12.5,
            fontWeight: 600,
            color: 'var(--nd-accent)',
            cursor: 'pointer',
          }}
        >
          + Add disk
        </button>
      </div>

      {adding && (
        <div style={{ ...card, padding: '13px 16px', marginBottom: 11 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <span style={{ fontSize: 13 }}>New disk of</span>
            <input
              type="number"
              min={1}
              aria-label="new disk size in GB"
              value={sizeInput}
              onChange={e => setSizeInput(Number(e.target.value))}
              style={numberInput}
            />
            <span style={{ fontSize: 13 }}>GB</span>
            <button
              onClick={() => run('Could not add the disk', () => attachDisk(vmId, sizeInput))}
              disabled={busy || sizeInput < 1}
              style={{
                background: busy || sizeInput < 1 ? 'var(--nd-track)' : 'var(--nd-accent)',
                color: busy || sizeInput < 1 ? 'var(--nd-muted)' : 'var(--nd-on-accent)',
                border: 'none',
                borderRadius: 7,
                padding: '5px 14px',
                fontSize: 12.5,
                fontWeight: 500,
                cursor: busy || sizeInput < 1 ? 'default' : 'pointer',
              }}
            >
              {busy ? 'Creating…' : 'Create'}
            </button>
          </div>
          <p style={{ ...note, marginTop: 9 }}>
            The disk is created empty and attached to this VM. It appears inside the guest at its next boot — as an unformatted block device
            that still needs a filesystem.
          </p>
        </div>
      )}

      <div style={{ ...card, overflow: 'hidden' }}>
        {disks.length === 0 && <div style={{ padding: '14px 16px', fontSize: 13, color: 'var(--nd-muted)' }}>No disks recorded.</div>}

        {disks.map((disk, i) => (
          <div key={disk.target} style={{ borderTop: i === 0 ? 'none' : '1px solid var(--nd-line-soft)', padding: '12px 16px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span style={{ ...mono, fontSize: 13, fontWeight: 600, color: 'var(--nd-text)' }}>{disk.target}</span>
                  <span style={{ fontSize: 13, color: 'var(--nd-text)' }}>{disk.size_gb} GB</span>
                  {disk.boot && (
                    <span
                      style={{
                        fontSize: 10.5,
                        fontWeight: 600,
                        color: 'var(--nd-text-2)',
                        background: 'var(--nd-surface-2)',
                        borderRadius: 5,
                        padding: '1px 6px',
                        letterSpacing: '0.03em',
                      }}
                    >
                      BOOT
                    </span>
                  )}
                </div>
                <div style={{ ...mono, fontSize: 11.5, color: 'var(--nd-muted)', marginTop: 3, overflowWrap: 'anywhere' }}>{disk.path}</div>
              </div>

              <div style={{ display: 'flex', gap: 7, flexShrink: 0 }}>
                <button
                  onClick={() => openResize(disk)}
                  disabled={busy || running}
                  title={running ? 'Stop the VM first — its disks cannot be written to while QEMU has them open.' : undefined}
                  style={{ ...quietButton, opacity: running ? 0.5 : 1, cursor: running ? 'not-allowed' : 'pointer' }}
                >
                  Resize
                </button>
                {/* The boot disk has no detach: its path is derived from the
                    pool and the VM name, so there is nothing to remove short of
                    deleting the VM. */}
                {!disk.boot && (
                  <button onClick={() => setMode({ target: disk.target, kind: 'detach' })} disabled={busy} style={quietButton}>
                    Detach
                  </button>
                )}
              </div>
            </div>

            {mode?.target === disk.target && mode.kind === 'resize' && (
              <div style={{ marginTop: 11, paddingTop: 11, borderTop: '1px dashed var(--nd-line-soft)' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                  <span style={{ fontSize: 13 }}>Grow to</span>
                  <input
                    type="number"
                    min={disk.size_gb + 1}
                    aria-label={`new size for ${disk.target}`}
                    value={sizeInput}
                    onChange={e => setSizeInput(Number(e.target.value))}
                    style={numberInput}
                  />
                  <span style={{ fontSize: 13 }}>GB</span>
                  <button
                    onClick={() => run(`Could not resize ${disk.target}`, () => resizeDisk(vmId, disk.target, sizeInput))}
                    disabled={busy || sizeInput <= disk.size_gb}
                    style={{
                      ...quietButton,
                      background: busy || sizeInput <= disk.size_gb ? 'var(--nd-surface-2)' : 'var(--nd-accent)',
                      color: busy || sizeInput <= disk.size_gb ? 'var(--nd-muted)' : 'var(--nd-on-accent)',
                      border: 'none',
                      padding: '5px 14px',
                      cursor: busy || sizeInput <= disk.size_gb ? 'default' : 'pointer',
                    }}
                  >
                    {busy ? 'Growing…' : 'Grow'}
                  </button>
                  <button onClick={() => setMode(null)} style={{ ...quietButton, border: 'none' }}>
                    Cancel
                  </button>
                </div>
                <p style={{ ...note, marginTop: 9 }}>
                  A disk can only be grown — shrinking one discards whatever the guest wrote past the new end. This grows the disk,{' '}
                  <strong>not the filesystem on it</strong>: the guest will still show the old size until you grow the partition inside it.
                </p>
              </div>
            )}

            {mode?.target === disk.target && mode.kind === 'detach' && (
              <div style={{ marginTop: 11, paddingTop: 11, borderTop: '1px dashed var(--nd-line-soft)' }}>
                <p style={{ ...note, margin: '0 0 10px', color: 'var(--nd-text)' }}>
                  Detach {disk.target} from this VM. It disappears from the guest at its next boot.
                </p>
                <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                  <button
                    onClick={() => run(`Could not detach ${disk.target}`, () => detachDisk(vmId, disk.target, false))}
                    disabled={busy}
                    style={{ ...quietButton, padding: '5px 14px' }}
                  >
                    Detach, keep the file
                  </button>
                  <button
                    onClick={() => run(`Could not detach ${disk.target}`, () => detachDisk(vmId, disk.target, true))}
                    disabled={busy}
                    style={{
                      ...quietButton,
                      padding: '5px 14px',
                      color: 'var(--nd-danger)',
                      borderColor: 'var(--nd-danger-line)',
                      background: 'var(--nd-danger-bg)',
                    }}
                  >
                    Detach and delete the file
                  </button>
                  <button onClick={() => setMode(null)} style={{ ...quietButton, border: 'none' }}>
                    Cancel
                  </button>
                </div>
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
