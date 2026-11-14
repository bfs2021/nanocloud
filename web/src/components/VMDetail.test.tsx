import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { VMDetail } from './VMDetail';
import { MOCK_VMS } from '../mock';

vi.mock('../api', () => ({
  getVMStats: vi.fn().mockResolvedValue({ cpu_percent: 0, memory_used_mb: 0, memory_total_mb: 0 }),
  getStatsHistory: vi.fn().mockResolvedValue([]),
  listSnapshots: vi.fn().mockResolvedValue([]),
  createSnapshot: vi.fn().mockResolvedValue(undefined),
  deleteSnapshot: vi.fn().mockResolvedValue(undefined),
  restoreSnapshot: vi.fn().mockResolvedValue(undefined),
  consoleWebSocketURL: vi.fn().mockReturnValue('ws://localhost/console'),
  // Inlined rather than imported: vi.mock's factory is hoisted above the
  // imports, so a top-level reference here is not initialised yet.
  getVMSpec: vi.fn().mockResolvedValue({
    pci_devices: [],
    usb_devices: [],
    cpu: { mode: 'host-model', topology: null, cpu_set: '' },
    network: { mode: 'nat', bridge: '', vlan_id: 0 },
    disks: [],
    restart_required: false,
    warnings: [],
  }),
  patchVMSpec: vi.fn(),
  attachDisk: vi.fn(),
  detachDisk: vi.fn(),
  resizeDisk: vi.fn(),
}));

import { deleteSnapshot, listSnapshots, restoreSnapshot } from '../api';

const runningVM = MOCK_VMS.find(v => v.status === 'running')!;

function renderDetail(overrides: Partial<Parameters<typeof VMDetail>[0]> = {}) {
  const handlers = {
    onBack: vi.fn(),
    onStart: vi.fn(),
    onStop: vi.fn(),
    onForceStop: vi.fn(),
    onError: vi.fn(),
    onRefreshVMs: vi.fn(),
  };
  render(<VMDetail vm={runningVM} leases={[]} bridges={[]} uplink="eth0" {...handlers} {...overrides} />);
  return handlers;
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(listSnapshots).mockResolvedValue([]);
  vi.mocked(restoreSnapshot).mockResolvedValue(undefined);
});

describe('VMDetail', () => {
  it('distinguishes a failed snapshot fetch from an empty snapshot list', async () => {
    vi.mocked(listSnapshots).mockRejectedValue(new Error('libvirt: connection reset'));
    const user = userEvent.setup();
    renderDetail();

    await user.click(screen.getByRole('tab', { name: /storage/i }));

    expect(await screen.findByRole('alert')).toHaveTextContent(/libvirt: connection reset/);
    // A failed fetch must not read as a VM with no snapshots.
    expect(screen.queryByText('No snapshots yet for this VM.')).not.toBeInTheDocument();
  });

  it('still shows the empty state when the VM genuinely has no snapshots', async () => {
    const user = userEvent.setup();
    renderDetail();

    await user.click(screen.getByRole('tab', { name: /storage/i }));

    expect(await screen.findByText('No snapshots yet for this VM.')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('asks before deleting a snapshot', async () => {
    vi.mocked(listSnapshots).mockResolvedValue([{ name: 'before-upgrade', description: '', created_at: '2026-07-30T10:00:00Z' }]);
    const user = userEvent.setup();
    renderDetail();

    await user.click(screen.getByRole('tab', { name: /storage/i }));
    await screen.findByText('before-upgrade');
    await user.click(screen.getByRole('button', { name: /^delete$/i }));
    expect(deleteSnapshot).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Delete snapshot' }));
    expect(deleteSnapshot).toHaveBeenCalledWith(expect.any(String), 'before-upgrade');
  });

  it('reports a failed snapshot restore instead of silently reverting the button', async () => {
    vi.mocked(listSnapshots).mockResolvedValue([{ name: 'before-upgrade', description: '', created_at: '2026-07-30T10:00:00Z' }]);
    vi.mocked(restoreSnapshot).mockRejectedValue(new Error('snapshot has no disk state'));
    const user = userEvent.setup();
    const handlers = renderDetail();

    await user.click(screen.getByRole('tab', { name: /storage/i }));
    await screen.findByText('before-upgrade');
    await user.click(screen.getByRole('button', { name: /^restore$/i }));
    await user.click(screen.getByRole('button', { name: 'Restore snapshot' }));

    expect(handlers.onError).toHaveBeenCalledWith(expect.stringContaining('before-upgrade'), expect.any(Error));
    // A failed restore must not claim the VM was rolled back.
    expect(handlers.onRefreshVMs).not.toHaveBeenCalled();
  });

  // A lease can outlive the guest (it survives a host power cut), while vm.ip
  // was confirmed via ARP or the guest agent, so vm.ip wins.
  it('prefers the confirmed IP over a DHCP lease', async () => {
    const user = userEvent.setup();
    renderDetail({
      vm: { ...runningVM, ip: '10.20.0.34', mac: '52:54:00:ab:cd:ef' },
      leases: [{ vm_name: runningVM.name, ip: '192.168.122.99', mac: '52:54:00:99:99:99' }],
    });

    await user.click(screen.getByRole('tab', { name: /network/i }));

    expect(await screen.findByText('10.20.0.34')).toBeInTheDocument();
    expect(screen.queryByText('192.168.122.99')).not.toBeInTheDocument();
    // The MAC comes from the VM, not the lease, so it shows without one.
    expect(screen.getByText('52:54:00:ab:cd:ef')).toBeInTheDocument();
    expect(screen.queryByText('52:54:00:99:99:99')).not.toBeInTheDocument();
  });

  it('offers Force off once a shutdown has gone unanswered', async () => {
    const user = userEvent.setup();
    const handlers = renderDetail({
      vm: { ...runningVM, phase: 'stopping' },
      canForceStop: true,
    });

    await user.click(screen.getByRole('button', { name: /force off/i }));
    expect(handlers.onForceStop).toHaveBeenCalledWith(runningVM.id);
    expect(handlers.onStop).not.toHaveBeenCalled();
  });

  it('copies the SSH command, and shows a booting VM what it is waiting for', async () => {
    const user = userEvent.setup();
    const booting = { ...runningVM, phase: 'booting' as const, ip: '192.168.64.7', username: 'bob', uptime_seconds: 65 };
    renderDetail({ vm: booting });
    expect(screen.getByRole('status')).toHaveTextContent('Network up · finishing boot · 1:05');
    await user.click(screen.getByRole('button', { name: 'Copy' }));
    expect(await navigator.clipboard.readText()).toBe('ssh bob@192.168.64.7');
    expect(screen.getByRole('button', { name: 'Copied' })).toBeInTheDocument();
  });
});

describe('VMDetail of a service’s VM', () => {
  it('says what the VM is to its service and leads there', async () => {
    const onOpenService = vi.fn();
    renderDetail({
      vm: { ...runningVM, service: 'lab', name: 'lab-agent-1' },
      onOpenService,
      service: {
        name: 'lab',
        recipe: 'kubernetes_cluster',
        recipe_name: 'Kubernetes cluster',
        category: 'containers',
        size: { id: 'medium', label: 'Medium', vcpus: 2, memory_mb: 4096, disk_gb: 30 },
        state: 'ready',
        vms: ['lab-server', 'lab-agent-1'],
        members: [
          { role: 'server', role_label: 'Control plane', vm: 'lab-server', status: 'running' },
          { role: 'agent', role_label: 'Worker', vm: 'lab-agent-1', status: 'running' },
        ],
        created_at_unix: 0,
        outputs: [],
        files: [],
        endpoints: [],
        shared: false,
        vm_user: 'nanocloud',
        vm_password: 'pw',
      },
    });
    expect(screen.getByRole('note')).toHaveTextContent('A worker of lab (Kubernetes cluster). Use it like any VM');
    await userEvent.setup().click(screen.getByRole('button', { name: 'Open lab →' }));
    expect(onOpenService).toHaveBeenCalledWith('lab');
  });

  it('shows no banner on a VM of your own', () => {
    renderDetail();
    expect(screen.queryByText(/Use it like any VM/)).not.toBeInTheDocument();
  });
});
