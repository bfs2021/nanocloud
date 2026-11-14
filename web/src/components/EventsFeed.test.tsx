import { describe, it, expect, beforeEach } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { EventsFeed } from './EventsFeed';
import type { NanoEvent } from '../types';

const base: NanoEvent = {
  seq: 1,
  at: new Date().toISOString(),
  level: 'info',
  kind: 'vm.restarted',
  vm: 'test1',
  message: 'restarted (attempt 1/6)',
};

// renderOpen renders the feed unfolded, as a person who opened it sees it.
function renderOpen(ui: React.ReactElement) {
  const r = render(ui);
  fireEvent.click(screen.getByRole('button', { name: /^Activity/ }));
  return r;
}

beforeEach(() => {
  localStorage.clear();
});

describe('EventsFeed', () => {
  it('starts folded, saying what happened last and what needs a look', () => {
    render(
      <EventsFeed
        events={[
          { ...base, level: 'warn', message: 'restart attempt 1/6 failed' },
          { ...base, seq: 2, message: 'recovered' },
        ]}
      />,
    );
    const header = screen.getByRole('button', { name: /^Activity/ });
    expect(header).toHaveAttribute('aria-expanded', 'false');
    expect(header).toHaveTextContent('1 needs a look');
    expect(header).toHaveTextContent('test1 · recovered');
    expect(screen.queryByRole('listitem')).not.toBeInTheDocument();
  });

  it('remembers being opened', () => {
    render(<EventsFeed events={[base]} />);
    fireEvent.click(screen.getByRole('button', { name: /^Activity/ }));
    expect(screen.getAllByRole('listitem')).toHaveLength(1);
    cleanup();
    render(<EventsFeed events={[base]} />);
    expect(screen.getByRole('button', { name: /^Activity/ })).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getAllByRole('listitem')).toHaveLength(1);
  });

  it('says so when there is nothing to report', () => {
    renderOpen(<EventsFeed events={[]} />);
    expect(screen.getByText(/hasn't had to step in/)).toBeInTheDocument();
  });

  it('renders newest first', () => {
    // App holds the backlog oldest-first, matching GET /events' ascending seq.
    renderOpen(<EventsFeed events={[base, { ...base, seq: 2, message: 'recovered' }]} />);
    const rows = screen.getAllByRole('listitem');
    expect(rows[0]).toHaveTextContent('recovered');
  });

  it('renders detail, the underlying cause, when present', () => {
    // The detail is the only lasting record of why a restart failed.
    renderOpen(
      <EventsFeed
        events={[
          {
            ...base,
            level: 'warn',
            message: 'restart attempt 1/6 failed, retrying at 2026-08-08T09:14:02Z',
            detail: 'Image is not in qcow2 format',
          },
        ]}
      />,
    );
    expect(screen.getByText('Image is not in qcow2 format')).toBeInTheDocument();
  });

  it('omits the detail line when the event carries none', () => {
    const { container } = renderOpen(<EventsFeed events={[base]} />);
    // One row, one text node — no empty second line.
    expect(container.querySelectorAll('li')).toHaveLength(1);
    expect(screen.getByRole('listitem')).toHaveTextContent('restarted (attempt 1/6)');
  });

  it('does not repeat the VM name that the message omits', () => {
    // The feed renders vm as its own chip, so messages leave the name out.
    renderOpen(<EventsFeed events={[base]} />);
    expect(screen.getByRole('listitem').textContent?.match(/test1/g)).toHaveLength(1);
  });
});
