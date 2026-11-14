import {
  createContext,
  forwardRef,
  useContext,
  useId,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from 'react';

interface FieldIds {
  id: string;
  'aria-describedby'?: string;
  'aria-invalid'?: true;
}

const FieldContext = createContext<FieldIds | null>(null);

interface FieldProps {
  label: ReactNode;
  hint?: ReactNode;
  // error replaces the hint and marks the control invalid.
  error?: ReactNode;
  children: ReactNode;
}

// Field labels the Input, Select or Textarea inside it and ties its hint or
// error to it, so a screen reader announces both with the control.
export function Field({ label, hint, error, children }: FieldProps) {
  const id = useId();
  const noteId = `${id}-note`;
  const note = error || hint;
  const ids: FieldIds = {
    id,
    'aria-describedby': note ? noteId : undefined,
    'aria-invalid': error ? true : undefined,
  };
  return (
    <div className="nd-field">
      <label className="nd-field-label" htmlFor={id}>
        {label}
      </label>
      <FieldContext.Provider value={ids}>{children}</FieldContext.Provider>
      {note && (
        <div id={noteId} className={error ? 'nd-field-error' : 'nd-field-hint'} role={error ? 'alert' : undefined}>
          {note}
        </div>
      )}
    </div>
  );
}

function useFieldIds() {
  return useContext(FieldContext) ?? {};
}

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function Input({ className, ...rest }, ref) {
  return <input ref={ref} className={['nd-input', className].filter(Boolean).join(' ')} {...useFieldIds()} {...rest} />;
});

export const Select = forwardRef<HTMLSelectElement, SelectHTMLAttributes<HTMLSelectElement>>(function Select({ className, ...rest }, ref) {
  return <select ref={ref} className={['nd-input', className].filter(Boolean).join(' ')} {...useFieldIds()} {...rest} />;
});

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(function Textarea(
  { className, ...rest },
  ref,
) {
  return <textarea ref={ref} className={['nd-input', className].filter(Boolean).join(' ')} {...useFieldIds()} {...rest} />;
});
