import { describe, it, expect, vi, beforeEach } from 'vitest';
import { act, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import App, { mergeEventPage } from './App';
import type { NanoEvent } from './types';
import {
  MOCK_VMS,
  MOCK_CACHED_IMAGES,
  MOCK_CATALOG_IMAGES,
  MOCK_STORAGE_POOLS,
  MOCK_BRIDGES,
  MOCK_NETWORK_INTERFACES,
  MOCK_UPLINK,
  MOCK_NETWORK_LEASES,
  MOCK_SYSTEM_DEPS,
} from './mock';

// Full module mock: App and every view it renders import from './api', so the
// factory must cover the whole surface, not just what App itself calls.
vi.mock('./api', () => ({
  listVMs: vi.fn(),
  startVM: vi.fn().mockResolvedValue(undefined),
  stopVM: vi.fn().mockResolvedValue(undefined),
  deleteVM: vi.fn().mockResolvedValue(undefined),
  createVM: vi.fn().mockResolvedValue({ name: 'x' }),
  getVMStats: vi.fn().mockResolvedValue({ cpu_percent: 0, memory_used_mb: 0, memory_total_mb: 0 }),
  listSnapshots: vi.fn().mockResolvedValue([]),
  createSnapshot: vi.fn().mockResolvedValue(undefined),
  deleteSnapshot: vi.fn().mockResolvedValue(undefined),
  listCachedImages: vi.fn(),
  listCatalog: vi.fn(),
  pullImage: vi.fn().mockResolvedValue(undefined),
  getImageStatus: vi.fn().mockResolvedValue({ state: 'cached', progress: 100 }),
  deleteImage: vi.fn().mockResolvedValue(undefined),
  cancelPull: vi.fn().mockResolvedValue(undefined),
  listStoragePools: vi.fn(),
  getSystemInfo: vi.fn(),
  getSystemDeps: vi.fn(),
  getUpdateStatus: vi.fn(),
  getSettings: vi.fn().mockResolvedValue({
    self_healing: true,
    update_check: true,
    pools: [],
    file: { config_path: '/etc/nano-cloud/config.yaml', listen: ':8080', image_cache_dir: '/i', metadata_path: '/m' },
  }),
  patchSettings: vi.fn(),
  listTokens: vi.fn().mockResolvedValue([]),
  getHostDevices: vi.fn().mockResolvedValue({
    iommu: { enabled: false, groups_seen: 0, message: 'IOMMU is off' },
    pci: [],
    usb: [],
  }),
  listNetworkInterfaces: vi.fn(),
  listNetworkLeases: vi.fn(),
  listEvents: vi.fn().mockResolvedValue({ events: [], cursor: 0 }),
  listTemplates: vi.fn().mockResolvedValue([]),
  deleteTemplate: vi.fn().mockResolvedValue(undefined),
  getStatsHistory: vi.fn().mockResolvedValue([]),
  listServices: vi.fn().mockResolvedValue([]),
  listServiceCatalog: vi.fn().mockResolvedValue([]),
  createService: vi.fn(),
  deleteService: vi.fn().mockResolvedValue(undefined),
  startService: vi.fn().mockResolvedValue(undefined),
  stopService: vi.fn().mockResolvedValue(undefined),
  serviceFileURL: (name: string, file: string) => `/services/${name}/files/${file}`,
}));

import {
  listVMs,
  startVM,
  stopVM,
  deleteVM,
  listCachedImages,
  listCatalog,
  listStoragePools,
  getSystemInfo,
  getSystemDeps,
  getUpdateStatus,
  listNetworkInterfaces,
  listNetworkLeases,
  listServices,
  listServiceCatalog,
  createService,
} from './api';
import { MOCK_SERVICE_CATALOG } from './mock';
import type { Service } from './types';

beforeEach(() => {
  // The page lives in the URL, which jsdom keeps between tests.
  window.history.replaceState(null, '', '#/vms');
  vi.clearAllMocks();
  vi.mocked(listVMs).mockResolvedValue(MOCK_VMS);
  vi.mocked(listCachedImages).mockResolvedValue(MOCK_CACHED_IMAGES);
  vi.mocked(listCatalog).mockResolvedValue(MOCK_CATALOG_IMAGES);
  vi.mocked(listStoragePools).mockResolvedValue(MOCK_STORAGE_POOLS);
  vi.mocked(listNetworkInterfaces).mockResolvedValue({ bridges: MOCK_BRIDGES, interfaces: MOCK_NETWORK_INTERFACES, uplink: MOCK_UPLINK });
  vi.mocked(listNetworkLeases).mockResolvedValue(MOCK_NETWORK_LEASES);
  vi.mocked(getSystemDeps).mockResolvedValue(MOCK_SYSTEM_DEPS);
  vi.mocked(getUpdateStatus).mockResolvedValue({ current: 'v0.1.73', available: false });
  vi.mocked(listServiceCatalog).mockResolvedValue(MOCK_SERVICE_CATALOG);
  vi.mocked(listServices).mockResolvedValue([]);
  vi.mocked(getSystemInfo).mockResolvedValue({
    hostname: 'test-host',
    cpu_percent: 10,
    memory_used_mb: 2048,
    memory_total_mb: 8192,
    cpus: 8,
  });
});

describe('App', () => {
  it('loads the dashboard with VMs from the API', async () => {
    render(<App />);
    expect(await screen.findByRole('heading', { name: 'Virtual machines' })).toBeInTheDocument();
    for (const vm of MOCK_VMS) {
      expect(screen.getByText(vm.name)).toBeInTheDocument();
    }
    expect(listVMs).toHaveBeenCalled();
  });

  it('navigates between views via the sidebar', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole('heading', { name: 'Virtual machines' });

    await user.click(screen.getByRole('button', { name: 'Images' }));
    expect(await screen.findByRole('heading', { name: 'Images' })).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Settings' }));
    expect(await screen.findByRole('heading', { name: 'General', level: 1 })).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Virtual machines' }));
    expect(await screen.findByRole('heading', { name: 'Virtual machines' })).toBeInTheDocument();
  });

  it('deletes a VM from its own page and leaves for the list, which Back does not return from', async () => {
    const vm = MOCK_VMS[0];
    vi.mocked(listVMs).mockResolvedValue([vm]);
    window.history.replaceState(null, '', `#/vms/${vm.id}`);
    const user = userEvent.setup();
    render(<App />);
    await user.click(await screen.findByRole('button', { name: `Delete ${vm.name}` }));
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete VM' }));
    expect(deleteVM).toHaveBeenCalledWith(vm.id);
    expect(await screen.findByRole('heading', { name: 'Virtual machines' })).toBeInTheDocument();
    expect(window.location.hash).toBe('#/vms');
  });

  it('warns about an unready host on every page except the one listing the checks', async () => {
    vi.mocked(getSystemDeps).mockResolvedValue({
      checks: [{ name: 'xorriso', ok: false, required: true, message: 'xorriso is not installed' }],
    });
    window.history.replaceState(null, '', '#/images');
    const { unmount } = render(<App />);
    expect(await screen.findByText(/can't create VMs: xorriso is not installed/)).toBeInTheDocument();
    unmount();

    window.history.replaceState(null, '', '#/settings/host');
    render(<App />);
    // The section's own list has loaded, so the banner would have too.
    expect(await screen.findByText('xorriso is not installed')).toBeInTheDocument();
    expect(screen.queryByText(/can't create VMs/)).not.toBeInTheDocument();
  });

  it('says so when a link names a VM that does not exist', async () => {
    window.history.replaceState(null, '', '#/vms/gone');
    const user = userEvent.setup();
    render(<App />);
    expect(await screen.findByRole('heading', { name: 'No VM named gone' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Show all VMs' }));
    expect(await screen.findByRole('heading', { name: 'Virtual machines' })).toBeInTheDocument();
  });

  it('opens the page the URL names, and Back returns to the previous one', async () => {
    window.history.replaceState(null, '', '#/images');
    const user = userEvent.setup();
    render(<App />);
    expect(await screen.findByRole('heading', { name: 'Images' })).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Settings' }));
    expect(window.location.hash).toBe('#/settings');

    act(() => {
      window.history.replaceState(null, '', '#/images');
      window.dispatchEvent(new HashChangeEvent('hashchange'));
    });
    expect(await screen.findByRole('heading', { name: 'Images' })).toBeInTheDocument();
  });

  it('points the sidebar at Settings when a newer release exists', async () => {
    vi.mocked(getUpdateStatus).mockResolvedValue({
      current: 'v0.1.73',
      latest: { tag: 'v0.1.80', url: 'https://example.com/r', published_at: '2026-11-10T00:00:00Z' },
      available: true,
    });
    const user = userEvent.setup();
    render(<App />);
    await user.click(await screen.findByRole('button', { name: 'v0.1.80 available' }));
    expect(window.location.hash).toBe('#/settings/updates');
    expect(await screen.findByText('sudo nanocloud update')).toBeInTheDocument();
  });

  it('opens the command palette with Ctrl+K and runs a command from it', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole('heading', { name: 'Virtual machines' });

    await user.keyboard('{Control>}k{/Control}');
    expect(screen.getByRole('dialog', { name: 'Command palette' })).toBeInTheDocument();
    await user.keyboard('go images{Enter}');

    expect(await screen.findByRole('heading', { name: 'Images' })).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: 'Command palette' })).not.toBeInTheDocument();
  });

  it('offers each VM by name in the command palette', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole('heading', { name: 'Virtual machines' });

    await user.click(screen.getByRole('button', { name: /search or run a command/i }));
    await user.keyboard('ssh web-prod');
    expect(screen.getByRole('option', { name: /Copy SSH command for web-prod-01/ })).toHaveTextContent('ssh admin@192.168.122.12');
  });

  it('switches between the dark and light themes from the sidebar', async () => {
    document.documentElement.dataset.theme = 'dark';
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole('heading', { name: 'Virtual machines' });

    await user.click(screen.getByRole('button', { name: 'Switch to light' }));
    expect(document.documentElement.dataset.theme).toBe('light');
    await user.click(screen.getByRole('button', { name: 'Switch to dark' }));
    expect(document.documentElement.dataset.theme).toBe('dark');
  });

  it('launches a service from the Services page and opens it', async () => {
    const created: Service = {
      name: 'pg-1',
      recipe: 'postgres',
      recipe_name: 'PostgreSQL',
      category: 'database',
      size: MOCK_SERVICE_CATALOG.find(r => r.id === 'postgres')!.sizes[1],
      state: 'pulling',
      progress: 12,
      vms: ['pg-1'],
      created_at_unix: 0,
      outputs: [],
      files: [],
      endpoints: [],
      shared: false,
      vm_user: 'nanocloud',
      vm_password: 'pw',
    };
    vi.mocked(createService).mockResolvedValue(created);
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole('heading', { name: 'Virtual machines' });

    await user.click(screen.getByRole('button', { name: 'Services' }));
    expect(await screen.findByRole('heading', { name: 'Services' })).toBeInTheDocument();
    await user.click(await screen.findByRole('button', { name: 'Launch PostgreSQL' }));

    const drawer = await screen.findByRole('dialog', { name: 'Launch PostgreSQL' });
    expect(drawer).toBeInTheDocument();
    vi.mocked(listServices).mockResolvedValue([created]);
    await user.click(within(drawer).getByRole('button', { name: 'Launch PostgreSQL' }));

    expect(createService).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'pg-1', recipe: 'postgres', size: 'medium', params: { database: 'app', user: 'app' } }),
    );
    expect(await screen.findByRole('heading', { name: 'pg-1' })).toBeInTheDocument();
    expect(screen.getByText('Downloading the base image · 12%')).toBeInTheDocument();
  });

  it('offers to launch services from the command palette', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole('heading', { name: 'Virtual machines' });
    await user.keyboard('{Control>}k{/Control}');
    await user.keyboard('launch kube{Enter}');
    expect(await screen.findByRole('dialog', { name: 'Launch Kubernetes' })).toBeInTheDocument();
  });

  it('opens the create-VM modal from the sidebar', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole('heading', { name: 'Virtual machines' });
    await user.click(screen.getByRole('button', { name: /new vm/i }));
    expect(await screen.findByText('Create virtual machine')).toBeInTheDocument();
  });

  it('optimistically marks a VM running after Start', async () => {
    const stopped = MOCK_VMS.find(v => v.status === 'stopped')!;
    vi.mocked(listVMs).mockResolvedValue([stopped]);
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText(stopped.name);

    // Exact match, not /start/i — the readiness banner's remedy snippet for
    // the mock's failing "default network" check contains "net-start" /
    // "net-autostart", which a substring regex would also match.
    await user.click(screen.getByRole('button', { name: 'Start' }));
    expect(startVM).toHaveBeenCalledWith(stopped.id);
    // Optimistic update flips the card to running (Stop button appears).
    expect(await screen.findByRole('button', { name: /stop/i })).toBeInTheDocument();
  });

  // --- Failure surfacing -----------------------------------------------

  it('surfaces a failed Start instead of swallowing it', async () => {
    const stopped = MOCK_VMS.find(v => v.status === 'stopped')!;
    vi.mocked(listVMs).mockResolvedValue([stopped]);
    vi.mocked(startVM).mockRejectedValue(new Error('domain is already active'));
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText(stopped.name);

    await user.click(screen.getByRole('button', { name: 'Start' }));

    // The backend's own message reaches the user, not a generic string.
    expect(await screen.findByText(/domain is already active/)).toBeInTheDocument();
  });

  it('surfaces a failed Delete and leaves the VM in the list', async () => {
    const vm = MOCK_VMS[0];
    vi.mocked(listVMs).mockResolvedValue([vm]);
    vi.mocked(deleteVM).mockRejectedValue(new Error('cannot delete inactive domain with 2 snapshots'));
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText(vm.name);

    await user.click(screen.getByRole('button', { name: `Delete ${vm.name}` }));
    await user.click(within(screen.getByRole('dialog', { name: `Delete ${vm.name}?` })).getByRole('button', { name: 'Delete VM' }));

    expect(await screen.findByText(/cannot delete inactive domain/)).toBeInTheDocument();
    // The optimistic removal must not have happened.
    expect(screen.getByText(vm.name)).toBeInTheDocument();
  });

  it('asks before deleting a service’s VM on its own, pointing at the service', async () => {
    const vm = { ...MOCK_VMS[0], id: 'lab-agent-1', name: 'lab-agent-1', service: 'lab' };
    vi.mocked(listVMs).mockResolvedValue([vm]);
    vi.mocked(deleteVM).mockResolvedValue(undefined);
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText(vm.name);

    await user.click(screen.getByRole('button', { name: 'Delete lab-agent-1' }));
    const dialog = screen.getByRole('dialog', { name: 'Delete lab-agent-1?' });
    expect(within(dialog).queryByRole('button', { name: 'Delete VM' })).not.toBeInTheDocument();
    expect(deleteVM).not.toHaveBeenCalled();

    await user.click(within(dialog).getByRole('checkbox', { name: /Delete only this VM/ }));
    await user.click(within(dialog).getByRole('button', { name: 'Delete VM anyway' }));
    expect(deleteVM).toHaveBeenCalledWith('lab-agent-1');
  });

  it('says when the storage pools and networks behind the wizard could not be loaded', async () => {
    vi.mocked(listStoragePools).mockRejectedValue(new Error('permission denied'));
    render(<App />);
    expect(await screen.findByText(/Could not load storage pools and networks: permission denied/)).toBeInTheDocument();
  });

  it('renders an error state, not the empty state, when the VM list fails', async () => {
    vi.mocked(listVMs).mockRejectedValue(new Error('connection refused'));
    render(<App />);

    expect(await screen.findByText(/Couldn't load virtual machines/)).toBeInTheDocument();
    expect(await screen.findByText(/connection refused/)).toBeInTheDocument();
    // An outage must not invite the user to create a VM against a down server.
    expect(screen.queryByText('No virtual machines yet')).not.toBeInTheDocument();
  });

  it('marks a VM "Stopping…" rather than optimistically flipping it to stopped', async () => {
    const running = MOCK_VMS.find(v => v.status === 'running')!;
    vi.mocked(listVMs).mockResolvedValue([running]);
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText(running.name);

    await user.click(screen.getByRole('button', { name: /^stop$/i }));
    expect(stopVM).toHaveBeenCalledWith(running.id);

    // A graceful shutdown is a request the guest may ignore, so the card says
    // Stopping, not stopped.
    expect(await screen.findByRole('button', { name: /stopping/i })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Start' })).not.toBeInTheDocument();
  });

  it('surfaces a failed Stop', async () => {
    const running = MOCK_VMS.find(v => v.status === 'running')!;
    vi.mocked(listVMs).mockResolvedValue([running]);
    vi.mocked(stopVM).mockRejectedValue(new Error('domain is not running'));
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText(running.name);

    await user.click(screen.getByRole('button', { name: /^stop$/i }));

    expect(await screen.findByText(/domain is not running/)).toBeInTheDocument();
  });
});

// The events ring is in-memory and its seq restarts at 1 with the daemon, so
// the merge has to tell "new events" from "the same events again".
describe('mergeEventPage', () => {
  const ev = (seq: number): NanoEvent => ({
    seq,
    at: '2026-08-08T09:00:00Z',
    level: 'info',
    kind: 'vm.restarted',
    vm: 'test1',
    message: `restarted (attempt ${seq}/6)`,
  });

  it('appends a page that continues the cursor', () => {
    const got = mergeEventPage([ev(1)], { events: [ev(2), ev(3)], cursor: 3 }, 1);
    expect(got.map(e => e.seq)).toEqual([1, 2, 3]);
  });

  it('keeps the same array when a poll brings nothing', () => {
    const prev = [ev(1)];
    // Referential identity matters: a new array here re-renders the feed on
    // every 10s poll of an idle host.
    expect(mergeEventPage(prev, { events: [], cursor: 1 }, 1)).toBe(prev);
  });

  it('replaces rather than duplicating when the daemon restarted', () => {
    // We asked from seq 9; a fresh ring answered with its own seq 1-2 and a
    // cursor of 2. Appending would show every event twice, with duplicate keys.
    const got = mergeEventPage([ev(8), ev(9)], { events: [ev(1), ev(2)], cursor: 2 }, 9);
    expect(got.map(e => e.seq)).toEqual([1, 2]);
  });

  it('clears the backlog when a restarted daemon has an empty ring', () => {
    expect(mergeEventPage([ev(8)], { events: [], cursor: 0 }, 8)).toEqual([]);
  });
});
