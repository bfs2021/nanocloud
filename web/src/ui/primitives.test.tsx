import { describe, expect, it, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { Button } from './Button';
import { ConfirmDialog, useConfirm } from './ConfirmDialog';
import { Dialog } from './Dialog';
import { Field, Input } from './Field';
import { Tabs } from './Tabs';

describe('Button', () => {
  it('is a plain button by default, and busy blocks a second click', async () => {
    const onClick = vi.fn();
    const { rerender } = render(<Button onClick={onClick}>Save</Button>);
    const btn = screen.getByRole('button', { name: 'Save' });
    expect(btn).toHaveAttribute('type', 'button');
    await userEvent.click(btn);
    expect(onClick).toHaveBeenCalledTimes(1);

    rerender(
      <Button onClick={onClick} busy>
        Save
      </Button>,
    );
    expect(btn).toBeDisabled();
    expect(btn).toHaveAttribute('aria-busy', 'true');
  });
});

describe('Dialog', () => {
  function Harness({ locked = false, onClose = () => {} }) {
    return (
      <>
        <button>Opener</button>
        <Dialog title="Rename" onClose={onClose} locked={locked}>
          <input aria-label="Name" />
          <button>Save</button>
        </Dialog>
      </>
    );
  }

  it('is a labelled modal that takes focus on open', () => {
    render(<Harness />);
    expect(screen.getByRole('dialog', { name: 'Rename' })).toHaveAttribute('aria-modal', 'true');
    expect(screen.getByLabelText('Name')).toHaveFocus();
  });

  it('keeps Tab inside itself in both directions', async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.tab();
    expect(screen.getByRole('button', { name: 'Save' })).toHaveFocus();
    await user.tab();
    expect(screen.getByLabelText('Name')).toHaveFocus();
    await user.tab({ shift: true });
    expect(screen.getByRole('button', { name: 'Save' })).toHaveFocus();
  });

  it('closes on Escape unless locked', async () => {
    const onClose = vi.fn();
    const { rerender } = render(<Harness onClose={onClose} locked />);
    await userEvent.keyboard('{Escape}');
    expect(onClose).not.toHaveBeenCalled();
    rerender(<Harness onClose={onClose} />);
    await userEvent.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('hands focus back to the opener when it closes', async () => {
    function Toggle() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button onClick={() => setOpen(true)}>Open</button>
          {open && (
            <Dialog title="T" onClose={() => setOpen(false)}>
              <button>Inside</button>
            </Dialog>
          )}
        </>
      );
    }
    const user = userEvent.setup();
    render(<Toggle />);
    await user.click(screen.getByRole('button', { name: 'Open' }));
    expect(screen.getByRole('button', { name: 'Inside' })).toHaveFocus();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Open' })).toHaveFocus();
  });
});

describe('ConfirmDialog', () => {
  it('starts on Cancel for a destructive action', () => {
    render(<ConfirmDialog title="Delete web-1?" confirmLabel="Delete VM" onConfirm={() => {}} onCancel={() => {}} />);
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus();
  });

  it('useConfirm resolves with the answer and goes away', async () => {
    let answer: Promise<boolean> | undefined;
    function Asker() {
      const [confirm, dialog] = useConfirm();
      return (
        <>
          <button onClick={() => (answer = confirm({ title: 'Delete?', confirmLabel: 'Delete' }))}>Ask</button>
          {dialog}
        </>
      );
    }
    const user = userEvent.setup();
    render(<Asker />);
    await user.click(screen.getByRole('button', { name: 'Ask' }));
    await user.click(screen.getByRole('button', { name: 'Delete' }));
    await act(async () => expect(await answer).toBe(true));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Ask' }));
    await user.keyboard('{Escape}');
    await act(async () => expect(await answer).toBe(false));
  });
});

describe('Field', () => {
  it('labels its control and ties the error to it', () => {
    render(
      <Field label="Name" hint="Lowercase only" error="Name is taken">
        <Input defaultValue="web" />
      </Field>,
    );
    const input = screen.getByLabelText('Name');
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input).toHaveAccessibleDescription('Name is taken');
  });
});

describe('Tabs', () => {
  function Harness() {
    const [tab, setTab] = useState<'a' | 'b' | 'c'>('a');
    return (
      <Tabs
        label="Sections"
        value={tab}
        onChange={setTab}
        items={[
          { id: 'a', label: 'Alpha' },
          { id: 'b', label: 'Beta' },
          { id: 'c', label: 'Gamma' },
        ]}
      >
        Panel {tab}
      </Tabs>
    );
  }

  it('moves with the arrow keys, wraps, and labels the panel by its tab', async () => {
    const user = userEvent.setup();
    render(<Harness />);
    expect(screen.getByRole('tabpanel', { name: 'Alpha' })).toHaveTextContent('Panel a');
    await user.click(screen.getByRole('tab', { name: 'Alpha' }));
    await user.keyboard('{ArrowLeft}');
    expect(screen.getByRole('tab', { name: 'Gamma' })).toHaveFocus();
    expect(screen.getByRole('tab', { name: 'Gamma' })).toHaveAttribute('aria-selected', 'true');
    await user.keyboard('{Home}');
    expect(screen.getByRole('tabpanel', { name: 'Alpha' })).toBeInTheDocument();
    expect(screen.getAllByRole('tab').filter(t => t.tabIndex === 0)).toHaveLength(1);
  });
});
