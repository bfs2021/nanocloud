import { useEffect, useId, useRef, type FormEvent, type KeyboardEvent, type ReactNode, type RefObject } from 'react';

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

interface DialogProps {
  title: ReactNode;
  onClose: () => void;
  children: ReactNode;
  // locked holds the dialog open while its action runs: Escape and a click
  // on the backdrop do nothing, so the result has somewhere to land.
  locked?: boolean;
  // initialFocus names what takes focus on open; otherwise the first
  // focusable element does.
  initialFocus?: RefObject<HTMLElement | null>;
  // onSubmit makes the dialog a form, so Enter in a field submits it.
  onSubmit?: (e: FormEvent<HTMLFormElement>) => void;
  width?: number;
}

// Dialog is a modal: it takes focus when it opens, keeps Tab inside itself,
// and hands focus back to whatever had it when it closes.
export function Dialog({ title, onClose, children, locked = false, initialFocus, onSubmit, width = 540 }: DialogProps) {
  const titleId = useId();
  const panel = useRef<HTMLElement | null>(null);

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const first = initialFocus?.current ?? panel.current?.querySelector<HTMLElement>(FOCUSABLE) ?? panel.current;
    first?.focus();
    return () => previous?.focus?.();
    // Focus moves once, on open; a re-render must not pull it back.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function onKeyDown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.stopPropagation();
      if (!locked) onClose();
      return;
    }
    if (e.key !== 'Tab' || !panel.current) return;
    const items = Array.from(panel.current.querySelectorAll<HTMLElement>(FOCUSABLE));
    if (items.length === 0) {
      e.preventDefault();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement;
    if (e.shiftKey && (active === first || active === panel.current)) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && active === last) {
      e.preventDefault();
      first.focus();
    }
  }

  const common = {
    role: 'dialog',
    'aria-modal': true,
    'aria-labelledby': titleId,
    tabIndex: -1,
    className: 'nd-dialog',
    style: { width: `min(${width}px, 100%)` },
    onKeyDown,
  } as const;
  const content = (
    <>
      <h2 id={titleId} className="nd-dialog-title">
        {title}
      </h2>
      {children}
    </>
  );

  return (
    <div
      className="nd-scrim"
      onMouseDown={e => {
        if (e.target === e.currentTarget && !locked) onClose();
      }}
    >
      {onSubmit ? (
        <form ref={el => (panel.current = el)} onSubmit={onSubmit} {...common}>
          {content}
        </form>
      ) : (
        <div ref={el => (panel.current = el)} {...common}>
          {content}
        </div>
      )}
    </div>
  );
}

export function DialogBody({ children }: { children: ReactNode }) {
  return <div className="nd-dialog-body">{children}</div>;
}

export function DialogActions({ children }: { children: ReactNode }) {
  return <div className="nd-dialog-actions">{children}</div>;
}
