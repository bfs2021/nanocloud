import { forwardRef, type ButtonHTMLAttributes } from 'react';
import { Spinner } from './Spinner';

export type ButtonVariant = 'primary' | 'secondary' | 'quiet' | 'danger';

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: 'md' | 'sm';
  // busy disables the button and shows a spinner beside its label, so a
  // second click cannot send the request twice.
  busy?: boolean;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = 'secondary', size = 'md', busy = false, disabled, className, type = 'button', children, ...rest },
  ref,
) {
  const classes = ['nd-btn', `nd-btn-${variant}`, size === 'sm' && 'nd-btn-sm', className].filter(Boolean).join(' ');
  return (
    <button ref={ref} type={type} className={classes} disabled={disabled || busy} aria-busy={busy || undefined} {...rest}>
      {busy && <Spinner size={14} track="transparent" head="currentColor" shrink />}
      {children}
    </button>
  );
});
