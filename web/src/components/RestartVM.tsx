import { useEffect, useRef, useState } from 'react';
import { startVM, stopVM } from '../api';

interface RestartVMProps {
  vm: string;
}

type Phase = 'idle' | 'confirm' | 'restarting' | 'done';

const small: React.CSSProperties = {
  minHeight: 30,
  padding: '0 12px',
  borderRadius: 8,
  fontSize: 12.5,
  fontWeight: 500,
  cursor: 'pointer',
  border: '1px solid var(--nd-line)',
  background: 'var(--nd-surface)',
  color: 'var(--nd-text)',
};

const danger: React.CSSProperties = {
  ...small,
  border: '1px solid var(--nd-danger-line)',
  background: 'var(--nd-danger-bg)',
  color: 'var(--nd-danger)',
};

// RestartVM power-cycles a machine that is up but not answering: a forced
// power-off, then a start. Pulling the power can lose what the guest had not
// written to disk, so it asks first, in place, and a polite shutdown is no use
// here — a guest that does not answer SSH may not answer ACPI either.
export function RestartVM({ vm }: RestartVMProps) {
  const [phase, setPhase] = useState<Phase>('idle');
  const [error, setError] = useState<string | null>(null);
  const cancel = useRef<HTMLButtonElement>(null);

  // The safe choice is the one under the keyboard when the question appears.
  useEffect(() => {
    if (phase === 'confirm') cancel.current?.focus();
  }, [phase]);

  async function restart() {
    setPhase('restarting');
    setError(null);
    let off = false;
    try {
      await stopVM(vm, true);
      off = true;
      await startVM(vm);
      setPhase('done');
    } catch (e) {
      const why = e instanceof Error ? e.message : String(e);
      setError(off ? `${vm} was powered off, but starting it again failed: ${why}` : `Could not power ${vm} off: ${why}`);
      setPhase('idle');
    }
  }

  return (
    <div
      onKeyDown={e => {
        if (e.key === 'Escape' && phase === 'confirm') setPhase('idle');
      }}
      style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8 }}
    >
      {phase === 'idle' && (
        <button type="button" onClick={() => setPhase('confirm')} style={small}>
          Restart VM
        </button>
      )}
      {phase === 'confirm' && (
        <>
          <span id={`restart-${vm}`} style={{ fontSize: 12.5, color: 'var(--nd-text-2)' }}>
            Pull the power on {vm} and start it again?
          </span>
          <button type="button" onClick={restart} aria-describedby={`restart-${vm}`} style={danger}>
            Power off and restart
          </button>
          <button type="button" ref={cancel} onClick={() => setPhase('idle')} style={small}>
            Cancel
          </button>
        </>
      )}
      {phase === 'restarting' && (
        <span role="status" style={{ display: 'inline-flex', alignItems: 'center', gap: 8, fontSize: 12.5, color: 'var(--nd-warn)' }}>
          <span
            aria-hidden="true"
            style={{
              width: 7,
              height: 7,
              borderRadius: '50%',
              background: 'var(--nd-warn)',
              animation: 'ncpulse 1.6s ease-in-out infinite',
            }}
          />
          Restarting…
        </span>
      )}
      {phase === 'done' && (
        <span role="status" style={{ fontSize: 12.5, color: 'var(--nd-ready)' }}>
          Restarted. Checking whether it answers…
        </span>
      )}
      {error && (
        <span role="alert" style={{ fontSize: 12.5, color: 'var(--nd-danger)', overflowWrap: 'anywhere' }}>
          {error}
        </span>
      )}
    </div>
  );
}
