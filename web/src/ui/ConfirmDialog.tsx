import { useCallback, useRef, useState, type ReactNode } from 'react';
import { Button } from './Button';
import { Dialog, DialogActions, DialogBody } from './Dialog';

export interface ConfirmOptions {
  title: ReactNode;
  body?: ReactNode;
  // confirmLabel names the action ("Delete snapshot"), never "OK".
  confirmLabel: string;
  cancelLabel?: string;
  tone?: 'danger' | 'primary';
}

interface ConfirmDialogProps extends ConfirmOptions {
  onConfirm: () => void;
  onCancel: () => void;
}

// ConfirmDialog asks before an action. A destructive one opens with focus on
// Cancel, so a reflexive Enter does not delete anything.
export function ConfirmDialog({
  title,
  body,
  confirmLabel,
  cancelLabel = 'Cancel',
  tone = 'danger',
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  const cancel = useRef<HTMLButtonElement>(null);
  const confirm = useRef<HTMLButtonElement>(null);
  return (
    <Dialog title={title} onClose={onCancel} initialFocus={tone === 'danger' ? cancel : confirm} width={460}>
      {body && <DialogBody>{body}</DialogBody>}
      <DialogActions>
        <Button ref={cancel} onClick={onCancel}>
          {cancelLabel}
        </Button>
        <Button ref={confirm} variant={tone} onClick={onConfirm}>
          {confirmLabel}
        </Button>
      </DialogActions>
    </Dialog>
  );
}

// useConfirm is the in-app replacement for window.confirm: await confirm()
// for a yes or no, and render the element it returns somewhere in the tree.
export function useConfirm(): [(opts: ConfirmOptions) => Promise<boolean>, ReactNode] {
  const [pending, setPending] = useState<{ opts: ConfirmOptions; resolve: (ok: boolean) => void } | null>(null);

  const ask = useCallback(
    (opts: ConfirmOptions) =>
      new Promise<boolean>(resolve => {
        setPending({ opts, resolve });
      }),
    [],
  );

  const settle = (ok: boolean) => {
    pending?.resolve(ok);
    setPending(null);
  };

  const element = pending ? <ConfirmDialog {...pending.opts} onConfirm={() => settle(true)} onCancel={() => settle(false)} /> : null;
  return [ask, element];
}
