import type { VM } from '../../types';
import { Button } from '../../ui/Button';

interface VMActionsProps {
  vm: VM;
  // A graceful shutdown is outstanding.
  stopping: boolean;
  canForceStop?: boolean;
  onStart: (id: string) => void;
  onStop: (id: string) => void;
  onForceStop?: (id: string) => void;
  onDelete?: (id: string) => void;
  onClone: () => void;
  onSaveTemplate: () => void;
}

// Copying is never the thing you came to this page to do, so Clone and Save
// as template stay secondary beside the power action that is.
export function VMActions({ vm, stopping, canForceStop, onStart, onStop, onForceStop, onDelete, onClone, onSaveTemplate }: VMActionsProps) {
  const running = vm.status === 'running';
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
      {onDelete && (
        <Button variant="quiet" onClick={() => onDelete(vm.id)} aria-label={`Delete ${vm.name}`} style={{ color: 'var(--nd-danger)' }}>
          Delete
        </Button>
      )}
      <Button onClick={onSaveTemplate} title="Keep this VM's configuration so the wizard can start from it">
        Save as template
      </Button>
      <Button
        onClick={onClone}
        // Copying a disk QEMU has open cannot produce a consistent image,
        // so the button says why it is off rather than failing at the request.
        title={running ? `Stop ${vm.id} first — its disks cannot be copied while it is running` : 'Make a full copy of this VM'}
        disabled={running}
      >
        Clone…
      </Button>
      {running && canForceStop && onForceStop ? (
        <Button
          variant="danger"
          onClick={() => onForceStop(vm.id)}
          title="The guest has not responded to the shutdown request — force it off"
        >
          Force off
        </Button>
      ) : running ? (
        <Button onClick={() => onStop(vm.id)} disabled={stopping}>
          <span
            style={{
              width: 9,
              height: 9,
              background: stopping ? 'var(--nd-muted)' : 'var(--nd-text-2)',
              borderRadius: 2,
              display: 'inline-block',
            }}
          />
          {stopping ? 'Stopping…' : 'Stop'}
        </Button>
      ) : (
        <Button variant="primary" onClick={() => onStart(vm.id)}>
          <span
            style={{
              borderStyle: 'solid',
              borderWidth: '5px 0 5px 9px',
              borderColor: 'transparent transparent transparent var(--nd-on-accent)',
              display: 'inline-block',
            }}
          />
          Start
        </Button>
      )}
    </div>
  );
}
