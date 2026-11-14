import { useEffect, useState } from 'react';
import type { VM, VMSpec } from '../../types';
import { getVMSpec } from '../../api';
import { Card } from '../../ui/Card';
import { DisksPanel } from '../DisksPanel';
import { SnapshotsPanel } from './SnapshotsPanel';

interface StorageTabProps {
  vm: VM;
  snapshotsSupported: boolean;
  onError?: (fallback: string, err: unknown) => void;
  onRefreshVMs: () => void;
}

export function StorageTab({ vm, snapshotsSupported, onError, onRefreshVMs }: StorageTabProps) {
  const [spec, setSpec] = useState<VMSpec | null>(null);

  useEffect(() => {
    // Swallowed on failure: a VM defined by hand with virsh has no spec, and
    // that is not a reason to hide its snapshots.
    getVMSpec(vm.id)
      .then(setSpec)
      .catch(() => setSpec(null));
  }, [vm.id]);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
      <Card as="div" style={{ padding: '14px 20px', display: 'flex', alignItems: 'center', gap: 12 }}>
        <div
          style={{
            width: 36,
            height: 36,
            borderRadius: 10,
            background: 'var(--nd-surface-2)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            color: 'var(--nd-text-2)',
            flexShrink: 0,
          }}
        >
          <svg width="17" height="17" viewBox="0 0 16 16" fill="none" aria-hidden="true">
            <ellipse cx="8" cy="3.4" rx="5.7" ry="2" stroke="currentColor" strokeWidth="1.4" />
            <path d="M2.3 3.4v4.4c0 1.1 2.55 2 5.7 2s5.7-.9 5.7-2V3.4" stroke="currentColor" strokeWidth="1.4" />
            <path d="M2.3 7.8v4.4c0 1.1 2.55 2 5.7 2s5.7-.9 5.7-2V7.8" stroke="currentColor" strokeWidth="1.4" />
          </svg>
        </div>
        <div>
          <div style={{ fontSize: 13.5, fontWeight: 600 }}>Disk pool: {vm.pool_name || '—'}</div>
          <div style={{ fontSize: 11.5, color: 'var(--nd-muted)', marginTop: 2, fontFamily: 'var(--nd-font-mono)' }}>
            {vm.pool_path || '—'}
          </div>
        </div>
      </Card>

      <DisksPanel vmId={vm.id} status={vm.status} spec={spec} onChanged={setSpec} onError={onError} />

      <SnapshotsPanel vmId={vm.id} enabled={snapshotsSupported} onError={onError} onRefreshVMs={onRefreshVMs} />
    </div>
  );
}
