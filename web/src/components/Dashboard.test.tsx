import { describe, it, expect, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Dashboard } from './Dashboard';
import { MOCK_VMS, MOCK_STORAGE_POOLS } from '../mock';
import type { Service } from '../types';

function renderDashboard(overrides: Partial<Parameters<typeof Dashboard>[0]> = {}) {
  const handlers = {
    onStart: vi.fn(),
    onStop: vi.fn(),
    onDelete: vi.fn(),
    onOpenDetail: vi.fn(),
    onNewVM: vi.fn(),
    onOpenSettings: vi.fn(),
    onLayoutChange: vi.fn(),
  };
  render(
    <Dashboard
      vms={MOCK_VMS}
      pools={MOCK_STORAGE_POOLS}
      hostname="test-host"
      cpuPercent={25}
      memUsedMb={4096}
      memTotalMb={16384}
      leaseMap={{}}
      statsMap={{}}
      readinessChecks={[]}
      layout="cards"
      {...handlers}
      {...overrides}
    />,
  );
  return handlers;
}

describe('Dashboard', () => {
  // The phase tag rides on the card, not one click away in the detail view.
  it.each([
    { vm: { network_mode: 'bridge' as const, vlan_id: 20 }, label: 'Bridge · VLAN 20' },
    { vm: { network_mode: 'bridge' as const, vlan_id: 0 }, label: 'Bridge' },
    { vm: { network_mode: 'nat' as const, vlan_id: 0 }, label: 'NAT' },
  ])('labels the network $label', ({ vm, label }) => {
    renderDashboard({ vms: [{ ...MOCK_VMS[0], ...vm }] });
    expect(screen.getByText(new RegExp(`· ${label}$`))).toBeInTheDocument();
  });

  it('renders every VM with its status', () => {
    renderDashboard();
    for (const vm of MOCK_VMS) {
      expect(screen.getByText(vm.name)).toBeInTheDocument();
    }
    expect(screen.getByRole('heading', { name: 'Virtual machines' })).toBeInTheDocument();
    expect(screen.getByText('test-host')).toBeInTheDocument();
  });

  it('computes the running/stopped stat cards from the VM list', () => {
    renderDashboard();
    // Status badges also say "Running"/"Stopped" (inside spans); the stat card
    // labels are divs, so scope the query by selector.
    const runningCard = screen.getByText('Running', { selector: 'div' }).parentElement!;
    const stoppedCard = screen.getByText('Stopped', { selector: 'div' }).parentElement!;
    // MOCK_VMS: 3 running, 1 stopped, 1 error (counted as not running).
    expect(within(runningCard).getByText('3')).toBeInTheDocument();
    expect(within(stoppedCard).getByText('2')).toBeInTheDocument();
    const totalCard = screen.getByText('Total VMs').parentElement!;
    expect(within(totalCard).getByText(String(MOCK_VMS.length))).toBeInTheDocument();
  });

  it('shows Start for stopped VMs and wires it to onStart', async () => {
    const user = userEvent.setup();
    const stopped = MOCK_VMS.find(v => v.status === 'stopped')!;
    const handlers = renderDashboard({ vms: [stopped] });
    await user.click(screen.getByRole('button', { name: /start/i }));
    expect(handlers.onStart).toHaveBeenCalledWith(stopped.id);
  });

  it('shows Stop for running VMs and wires it to onStop', async () => {
    const user = userEvent.setup();
    const running = MOCK_VMS.find(v => v.status === 'running')!;
    const handlers = renderDashboard({ vms: [running] });
    await user.click(screen.getByRole('button', { name: /stop/i }));
    expect(handlers.onStop).toHaveBeenCalledWith(running.id);
  });

  it('renders the empty state with a working New VM button', async () => {
    const user = userEvent.setup();
    const handlers = renderDashboard({ vms: [] });
    expect(screen.getByText('No virtual machines yet')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '+ New VM' }));
    expect(handlers.onNewVM).toHaveBeenCalled();
  });

  it('walks a new install through its first VM, from live state', async () => {
    const user = userEvent.setup();
    const failing = { name: 'libvirt', ok: false, required: true, message: 'libvirtd is not running' };
    const handlers = renderDashboard({ vms: [], readinessChecks: [failing], cachedImageCount: 0 });
    expect(screen.getByText(/One requirement needs attention/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '+ New VM' })).toBeDisabled();
    await user.click(screen.getByRole('button', { name: 'See what is missing' }));
    expect(handlers.onOpenSettings).toHaveBeenCalled();
  });

  it('ticks off the image step once one is downloaded', () => {
    renderDashboard({ vms: [], cachedImageCount: 2 });
    expect(screen.getByText('2 downloaded.')).toBeInTheDocument();
    expect(screen.getByText('Download a cloud image')).toHaveTextContent('(done)');
  });

  it('distinguishes a VM that never got an address from one that is still booting', () => {
    // A running VM with no IP is "Booting" only while booting explains it. Past
    // that the backend sends "no-network", and the badge stops implying progress.
    const running = MOCK_VMS.find(v => v.status === 'running' && v.phase === 'ready')!;
    const stuck = { ...running, phase: 'no-network' as const, ip: undefined };
    renderDashboard({ vms: [stuck] });

    expect(screen.getByText('No network')).toBeInTheDocument();
    expect(screen.queryByText('Booting')).not.toBeInTheDocument();
  });

  it('shows the SSH command once a lease IP is known, even if the backend has not resolved vm.ip/phase yet', () => {
    // The SSH row shows whenever a username and an IP from either source are
    // known, regardless of vm.phase: the lease IP can arrive before the backend
    // confirms the address.
    const running = MOCK_VMS.find(v => v.status === 'running' && v.phase === 'ready')!;
    const vm = { ...running, phase: 'booting' as const, ip: undefined, username: 'bob' };
    renderDashboard({ vms: [vm], leaseMap: { [vm.name]: '10.0.0.42' } });
    expect(screen.getByTitle('Copy SSH command')).toHaveTextContent('ssh bob@10.0.0.42');
  });

  it('prefers the address the backend resolved for this boot over the lease list', () => {
    const running = MOCK_VMS.find(v => v.status === 'running' && v.phase === 'ready')!;
    const vm = { ...running, ip: '192.168.64.7', username: 'bob' };
    renderDashboard({ vms: [vm], leaseMap: { [vm.name]: '192.168.64.3' } });
    expect(screen.getByTitle('Copy SSH command')).toHaveTextContent('ssh bob@192.168.64.7');
  });

  it('says what a booting VM is waiting for, and nothing once it is ready', () => {
    const running = MOCK_VMS.find(v => v.status === 'running' && v.phase === 'ready')!;
    const booting = { ...running, id: 'b', name: 'booting-vm', phase: 'booting' as const, ip: undefined, uptime_seconds: 42 };
    renderDashboard({ vms: [booting, running] });
    expect(screen.getAllByRole('status')).toHaveLength(1);
    expect(screen.getByRole('status')).toHaveTextContent('Starting · waiting for network · 0:42');
  });

  it('reports the layout choice via onLayoutChange rather than switching itself', async () => {
    // Layout lives in App so it survives Dashboard unmounting; Dashboard only
    // reports clicks.
    const user = userEvent.setup();
    const handlers = renderDashboard();
    await user.click(screen.getByRole('button', { name: 'Table' }));
    expect(handlers.onLayoutChange).toHaveBeenCalledWith('table');
  });

  it('renders the table layout when layout="table"', () => {
    renderDashboard({ layout: 'table' });
    // The table layout renders column headers that the cards layout lacks.
    expect(screen.getByText('Name')).toBeInTheDocument();
    expect(screen.getByText('Status')).toBeInTheDocument();
    for (const vm of MOCK_VMS) {
      expect(screen.getByText(vm.name)).toBeInTheDocument();
    }
  });

  it('renders an error state instead of the empty state when the VM list failed', () => {
    renderDashboard({ vms: [], vmsError: 'connection refused' });
    expect(screen.getByRole('alert')).toHaveTextContent(/connection refused/);
    expect(screen.queryByText('No virtual machines yet')).not.toBeInTheDocument();
  });

  it('offers Force off for a VM whose graceful shutdown went unanswered', async () => {
    const running = MOCK_VMS.find(v => v.status === 'running')!;
    const onForceStop = vi.fn();
    renderDashboard({
      vms: [{ ...running, phase: 'stopping' }],
      forceOffIds: new Set([running.id]),
      onForceStop,
    });

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /force off/i }));
    expect(onForceStop).toHaveBeenCalledWith(running.id);
  });

  it('shows a disabled "Stopping…" button while a shutdown is outstanding', () => {
    const running = MOCK_VMS.find(v => v.status === 'running')!;
    // No entry in forceOffIds — the request is recent, so no escape hatch yet.
    renderDashboard({ vms: [{ ...running, phase: 'stopping' }], forceOffIds: new Set() });

    const stopButton = screen.getByRole('button', { name: /stopping/i });
    expect(stopButton).toBeDisabled();
    expect(screen.queryByRole('button', { name: /force off/i })).not.toBeInTheDocument();
  });
});

describe('Dashboard: your VMs and services’ VMs', () => {
  const svc: Service = {
    name: 'lab',
    recipe: 'kubernetes_cluster',
    recipe_name: 'Kubernetes cluster',
    category: 'containers',
    size: { id: 'medium', label: 'Medium', vcpus: 2, memory_mb: 4096, disk_gb: 30 },
    state: 'ready',
    vms: ['lab-server', 'lab-agent-1'],
    created_at_unix: 0,
    outputs: [],
    files: [],
    endpoints: [],
    shared: false,
    vm_user: 'nanocloud',
    vm_password: 'pw',
  };
  const mine = { ...MOCK_VMS[0], id: 'dev-box', name: 'dev-box', service: undefined };
  const server = { ...MOCK_VMS[0], id: 'lab-server', name: 'lab-server', service: 'lab' };
  const agent = { ...MOCK_VMS[0], id: 'lab-agent-1', name: 'lab-agent-1', service: 'lab' };

  it('keeps a service’s VMs apart, under the service', async () => {
    const onOpenService = vi.fn();
    renderDashboard({ vms: [server, mine, agent], services: [svc], onOpenService });
    const yours = screen.getByRole('region', { name: 'Your VMs' });
    expect(within(yours).getByText('dev-box')).toBeInTheDocument();
    expect(within(yours).queryByText('lab-server')).not.toBeInTheDocument();

    const group = screen.getByRole('group', { name: 'VMs of lab' });
    expect(within(group).getByText('lab-server')).toBeInTheDocument();
    expect(within(group).getByText('lab-agent-1')).toBeInTheDocument();
    expect(within(group).getByText('Kubernetes cluster')).toBeInTheDocument();
    // The group says which service; its cards do not repeat it.
    expect(within(group).queryByText(/Part of service/)).not.toBeInTheDocument();
    await userEvent.setup().click(within(group).getByRole('button', { name: 'Open lab →' }));
    expect(onOpenService).toHaveBeenCalledWith('lab');
  });

  it('filters to one kind', async () => {
    const user = userEvent.setup();
    renderDashboard({ vms: [server, mine, agent], services: [svc] });
    expect(screen.getByRole('button', { name: 'Managed by services 2' })).toHaveAttribute('aria-pressed', 'false');
    await user.click(screen.getByRole('button', { name: 'Yours 1' }));
    expect(screen.queryByRole('region', { name: 'Managed by services' })).not.toBeInTheDocument();
    expect(screen.getByText('dev-box')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Managed by services 2' }));
    expect(screen.queryByRole('region', { name: 'Your VMs' })).not.toBeInTheDocument();
    expect(screen.getByText('lab-server')).toBeInTheDocument();
  });

  it('shows no sections while no VM runs a service', () => {
    renderDashboard({ vms: [mine] });
    expect(screen.queryByRole('region', { name: 'Your VMs' })).not.toBeInTheDocument();
    expect(screen.queryByRole('group', { name: 'Show' })).not.toBeInTheDocument();
    expect(screen.getByText('dev-box')).toBeInTheDocument();
  });
});
