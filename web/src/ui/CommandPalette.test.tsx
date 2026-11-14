import { describe, it, expect, vi } from 'vitest';
import { useState } from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { CommandPalette, isPaletteShortcut, matchCommands, type Command } from './CommandPalette';

function commands(): Command[] {
  return [
    { id: 'new', label: 'New VM', run: vi.fn() },
    { id: 'images', label: 'Go to Images', run: vi.fn() },
    { id: 'stop-web', label: 'Stop web-prod-01', run: vi.fn() },
    { id: 'ssh-web', label: 'Copy SSH command for web-prod-01', hint: 'ssh admin@10.0.0.5', run: vi.fn() },
  ];
}

describe('matchCommands', () => {
  it('returns everything for an empty query', () => {
    expect(matchCommands(commands(), '  ')).toHaveLength(4);
  });

  it('needs every word, in any order, in the label or hint', () => {
    expect(matchCommands(commands(), 'web st').map(c => c.id)).toEqual(['stop-web']);
    expect(matchCommands(commands(), '10.0.0.5').map(c => c.id)).toEqual(['ssh-web']);
    expect(matchCommands(commands(), 'IMAGES go').map(c => c.id)).toEqual(['images']);
    expect(matchCommands(commands(), 'nothing like this')).toEqual([]);
  });
});

describe('isPaletteShortcut', () => {
  function press(init: KeyboardEventInit, target: Element = document.body) {
    const e = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init });
    let result = false;
    const listen = (ev: Event) => (result = isPaletteShortcut(ev as KeyboardEvent));
    window.addEventListener('keydown', listen);
    target.dispatchEvent(e);
    window.removeEventListener('keydown', listen);
    return result;
  }

  it('takes ⌘K and Ctrl+K', () => {
    expect(press({ key: 'k', metaKey: true })).toBe(true);
    expect(press({ key: 'K', ctrlKey: true })).toBe(true);
  });

  it('ignores K alone and other shortcuts', () => {
    expect(press({ key: 'k' })).toBe(false);
    expect(press({ key: 'j', ctrlKey: true })).toBe(false);
  });

  it('leaves Ctrl+K to a VM console', () => {
    const vmConsole = document.createElement('div');
    vmConsole.setAttribute('data-console', '');
    const canvas = document.createElement('canvas');
    vmConsole.appendChild(canvas);
    document.body.appendChild(vmConsole);
    try {
      expect(press({ key: 'k', ctrlKey: true }, canvas)).toBe(false);
    } finally {
      vmConsole.remove();
    }
  });
});

// Harness owns the open state the way App does, with a button to open it from
// so focus has somewhere to go back to.
function Harness({ cmds, onClose }: { cmds: Command[]; onClose?: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button onClick={() => setOpen(true)}>Open palette</button>
      <CommandPalette
        open={open}
        commands={cmds}
        onClose={() => {
          setOpen(false);
          onClose?.();
        }}
      />
    </>
  );
}

async function openPalette() {
  const user = userEvent.setup();
  await user.click(screen.getByRole('button', { name: 'Open palette' }));
  return user;
}

describe('CommandPalette', () => {
  it('renders nothing while closed', () => {
    render(<CommandPalette open={false} commands={commands()} onClose={vi.fn()} />);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('focuses the input and lists every command when it opens', async () => {
    render(<Harness cmds={commands()} />);
    await openPalette();
    expect(screen.getByRole('combobox', { name: 'Command' })).toHaveFocus();
    expect(screen.getAllByRole('option')).toHaveLength(4);
    expect(screen.getAllByRole('option')[0]).toHaveAttribute('aria-selected', 'true');
  });

  it('filters as you type and runs the highlighted match on Enter', async () => {
    const cmds = commands();
    const onClose = vi.fn();
    render(<Harness cmds={cmds} onClose={onClose} />);
    const user = await openPalette();

    await user.keyboard('web st');
    expect(screen.getAllByRole('option')).toHaveLength(1);
    await user.keyboard('{Enter}');

    expect(cmds[2].run).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('moves the highlight with the arrow keys, stopping at either end', async () => {
    const cmds = commands();
    render(<Harness cmds={cmds} />);
    const user = await openPalette();
    const input = screen.getByRole('combobox');

    await user.keyboard('{ArrowUp}{ArrowDown}{ArrowDown}');
    const options = screen.getAllByRole('option');
    expect(options[2]).toHaveAttribute('aria-selected', 'true');
    expect(input).toHaveAttribute('aria-activedescendant', options[2].id);

    await user.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}{Enter}');
    expect(cmds[3].run).toHaveBeenCalledTimes(1);
  });

  it('runs a command that is clicked', async () => {
    const cmds = commands();
    render(<Harness cmds={cmds} />);
    const user = await openPalette();
    await user.click(screen.getByRole('option', { name: 'Go to Images' }));
    expect(cmds[1].run).toHaveBeenCalledTimes(1);
  });

  it('says so when nothing matches, and Enter then does nothing', async () => {
    const cmds = commands();
    render(<Harness cmds={cmds} />);
    const user = await openPalette();
    await user.keyboard('zzz{Enter}');
    expect(screen.getByText('No matching command')).toBeInTheDocument();
    expect(screen.queryAllByRole('option')).toHaveLength(0);
    for (const c of cmds) expect(c.run).not.toHaveBeenCalled();
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('closes on Escape and gives focus back', async () => {
    const cmds = commands();
    render(<Harness cmds={cmds} />);
    const user = await openPalette();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Open palette' })).toHaveFocus();
    for (const c of cmds) expect(c.run).not.toHaveBeenCalled();
  });

  it('starts empty each time it opens', async () => {
    render(<Harness cmds={commands()} />);
    const user = await openPalette();
    await user.keyboard('images{Escape}');
    await user.click(screen.getByRole('button', { name: 'Open palette' }));
    expect(screen.getByRole('combobox')).toHaveValue('');
    expect(screen.getAllByRole('option')).toHaveLength(4);
  });
});
