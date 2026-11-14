import { useId, type ReactNode } from 'react';
import { RemedySnippet } from '../RemedySnippet';
import type { Availability } from './firmware';
import './create-vm.css';

// ReasonNote is the "off, and here is why" box. Shared by Toggle and the
// network picker so a control the host constrains explains itself the same way
// wherever it appears.
export function ReasonNote({ availability, indent = 0 }: { availability: Availability; indent?: number }) {
  if (availability.available || !availability.reason) return null;
  return (
    <div className="cv-reason" style={{ marginLeft: indent }}>
      {availability.reason}
      {availability.remedy && <RemedySnippet command={availability.remedy} />}
    </div>
  );
}

// Toggle is a labelled checkbox that states why it is off when it cannot be
// turned on. The reason sits under the label rather than in a title attribute:
// a tooltip on a disabled control is unreachable on touch and invisible to
// anyone who does not hover it.
export function Toggle({
  label,
  hint,
  checked,
  onChange,
  availability,
}: {
  label: string;
  hint: string;
  checked: boolean;
  onChange: (v: boolean) => void;
  availability: Availability;
}) {
  const disabled = !availability.available;

  return (
    <div className="cv-toggle" data-disabled={disabled || undefined}>
      <label>
        <input type="checkbox" checked={checked} disabled={disabled} onChange={e => onChange(e.target.checked)} />
        <span>
          <span className="cv-toggle-label">{label}</span>
          <span className="cv-toggle-hint">{hint}</span>
        </span>
      </label>
      <ReasonNote availability={availability} indent={26} />
    </div>
  );
}

// Disclosure hides an advanced section behind a header that says what is in it
// and whether anything inside has been changed. The summary stays visible while
// collapsed, so a customised section never looks untouched.
export function Disclosure({
  title,
  summary,
  open,
  onToggle,
  children,
}: {
  title: string;
  summary?: string;
  open: boolean;
  onToggle: () => void;
  children: ReactNode;
}) {
  const panelId = useId();
  return (
    <section className="cv-disclosure">
      <button type="button" className="cv-disclosure-head" aria-expanded={open} aria-controls={panelId} onClick={onToggle}>
        <span className="cv-chevron" aria-hidden="true" />
        {title}
        {summary && <span className="cv-disclosure-summary">{summary}</span>}
      </button>
      {open && (
        <div id={panelId} className="cv-disclosure-body">
          {children}
        </div>
      )}
    </section>
  );
}
