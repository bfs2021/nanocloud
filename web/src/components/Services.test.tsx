import { describe, it, expect, vi, beforeEach } from 'vitest';
import { cleanup, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { CopyField } from '../ui/CopyField';
import { ServiceCreateDrawer } from './ServiceCreateDrawer';
import { ServiceDetail } from './ServiceDetail';
import { ServicesView } from './ServicesView';
import { MOCK_SERVICE_CATALOG } from '../mock';
import type { NanoEvent, Service } from '../types';
import { CapabilitiesContext, VZ_CAPABILITIES, type Capabilities } from '../capabilities';

vi.mock('../api', () => ({
  createService: vi.fn(),
  serviceFileURL: (name: string, file: string) => `/services/${name}/files/${file}`,
  getServiceLog: vi.fn(async (_name: string, vm: string) => ({
    vm,
    state: 'installing',
    log: `installing\nReading package lists on ${vm}...\n`,
  })),
  stopVM: vi.fn(),
  startVM: vi.fn(),
}));

import { createService, getServiceLog } from '../api';

const postgres = MOCK_SERVICE_CATALOG.find(r => r.id === 'postgres')!;

function service(over: Partial<Service> = {}): Service {
  return {
    name: 'pg-dev',
    recipe: 'postgres',
    recipe_name: 'PostgreSQL',
    category: 'database',
    size: postgres.sizes[1],
    state: 'ready',
    vms: ['pg-dev'],
    ip: '10.0.0.5',
    created_at_unix: 0,
    outputs: [
      { id: 'url', label: 'Connection URL', value: 'postgres://app:s3cr3t@localhost:30432/app', secret: true },
      { id: 'host', label: 'Host', value: 'localhost:30432' },
    ],
    files: [],
    endpoints: [{ id: 'db', label: 'PostgreSQL', port: 5432, host_port: 30432, address: 'localhost:30432', direct: '10.0.0.5:5432' }],
    shared: false,
    vm_user: 'nanocloud',
    vm_password: 'consolepw',
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('CopyField', () => {
  it('hides a secret until asked, and copies it either way', async () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue();
    render(<CopyField label="Password" value="hunter2" secret />);

    expect(screen.queryByText('hunter2')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Copy Password' }));
    expect(writeText).toHaveBeenCalledWith('hunter2');

    await user.click(screen.getByRole('button', { name: 'Show' }));
    expect(screen.getByText('hunter2')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Hide' }));
    expect(screen.queryByText('hunter2')).not.toBeInTheDocument();
  });

  it('shows a plain value as is', () => {
    render(<CopyField label="Host" value="10.0.0.5:5432" />);
    expect(screen.getByText('10.0.0.5:5432')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Show' })).not.toBeInTheDocument();
  });
});

describe('ServiceCreateDrawer', () => {
  function renderDrawer(taken = new Set<string>()) {
    const onCreated = vi.fn();
    const onClose = vi.fn();
    render(<ServiceCreateDrawer recipe={postgres} taken={taken} onClose={onClose} onCreated={onCreated} />);
    return { onCreated, onClose };
  }

  it('suggests a free name and launches with the chosen size and settings', async () => {
    const user = userEvent.setup();
    vi.mocked(createService).mockResolvedValue(service({ name: 'pg-2', state: 'pulling' }));
    const { onCreated } = renderDrawer(new Set(['pg-1']));

    expect(screen.getByLabelText('Name')).toHaveValue('pg-2');
    await user.click(screen.getByRole('radio', { name: /Large/ }));
    await user.clear(screen.getByLabelText('Database'));
    await user.type(screen.getByLabelText('Database'), 'shop');
    await user.click(screen.getByRole('button', { name: /SSH access/ }));
    await user.type(screen.getByLabelText('GitHub username'), 'octocat');
    await user.click(screen.getByRole('button', { name: 'Launch PostgreSQL' }));

    expect(createService).toHaveBeenCalledWith({
      name: 'pg-2',
      recipe: 'postgres',
      size: 'large',
      params: { database: 'shop', user: 'app' },
      github_user: 'octocat',
    });
    expect(onCreated).toHaveBeenCalled();
  });

  it('refuses a name the daemon would refuse, before asking it', async () => {
    const user = userEvent.setup();
    renderDrawer(new Set(['web']));
    const name = screen.getByLabelText('Name');

    await user.clear(name);
    await user.type(name, 'web');
    expect(screen.getByText(/already taken/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Launch PostgreSQL' })).toBeDisabled();

    await user.clear(name);
    await user.type(name, '1bad');
    expect(screen.getByText(/starting with a letter/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Launch PostgreSQL' })).toBeDisabled();
  });

  it('checks a setting against its pattern', async () => {
    const user = userEvent.setup();
    renderDrawer();
    await user.clear(screen.getByLabelText('User'));
    await user.type(screen.getByLabelText('User'), 'Bad User');
    expect(screen.getByText('Use lowercase letters, digits and _.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Launch PostgreSQL' })).toBeDisabled();
  });

  it('never asks for a generated secret', () => {
    renderDrawer();
    expect(screen.queryByLabelText('Password')).not.toBeInTheDocument();
    expect(screen.getByText(/Generated for you: password/)).toBeInTheDocument();
  });

  it('shows the daemon’s refusal and stays open', async () => {
    const user = userEvent.setup();
    vi.mocked(createService).mockRejectedValue(new Error('a VM named "pg-1" already exists'));
    const { onCreated } = renderDrawer();
    await user.click(screen.getByRole('button', { name: 'Launch PostgreSQL' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('already exists');
    expect(onCreated).not.toHaveBeenCalled();
  });

  it('closes on Escape', async () => {
    const user = userEvent.setup();
    const { onClose } = renderDrawer();
    await user.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalled();
  });
});

function user() {
  return userEvent.setup();
}

function renderDetail(svc: Service, localViewer = true, events: NanoEvent[] = [], caps?: Capabilities) {
  const handlers = {
    onBack: vi.fn(),
    onStart: vi.fn(),
    onStop: vi.fn(),
    onDelete: vi.fn(),
    onOpenVM: vi.fn(),
    onShare: vi.fn(),
    onRetry: vi.fn(),
  };
  const detail = <ServiceDetail svc={svc} events={events} busy={false} hostname="mac-studio" localViewer={localViewer} {...handlers} />;
  render(caps ? <CapabilitiesContext.Provider value={caps}>{detail}</CapabilitiesContext.Provider> : detail);
  return handlers;
}

describe('ServiceDetail', () => {
  async function openTab(name: string) {
    await userEvent.setup().click(screen.getByRole('tab', { name }));
  }

  it('shows how to connect to a ready service, secrets hidden', () => {
    renderDetail(service());
    expect(screen.getByRole('tab', { name: 'Overview' })).toHaveAttribute('aria-selected', 'true');
    const connect = screen.getByRole('region', { name: 'Connect' });
    expect(within(connect).getByText('localhost:30432')).toBeInTheDocument();
    expect(within(connect).queryByText(/s3cr3t/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Stop' })).toBeInTheDocument();
  });

  it('lists every address on Access, through nano-cloud and on the VM network', async () => {
    renderDetail(service());
    await user().click(screen.getByRole('button', { name: 'All addresses and logins →' }));
    expect(screen.getByRole('tab', { name: 'Access' })).toHaveAttribute('aria-selected', 'true');
    const addresses = screen.getByRole('region', { name: 'Addresses' });
    expect(within(addresses).getByText('localhost:30432')).toBeInTheDocument();
    expect(within(addresses).getByText('10.0.0.5:5432')).toBeInTheDocument();
    const logins = screen.getByRole('region', { name: 'Log in to the machine' });
    expect(within(logins).getByText('ssh nanocloud@10.0.0.5')).toBeInTheDocument();
  });

  it('tells Mac users how to let their browser reach the VM network', async () => {
    renderDetail(service(), true, [], VZ_CAPABILITIES);
    await openTab('Access');
    const addresses = screen.getByRole('region', { name: 'Addresses' });
    expect(within(addresses).getByText(/Privacy & Security → Local Network/)).toBeInTheDocument();
  });

  it('lets other machines connect when asked', async () => {
    const { onShare } = renderDetail(service());
    await openTab('Access');
    const share = screen.getByRole('switch', { name: 'Reachable from other machines' });
    expect(share).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByText('Off: only mac-studio itself can connect.')).toBeInTheDocument();
    await user().click(share);
    expect(onShare).toHaveBeenCalledWith(true);
  });

  it('tells someone on another computer the addresses are for the host until shared', async () => {
    renderDetail(service(), false);
    await openTab('Access');
    expect(screen.getByRole('note')).toHaveTextContent('These addresses work on mac-studio itself');
    cleanup();
    renderDetail(service({ shared: true }), false);
    await openTab('Access');
    expect(screen.queryByRole('note')).not.toBeInTheDocument();
  });

  it('says why an endpoint is not forwarded', async () => {
    renderDetail(
      service({
        endpoints: [{ id: 'db', label: 'PostgreSQL', port: 5432, host_port: 30432, problem: 'Port 30432 is in use by another program.' }],
      }),
    );
    await openTab('Access');
    expect(screen.getByText('Port 30432 is in use by another program.')).toBeInTheDocument();
  });

  it('opens a web UI through nano-cloud', async () => {
    renderDetail(
      service({
        recipe: 'observability',
        endpoints: [
          { id: 'grafana', label: 'Grafana', scheme: 'http', port: 3000, host_port: 30000, address: 'localhost:30000' },
          { id: 'api', label: 'API', scheme: 'https', port: 6443, host_port: 30443, address: 'localhost:30443' },
        ],
      }),
    );
    await openTab('Access');
    const open = screen.getAllByRole('link', { name: 'Open ↗' });
    expect(open).toHaveLength(1);
    expect(open[0]).toHaveAttribute('href', 'http://localhost:30000');
  });

  it('offers its files as downloads', () => {
    renderDetail(service({ recipe: 'kubernetes', recipe_name: 'Kubernetes', files: [{ id: 'kubeconfig', label: 'kubeconfig' }] }));
    expect(screen.getByRole('link', { name: /Download kubeconfig/ })).toHaveAttribute('href', '/services/pg-dev/files/kubeconfig');
  });

  it('shows the build as it goes, with its live log', async () => {
    renderDetail(service({ state: 'installing', outputs: [] }));
    expect(screen.getByRole('progressbar', { name: 'Boot progress' })).toHaveAttribute('aria-valuetext', 'Install: in progress');
    expect(screen.getByText('Installing PostgreSQL · usually a few minutes')).toBeInTheDocument();
    expect(screen.getByText(/appear here once it is ready/)).toBeInTheDocument();
    const live = screen.getByRole('region', { name: 'Live log' });
    expect(await within(live).findByText(/Reading package lists on pg-dev/)).toBeInTheDocument();
    expect(getServiceLog).toHaveBeenCalledWith('pg-dev', 'pg-dev');
  });

  it('reads each machine of a cluster its own log', async () => {
    renderDetail(
      service({
        name: 'lab',
        state: 'installing',
        outputs: [],
        vms: ['lab-server', 'lab-agent-1'],
        members: [
          { role: 'server', role_label: 'Control plane', vm: 'lab-server', status: 'running', phase: 'ready', ip: '10.0.0.5' },
          { role: 'agent', role_label: 'Worker', vm: 'lab-agent-1', status: 'running', phase: 'ready', ip: '10.0.0.6' },
        ],
      }),
    );
    const live = screen.getByRole('region', { name: 'Live log' });
    expect(await within(live).findByText(/Reading package lists on lab-server/)).toBeInTheDocument();
    await user().click(within(live).getByRole('button', { name: 'agent-1' }));
    expect(await within(live).findByText(/Reading package lists on lab-agent-1/)).toBeInTheDocument();
  });

  it('says why a service failed, and offers to retry', async () => {
    const { onRetry } = renderDetail(
      service({
        state: 'failed',
        failed_in: 'installing',
        reason: 'The install failed at apt-get install.\nE: no space left',
        outputs: [],
      }),
    );
    expect(screen.getByRole('alert')).toHaveTextContent('E: no space left');
    expect(screen.getByRole('alert')).toHaveTextContent('Retry runs the install again on each VM where it failed');
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuetext', 'Install: failed');
    expect(screen.queryByRole('button', { name: 'Stop' })).not.toBeInTheDocument();
    await user().click(screen.getByRole('button', { name: 'Retry' }));
    expect(onRetry).toHaveBeenCalled();
  });

  it('shows its own activity, newest first', async () => {
    const at = '2026-10-26T10:00:00Z';
    renderDetail(service({ name: 'lab', vms: ['lab-server'] }), true, [
      { seq: 1, at, level: 'info', kind: 'service.progress', vm: 'lab', message: "Created lab's VM." },
      { seq: 2, at, level: 'info', kind: 'vm.restarted', vm: 'other', message: 'Restarted other.' },
      { seq: 3, at, level: 'info', kind: 'service.ready', vm: 'lab-server', message: 'Kubernetes lab is ready.' },
    ]);
    await openTab('Activity');
    const items = within(screen.getByRole('region', { name: 'Events' })).getAllByRole('listitem');
    expect(items.map(i => i.textContent)).toEqual([
      expect.stringContaining('Kubernetes lab is ready.'),
      expect.stringContaining("Created lab's VM."),
    ]);
  });

  it('starts a stopped service and opens its VM', async () => {
    const user = userEvent.setup();
    const h = renderDetail(service({ state: 'stopped', outputs: [] }));
    await user.click(screen.getByRole('button', { name: 'Start' }));
    expect(h.onStart).toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: /Open VM/ }));
    expect(h.onOpenVM).toHaveBeenCalledWith('pg-dev');
  });
});

describe('ServicesView', () => {
  it('lists the catalog and the services, and never puts a secret on a card', async () => {
    const user = userEvent.setup();
    const onLaunch = vi.fn();
    const onOpen = vi.fn();
    render(<ServicesView catalog={MOCK_SERVICE_CATALOG} services={[service()]} hostname="h" onLaunch={onLaunch} onOpen={onOpen} />);
    expect(screen.getByText('localhost:30432')).toBeInTheDocument();
    expect(screen.queryByText(/s3cr3t/)).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Launch S3 storage' }));
    expect(onLaunch).toHaveBeenCalledWith(expect.objectContaining({ id: 's3' }));
    await user.click(screen.getByRole('button', { name: 'Open pg-dev' }));
    expect(onOpen).toHaveBeenCalledWith('pg-dev');
  });

  it('shows a failed load rather than an empty list', () => {
    render(
      <ServicesView
        catalog={MOCK_SERVICE_CATALOG}
        services={[]}
        error="connection refused"
        hostname="h"
        onLaunch={vi.fn()}
        onOpen={vi.fn()}
      />,
    );
    expect(screen.getByRole('alert')).toHaveTextContent('connection refused');
  });
});

describe('clusters', () => {
  const cluster = MOCK_SERVICE_CATALOG.find(r => r.id === 'kubernetes_cluster')!;

  it('says what a cluster will create, and follows the worker count', async () => {
    const user = userEvent.setup();
    render(<ServiceCreateDrawer recipe={cluster} taken={new Set()} onClose={vi.fn()} onCreated={vi.fn()} />);
    expect(screen.getByRole('status')).toHaveTextContent('Creates 3 VMs — 1 control plane + 2 workers — each 2 vCPU · 4 GB · 30 GB disk.');

    await user.clear(screen.getByLabelText('Workers'));
    await user.type(screen.getByLabelText('Workers'), '4');
    expect(screen.getByRole('status')).toHaveTextContent('Creates 5 VMs');

    await user.clear(screen.getByLabelText('Workers'));
    await user.type(screen.getByLabelText('Workers'), '9');
    expect(screen.getByText('Use a number from 1 to 5.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Launch Kubernetes cluster' })).toBeDisabled();
  });

  it('says which machine holds a build up, and why', () => {
    const now = Math.floor(Date.now() / 1000);
    renderDetail(
      service({
        name: 'lab',
        recipe: 'kubernetes_cluster',
        recipe_name: 'Kubernetes cluster',
        state: 'installing',
        outputs: [],
        vms: ['lab-server', 'lab-agent-1'],
        members: [
          {
            role: 'server',
            role_label: 'Control plane',
            vm: 'lab-server',
            status: 'running',
            phase: 'ready',
            ip: '10.0.0.5',
            unreachable: 'SSH does not answer at its address (dial tcp 10.0.0.5:22: i/o timeout)',
            unreachable_since: now - 400,
          },
          {
            role: 'agent',
            role_label: 'Worker',
            vm: 'lab-agent-1',
            status: 'running',
            phase: 'ready',
            ip: '10.0.0.6',
            install: 'waiting',
            waits_for: 'Control plane',
          },
        ],
      }),
    );
    expect(screen.getByText('Waiting on lab-server: not answering over SSH for 6 min')).toBeInTheDocument();
    const server = screen.getByRole('button', { name: 'Open VM lab-server' });
    expect(server).toHaveAccessibleDescription('Not answering over SSH for 6 min');
    expect(server).toHaveTextContent('dial tcp 10.0.0.5:22: i/o timeout');
    // The cure is a control of its own, beside the row's button and not inside it.
    expect(server).not.toHaveTextContent('restart it');
    expect(within(server).queryByRole('button')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Restart VM' })).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Restart VM' })).toHaveLength(1);
    expect(screen.getByRole('button', { name: 'Open VM lab-agent-1' })).toHaveAccessibleDescription('Waiting for the control plane');
  });

  it('says how far each machine of a failed cluster got, not just that its VM runs', () => {
    const why = 'this worker cannot reach the control plane at https://10.0.0.5:6443: traffic between the VMs does not get through';
    renderDetail(
      service({
        name: 'lab',
        recipe: 'kubernetes_cluster',
        recipe_name: 'Kubernetes cluster',
        state: 'failed',
        failed_in: 'installing',
        reason: `On lab-agent-1, ${why}.\nlab-agent-2 failed the same way.`,
        outputs: [],
        vms: ['lab-server', 'lab-agent-1', 'lab-agent-2', 'lab-agent-3'],
        members: [
          {
            role: 'server',
            role_label: 'Control plane',
            vm: 'lab-server',
            status: 'running',
            phase: 'ready',
            ip: '10.0.0.5',
            install: 'ready',
          },
          {
            role: 'agent',
            role_label: 'Worker',
            vm: 'lab-agent-1',
            status: 'running',
            phase: 'ready',
            ip: '10.0.0.6',
            install: 'failed',
            failure: why,
          },
          {
            role: 'agent',
            role_label: 'Worker',
            vm: 'lab-agent-2',
            status: 'running',
            phase: 'ready',
            ip: '10.0.0.7',
            install: 'failed',
            failure: why,
          },
          // Never heard from, as after a restart of the daemon.
          { role: 'agent', role_label: 'Worker', vm: 'lab-agent-3', status: 'running', phase: 'ready', ip: '10.0.0.8' },
        ],
      }),
    );
    expect(screen.getByRole('button', { name: 'Open VM lab-server' })).toHaveAccessibleDescription('Installed');
    for (const vm of ['lab-agent-1', 'lab-agent-2']) {
      const worker = screen.getByRole('button', { name: `Open VM ${vm}` });
      expect(worker).toHaveAccessibleDescription('Install failed');
      expect(worker).toHaveTextContent(
        'This worker cannot reach the control plane at https://10.0.0.5:6443: traffic between the VMs does not get through.',
      );
      expect(worker).not.toHaveTextContent('restart it');
    }
    // A failed install is not cured by a power cycle.
    expect(screen.queryByRole('button', { name: 'Restart VM' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Open VM lab-agent-3' })).toHaveAccessibleDescription('VM running');
  });

  it('lists every machine of a cluster with its role', async () => {
    const user = userEvent.setup();
    const { onOpenVM } = renderDetail(
      service({
        name: 'lab',
        recipe: 'kubernetes_cluster',
        recipe_name: 'Kubernetes cluster',
        vms: ['lab-server', 'lab-agent-1', 'lab-agent-2'],
        members: [
          { role: 'server', role_label: 'Control plane', vm: 'lab-server', status: 'running', phase: 'ready', ip: '10.0.0.5' },
          { role: 'agent', role_label: 'Worker', vm: 'lab-agent-1', status: 'running', phase: 'ready', ip: '10.0.0.6' },
          { role: 'agent', role_label: 'Worker', vm: 'lab-agent-2', status: 'missing' },
        ],
      }),
    );
    expect(screen.getByRole('heading', { name: 'Machines (3)' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Open VM lab-agent-1' })).toHaveTextContent('Worker · 10.0.0.6');
    expect(screen.getByRole('button', { name: 'Open VM lab-agent-2' })).toBeDisabled();
    await user.click(screen.getByRole('button', { name: 'Open VM lab-server' }));
    expect(onOpenVM).toHaveBeenCalledWith('lab-server');
  });

  it('shows the machine count on its tile', () => {
    render(<ServicesView catalog={[cluster]} services={[]} hostname="h" onLaunch={vi.fn()} onOpen={vi.fn()} />);
    expect(screen.getByText('3 VMs · from 2 vCPU · 2 GB each')).toBeInTheDocument();
    expect(screen.getByText('1 control plane + 2 workers, by default')).toBeInTheDocument();
  });
});
