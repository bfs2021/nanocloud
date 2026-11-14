import { afterEach, beforeEach, describe, it, expect, vi } from 'vitest';
import { act, cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ServiceLogView, retryDelay } from './ServiceLogView';

vi.mock('../api', () => ({ getServiceLog: vi.fn() }));
import { getServiceLog } from '../api';

const read = vi.mocked(getServiceLog);
const SSH =
  "nano-cloud cannot read lab-agent-2's log at 192.168.2.7: SSH starts but does not finish connecting (ssh: handshake failed: connection reset by peer)";
const good = (log: string) => ({ vm: 'lab-agent-2', state: 'installing', log });

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  read.mockReset();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const settle = (ms = 0) => act(() => vi.advanceTimersByTimeAsync(ms));

describe('retryDelay', () => {
  it('doubles from 3 s and stops at 15 s', () => {
    expect([1, 2, 3, 4, 9].map(retryDelay)).toEqual([3000, 6000, 12000, 15000, 15000]);
  });
});

describe('ServiceLogView', () => {
  it('keeps the last log and says it is trying again when a read fails', async () => {
    read.mockResolvedValueOnce(good('installing\nstep one\n')).mockRejectedValue(new Error(SSH));
    render(<ServiceLogView service="lab" vms={['lab-agent-2']} follow />);
    await settle();
    expect(screen.getByLabelText('Install log of lab-agent-2')).toHaveTextContent('step one');

    await settle(3000);
    expect(screen.getByLabelText('Install log of lab-agent-2')).toHaveTextContent('step one');
    expect(screen.getByRole('status')).toHaveTextContent("agent-2 isn't answering over SSH — trying again…");
    // The raw reason is there for whoever wants it, but not in the way.
    expect(screen.getByText(/handshake failed/)).not.toBeVisible();
  });

  it('shows a waiting state, not the raw error, when no log was ever read', async () => {
    read.mockRejectedValue(new Error(SSH));
    render(<ServiceLogView service="lab" vms={['lab-agent-2']} follow={false} />);
    await settle();
    expect(screen.getByLabelText('Install log of lab-agent-2')).toHaveTextContent('Waiting for agent-2 to answer');
    expect(screen.getByLabelText('Install log of lab-agent-2')).not.toHaveTextContent('handshake');
  });

  it('retries on its own without follow, backing off, then recovers', async () => {
    read.mockRejectedValueOnce(new Error(SSH)).mockRejectedValueOnce(new Error(SSH)).mockResolvedValue(good('back again\n'));
    render(<ServiceLogView service="lab" vms={['lab-agent-2']} follow={false} />);
    await settle();
    expect(read).toHaveBeenCalledTimes(1);

    await settle(2900);
    expect(read).toHaveBeenCalledTimes(1);
    await settle(200);
    expect(read).toHaveBeenCalledTimes(2);

    // The second failure waits twice as long.
    await settle(5900);
    expect(read).toHaveBeenCalledTimes(2);
    await settle(200);
    expect(read).toHaveBeenCalledTimes(3);

    expect(screen.getByLabelText('Install log of lab-agent-2')).toHaveTextContent('back again');
    expect(screen.getByRole('status')).not.toHaveTextContent('trying again');
    expect(screen.getByRole('status')).toHaveTextContent('/var/log/nanocloud-service.log on lab-agent-2');

    // A log that is not followed is read once more only if it fails.
    await settle(60000);
    expect(read).toHaveBeenCalledTimes(3);
  });

  it('does not schedule a second read while following and failing', async () => {
    read.mockRejectedValue(new Error(SSH));
    render(<ServiceLogView service="lab" vms={['lab-agent-2']} follow />);
    await settle();
    await settle(3000);
    expect(read).toHaveBeenCalledTimes(2);
    await settle(3000);
    expect(read).toHaveBeenCalledTimes(2);
    await settle(3100);
    expect(read).toHaveBeenCalledTimes(3);
  });

  it('stops reading once unmounted', async () => {
    read.mockRejectedValue(new Error(SSH));
    const { unmount } = render(<ServiceLogView service="lab" vms={['lab-agent-2']} follow={false} />);
    await settle();
    unmount();
    await settle(60000);
    expect(read).toHaveBeenCalledTimes(1);
  });

  it('marks the machines it cannot reach, and drops an answer for a machine no longer shown', async () => {
    let finish: (l: ReturnType<typeof good>) => void = () => {};
    read.mockImplementation((_s, vm) =>
      vm === 'lab-server'
        ? new Promise(r => {
            finish = r as typeof finish;
          })
        : Promise.resolve({ vm, state: 'ready', log: 'agent log\n' }),
    );
    render(<ServiceLogView service="lab" vms={['lab-server', 'lab-agent-2']} follow={false} unreachable={['lab-agent-2']} />);
    expect(screen.getByRole('button', { name: /agent-2/ })).toContainElement(screen.getByRole('img', { name: 'not answering' }));

    await userEvent.setup({ advanceTimers: vi.advanceTimersByTime }).click(screen.getByRole('button', { name: /agent-2/ }));
    await settle();
    finish(good('late answer for the server\n'));
    await settle();
    expect(screen.getByLabelText('Install log of lab-agent-2')).toHaveTextContent('agent log');
  });
});
