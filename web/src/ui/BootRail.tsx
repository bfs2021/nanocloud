import type { VM } from '../types';

// The boot rail: Image → VM → Network → Install → Ready, filling in as a VM
// comes up. Derived only from GET /vms: an address means the network is up,
// and phase "ready" means the backend confirmed the VM is usable.

export type StepState = 'done' | 'active' | 'todo' | 'failed';

export const RAIL_STEPS = ['Image', 'VM', 'Network', 'Install', 'Ready'] as const;

// railSteps says how far a VM has come, or null when it has no boot to show
// (stopped, stopping, or in a state the rail would only misdescribe).
export function railSteps(vm: Pick<VM, 'status' | 'phase' | 'ip'>): StepState[] | null {
  if (vm.status !== 'running') return null;
  switch (vm.phase) {
    case 'ready':
      return ['done', 'done', 'done', 'done', 'done'];
    case 'booting':
      return vm.ip ? ['done', 'done', 'done', 'active', 'todo'] : ['done', 'done', 'active', 'todo', 'todo'];
    case 'no-network':
      return ['done', 'done', 'failed', 'todo', 'todo'];
    default:
      return null;
  }
}

const SEGMENT: Record<StepState, { background: string; animation: string }> = {
  done: { background: 'var(--nd-ready)', animation: 'none' },
  active: { background: 'var(--nd-aurora)', animation: 'ndshimmer 1.6s linear infinite' },
  failed: { background: 'var(--nd-alert)', animation: 'none' },
  todo: { background: 'var(--nd-track)', animation: 'none' },
};

const LABEL_COLOR: Record<StepState, string> = {
  done: 'var(--nd-muted)',
  active: 'var(--nd-text)',
  failed: 'var(--nd-alert)',
  todo: 'var(--nd-muted)',
};

function describe(steps: StepState[]): string {
  const i = steps.findIndex(s => s === 'active' || s === 'failed');
  if (i < 0) return 'Ready';
  return `${RAIL_STEPS[i]}: ${steps[i] === 'failed' ? 'failed' : 'in progress'}`;
}

export function BootRail({ steps, labels = true }: { steps: StepState[]; labels?: boolean }) {
  const done = steps.filter(s => s === 'done').length;
  return (
    <div
      role="progressbar"
      aria-label="Boot progress"
      aria-valuemin={0}
      aria-valuemax={steps.length}
      aria-valuenow={done}
      aria-valuetext={describe(steps)}
    >
      <div style={{ display: 'flex', gap: 4 }}>
        {steps.map((s, i) => (
          <div
            key={RAIL_STEPS[i]}
            style={{
              flex: 1,
              height: 6,
              borderRadius: 3,
              background: SEGMENT[s].background,
              backgroundSize: '200% 100%',
              animation: SEGMENT[s].animation,
            }}
          />
        ))}
      </div>
      {labels && (
        <div aria-hidden="true" style={{ display: 'flex', gap: 4, marginTop: 6 }}>
          {steps.map((s, i) => (
            <span
              key={RAIL_STEPS[i]}
              style={{
                flex: 1,
                fontFamily: 'var(--nd-font-mono)',
                fontSize: 11,
                color: LABEL_COLOR[s],
              }}
            >
              {RAIL_STEPS[i]}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}
