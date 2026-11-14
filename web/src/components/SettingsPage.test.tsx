import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { Settings } from '../types';

const base: Settings = {
  self_healing: true,
  update_check: true,
  pools: [
    { name: 'default', path: '/var/lib/nano-cloud/vm-disks', default: true, removable: false },
    { name: 'nvme', path: '/mnt/nvme', default: false, removable: true },
  ],
  file: { config_path: '/etc/nano-cloud/config.yaml', listen: ':8080', image_cache_dir: '/var/lib/nano-cloud/images', metadata_path: '/m' },
};

vi.mock('../api', () => ({
  getSettings: vi.fn(),
  patchSettings: vi.fn(),
  addStoragePool: vi.fn(),
  removeStoragePool: vi.fn(),
  listStoragePools: vi.fn().mockResolvedValue([]),
  listTokens: vi.fn(),
  createToken: vi.fn(),
  revokeToken: vi.fn().mockResolvedValue(undefined),
  changePassword: vi.fn(),
  logout: vi.fn(),
  getUpdateStatus: vi.fn().mockResolvedValue({ current: 'v0.1.74', available: false }),
  getSystemDeps: vi.fn().mockResolvedValue({ checks: [] }),
  getHostDevices: vi.fn().mockResolvedValue({ iommu: { enabled: false, groups_seen: 0, message: '' }, pci: [], usb: [] }),
  listTemplates: vi.fn().mockResolvedValue([]),
  deleteTemplate: vi.fn(),
  UNAUTHORIZED_EVENT: 'nanocloud:unauthorized',
}));

import { addStoragePool, createToken, getSettings, listTokens, patchSettings, removeStoragePool, revokeToken } from '../api';
import { SettingsPage } from './SettingsPage';

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(getSettings).mockResolvedValue(structuredClone(base));
  vi.mocked(listTokens).mockResolvedValue([]);
});

describe('SettingsPage', () => {
  it('opens the section the URL names, links every section, and falls back to General', () => {
    const { rerender } = render(<SettingsPage section="about" />);
    expect(screen.getByRole('heading', { level: 1, name: 'About' })).toBeInTheDocument();
    const nav = screen.getByRole('navigation', { name: 'Settings sections' });
    expect(within(nav).getByRole('link', { name: 'About' })).toHaveAttribute('aria-current', 'page');
    expect(within(nav).getByRole('link', { name: 'Storage' })).toHaveAttribute('href', '#/settings/storage');

    rerender(<SettingsPage section="nonsense" />);
    expect(screen.getByRole('heading', { level: 1, name: 'General' })).toBeInTheDocument();
  });

  it('turns self-healing off and shows what the daemon answered', async () => {
    vi.mocked(patchSettings).mockResolvedValue({ ...base, self_healing: false });
    const user = userEvent.setup();
    render(<SettingsPage section="self-healing" />);
    const sw = await screen.findByRole('switch', { name: 'Keep VMs the way you left them' });
    expect(sw).toHaveAttribute('aria-checked', 'true');

    await user.click(sw);
    expect(patchSettings).toHaveBeenCalledWith({ self_healing: false });
    expect(sw).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByText(/stays down until you start it/)).toBeInTheDocument();
  });

  it('says why a switch did not change, beside it', async () => {
    vi.mocked(patchSettings).mockRejectedValue(new Error('settings: disk full'));
    const user = userEvent.setup();
    render(<SettingsPage section="updates" />);
    const sw = await screen.findByRole('switch', { name: 'Check for new releases' });
    await user.click(sw);
    expect(await screen.findByRole('alert')).toHaveTextContent('disk full');
    expect(sw).toHaveAttribute('aria-checked', 'true');
  });

  it('shows a new token once, and revokes only after asking', async () => {
    const tok = { id: 't1', name: 'ci', created_at: '2026-11-13T10:00:00Z', last_used: '0001-01-01T00:00:00Z' };
    vi.mocked(createToken).mockResolvedValue({ ...tok, token: 'nc_secret' });
    vi.mocked(listTokens).mockResolvedValueOnce([]).mockResolvedValue([tok]);
    const user = userEvent.setup();
    render(<SettingsPage section="security" />);

    await user.type(screen.getByLabelText('New token name'), 'ci');
    await user.click(screen.getByRole('button', { name: 'Create token' }));
    expect(createToken).toHaveBeenCalledWith('ci');
    expect(await screen.findByText('nc_secret')).toBeInTheDocument();
    expect(screen.getByText(/never used/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Done' }));
    expect(screen.queryByText('nc_secret')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Revoke ci' }));
    expect(revokeToken).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Revoke token' }));
    expect(revokeToken).toHaveBeenCalledWith('t1');
  });

  it('adds a pool, shows a refusal verbatim, and removes only Settings-added pools', async () => {
    vi.mocked(addStoragePool).mockRejectedValueOnce(new Error('/srv/vms is read-only to the daemon'));
    vi.mocked(removeStoragePool).mockResolvedValue({ ...base, pools: [base.pools[0]] });
    const user = userEvent.setup();
    render(<SettingsPage section="storage" />);
    await screen.findByText('/mnt/nvme');

    await user.type(screen.getByLabelText('Name'), 'srv');
    await user.type(screen.getByLabelText('Directory'), '/srv/vms');
    await user.click(screen.getByRole('button', { name: 'Add pool' }));
    expect(addStoragePool).toHaveBeenCalledWith('srv', '/srv/vms');
    expect(await screen.findByRole('alert')).toHaveTextContent('read-only to the daemon');

    expect(screen.queryByRole('button', { name: 'Remove default' })).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Remove nvme' }));
    await user.click(screen.getByRole('button', { name: 'Remove pool' }));
    expect(removeStoragePool).toHaveBeenCalledWith('nvme');
    expect(screen.queryByText('/mnt/nvme')).not.toBeInTheDocument();
  });
});
