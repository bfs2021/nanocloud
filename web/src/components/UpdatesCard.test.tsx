import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { UpdatesCard } from './UpdatesCard';
import { getUpdateStatus } from '../api';

vi.mock('../api', () => ({ getUpdateStatus: vi.fn() }));

describe('UpdatesCard', () => {
  it('shows the command and the release notes when a release is newer', async () => {
    vi.mocked(getUpdateStatus).mockResolvedValue({
      current: 'v0.1.73',
      latest: { tag: 'v0.1.80', url: 'https://example.com/notes', published_at: '2026-11-10T00:00:00Z' },
      available: true,
    });
    render(<UpdatesCard />);
    expect(await screen.findByText('sudo nanocloud update')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Release notes' })).toHaveAttribute('href', 'https://example.com/notes');
  });

  it('says so when up to date, offering no command', async () => {
    vi.mocked(getUpdateStatus).mockResolvedValue({ current: 'v0.1.80', available: false });
    render(<UpdatesCard />);
    expect(await screen.findByText(/the latest release/)).toBeInTheDocument();
    expect(screen.queryByText('sudo nanocloud update')).not.toBeInTheDocument();
  });

  it('explains a check turned off in config', async () => {
    vi.mocked(getUpdateStatus).mockResolvedValue({ current: 'v0.1.80', available: false, disabled: true });
    render(<UpdatesCard />);
    expect(await screen.findByText(/never asks GitHub/)).toBeInTheDocument();
  });

  it('reports a failed check rather than claiming to be current', async () => {
    vi.mocked(getUpdateStatus).mockResolvedValue({ current: 'v0.1.80', available: false, error: 'GitHub answered 403' });
    render(<UpdatesCard />);
    expect(await screen.findByText(/GitHub answered 403/)).toBeInTheDocument();
    expect(screen.queryByText(/the latest release/)).not.toBeInTheDocument();
  });
});
