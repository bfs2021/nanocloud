import { beforeEach, describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { RestartVM } from './RestartVM';

vi.mock('../api', () => ({ stopVM: vi.fn(), startVM: vi.fn() }));
import { startVM, stopVM } from '../api';

beforeEach(() => {
  vi.resetAllMocks();
});

describe('RestartVM', () => {
  it('asks before pulling the power, and Cancel changes nothing', async () => {
    const user = userEvent.setup();
    render(<RestartVM vm="lab-agent-1" />);
    await user.click(screen.getByRole('button', { name: 'Restart VM' }));
    expect(screen.getByText('Pull the power on lab-agent-1 and start it again?')).toBeInTheDocument();
    expect(stopVM).not.toHaveBeenCalled();
    // The safe answer has the focus.
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus();

    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.getByRole('button', { name: 'Restart VM' })).toBeInTheDocument();
    expect(stopVM).not.toHaveBeenCalled();
  });

  it('Escape withdraws the question', async () => {
    const user = userEvent.setup();
    render(<RestartVM vm="lab-agent-1" />);
    await user.click(screen.getByRole('button', { name: 'Restart VM' }));
    await user.keyboard('{Escape}');
    expect(screen.getByRole('button', { name: 'Restart VM' })).toBeInTheDocument();
  });

  it('forces the VM off, then starts it, showing progress between', async () => {
    const user = userEvent.setup();
    const order: string[] = [];
    let release: () => void = () => {};
    vi.mocked(stopVM).mockImplementation(async () => {
      order.push('stop');
      await new Promise<void>(r => {
        release = r;
      });
    });
    vi.mocked(startVM).mockImplementation(async () => {
      order.push('start');
    });
    render(<RestartVM vm="lab-agent-1" />);

    await user.click(screen.getByRole('button', { name: 'Restart VM' }));
    await user.click(screen.getByRole('button', { name: 'Power off and restart' }));
    expect(stopVM).toHaveBeenCalledWith('lab-agent-1', true);
    expect(await screen.findByRole('status')).toHaveTextContent('Restarting…');
    expect(startVM).not.toHaveBeenCalled();

    release();
    expect(await screen.findByText(/Restarted/)).toBeInTheDocument();
    expect(startVM).toHaveBeenCalledWith('lab-agent-1');
    expect(order).toEqual(['stop', 'start']);
  });

  it('says so when the power-off fails, and lets the user try again', async () => {
    const user = userEvent.setup();
    vi.mocked(stopVM).mockRejectedValue(new Error('libvirt is not answering'));
    render(<RestartVM vm="lab-agent-1" />);
    await user.click(screen.getByRole('button', { name: 'Restart VM' }));
    await user.click(screen.getByRole('button', { name: 'Power off and restart' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not power lab-agent-1 off: libvirt is not answering');
    expect(startVM).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Restart VM' })).toBeInTheDocument();
  });

  it('says the VM is off when it could not be started again', async () => {
    const user = userEvent.setup();
    vi.mocked(stopVM).mockResolvedValue();
    vi.mocked(startVM).mockRejectedValue(new Error('no space left'));
    render(<RestartVM vm="lab-agent-1" />);
    await user.click(screen.getByRole('button', { name: 'Restart VM' }));
    await user.click(screen.getByRole('button', { name: 'Power off and restart' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('lab-agent-1 was powered off, but starting it again failed: no space left');
  });
});
