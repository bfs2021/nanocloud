import { useEffect, useId, useRef, useState } from 'react';
import type { Service, VM } from '../types';

interface ManagedDeleteDialogProps {
  vm: VM;
  // The service the VM belongs to; absent when the list has not caught up.
  service?: Service;
  onOpenService: () => void;
  onDeleteVM: () => void;
  onClose: () => void;
}

// ManagedDeleteDialog stands between a person and deleting one of a
// service's VMs on its own, which leaves the service expecting a machine that
// never comes back. It points at the service first; deleting the VM anyway
// takes a deliberate tick.
export function ManagedDeleteDialog({ vm, service, onOpenService, onDeleteVM, onClose }: ManagedDeleteDialogProps) {
  const [ack, setAck] = useState(false);
  const titleId = useId();
  const first = useRef<HTMLButtonElement>(null);
  const name = vm.service!;
  const role = service?.members?.find(m => m.vm === vm.name)?.role_label;
  const many = (service?.members?.length ?? 1) > 1;
  const what = service ? ` (${service.recipe_name})` : '';

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    first.current?.focus();
    return () => previous?.focus?.();
  }, []);

  return (
    <div
      onMouseDown={e => {
        if (e.target === e.currentTarget) onClose();
      }}
      style={{
        position: 'fixed',
        inset: 0,
        zIndex: 50,
        background: 'var(--nd-scrim)',
        display: 'flex',
        alignItems: 'flex-start',
        justifyContent: 'center',
        padding: '12vh 16px 16px',
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onKeyDown={e => {
          if (e.key === 'Escape') onClose();
        }}
        style={{
          width: 'min(540px, 100%)',
          background: 'var(--nd-bg)',
          border: '1px solid var(--nd-line)',
          borderRadius: 16,
          boxShadow: 'var(--nd-shadow-lg)',
          padding: 22,
          display: 'flex',
          flexDirection: 'column',
          gap: 14,
          animation: 'ncfadein 0.15s ease',
        }}
      >
        <h2 id={titleId} style={{ margin: 0, fontSize: 18, fontWeight: 650, letterSpacing: '-0.015em' }}>
          Delete {vm.name}?
        </h2>
        <p style={{ margin: 0, fontSize: 14, lineHeight: 1.55, color: 'var(--nd-text-2)' }}>
          {many && role ? `This VM is a ${role.toLowerCase()} of ` : 'This VM runs '}
          <b style={{ fontWeight: 600, color: 'var(--nd-text)' }}>{name}</b>
          {what}. Deleted on its own, it leaves {name} expecting a machine that never comes back.
        </p>

        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: 10,
            padding: 14,
            borderRadius: 12,
            border: '1px solid var(--nd-accent-line)',
            background: 'var(--nd-accent-soft)',
          }}
        >
          <span style={{ fontSize: 13.5, lineHeight: 1.5 }}>
            <b style={{ fontWeight: 600 }}>Go to {name}</b> to delete the whole service with its VMs, or to see what is wrong with this one.
          </span>
          <button
            ref={first}
            onClick={onOpenService}
            style={{
              alignSelf: 'flex-start',
              minHeight: 40,
              padding: '0 16px',
              borderRadius: 9,
              border: 'none',
              background: 'var(--nd-accent)',
              color: 'var(--nd-on-accent)',
              fontSize: 13.5,
              fontWeight: 600,
              cursor: 'pointer',
            }}
          >
            Open {name}
          </button>
        </div>

        <div
          style={{ display: 'flex', flexDirection: 'column', gap: 10, padding: 14, borderRadius: 12, border: '1px solid var(--nd-line)' }}
        >
          <label style={{ display: 'flex', alignItems: 'flex-start', gap: 10, fontSize: 13.5, cursor: 'pointer' }}>
            <input
              type="checkbox"
              checked={ack}
              onChange={e => setAck(e.target.checked)}
              style={{ width: 18, height: 18, margin: '1px 0 0', accentColor: 'var(--nd-danger)' }}
            />
            Delete only this VM. I understand {name} loses it.
          </label>
          <button
            onClick={onDeleteVM}
            disabled={!ack}
            style={{
              alignSelf: 'flex-start',
              minHeight: 40,
              padding: '0 16px',
              borderRadius: 9,
              border: '1px solid var(--nd-danger-line)',
              background: 'var(--nd-danger-bg)',
              color: 'var(--nd-danger)',
              fontSize: 13.5,
              fontWeight: 600,
              cursor: ack ? 'pointer' : 'not-allowed',
              opacity: ack ? 1 : 0.55,
            }}
          >
            Delete VM anyway
          </button>
        </div>

        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <button
            onClick={onClose}
            style={{
              minHeight: 40,
              padding: '0 16px',
              borderRadius: 9,
              border: '1px solid var(--nd-line)',
              background: 'var(--nd-surface)',
              color: 'var(--nd-text)',
              fontSize: 13.5,
              cursor: 'pointer',
            }}
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}
