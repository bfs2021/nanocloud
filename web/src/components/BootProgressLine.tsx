import { getStatus } from '../design';
import type { VMPhase } from '../types';

// BootProgressLine is the one line under a booting VM's rail saying what it is
// waiting for (see bootProgress in design.ts), in the colour of its badge: a
// wait that has gone wrong (no network) reads as a warning.
export function BootProgressLine({ phase, text }: { phase: VMPhase; text: string }) {
  const s = getStatus(phase === 'no-network' ? 'no-network' : 'booting');
  return (
    <div
      role="status"
      style={{
        fontFamily: 'var(--nd-font-mono)',
        fontSize: 12.5,
        color: s.color,
      }}
    >
      {text}
    </div>
  );
}
