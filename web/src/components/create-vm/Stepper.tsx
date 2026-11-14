import { STEPS, stepIndex, type StepId } from './state';
import './create-vm.css';

// Stepper shows where the person is in the flow. Steps already passed are
// buttons, because going back to one is always safe; steps ahead are plain
// text, because reaching them is Continue's job, which is what checks the step
// before it.
export function Stepper({ current, locked, onGo }: { current: StepId; locked: boolean; onGo: (s: StepId) => void }) {
  const at = stepIndex(current);
  return (
    <nav className="cv-stepper" aria-label="Progress">
      <ol>
        {STEPS.map((s, i) => {
          const state = i < at ? 'done' : i === at ? 'active' : 'future';
          const dot = (
            <span className="cv-step-dot" aria-hidden="true">
              {state === 'done' ? '✓' : i + 1}
            </span>
          );
          return (
            <li key={s.id} data-state={state} aria-current={state === 'active' ? 'step' : undefined}>
              {state === 'done' ? (
                <button type="button" className="cv-step-link" disabled={locked} onClick={() => onGo(s.id)}>
                  {dot}
                  <span className="cv-step-label">{s.label}</span>
                </button>
              ) : (
                <span className="cv-step-link">
                  {dot}
                  <span className="cv-step-label">{s.label}</span>
                </span>
              )}
            </li>
          );
        })}
      </ol>
      <p className="cv-step-count">
        Step {at + 1} of {STEPS.length} · {STEPS[at].label}
      </p>
    </nav>
  );
}
