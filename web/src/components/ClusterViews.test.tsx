import { describe, it, expect, vi, beforeEach } from 'vitest';
import { cleanup, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ServiceDetail } from './ServiceDetail';
import { schemeOf } from './ClusterViews';
import { MOCK_SERVICE_CATALOG } from '../mock';
import type { Cluster, Service } from '../types';

vi.mock('../api', () => ({
  serviceFileURL: (name: string, file: string) => `/services/${name}/files/${file}`,
  getServiceLog: vi.fn(async (_name: string, vm: string) => ({ vm, state: 'ready', log: 'ready\n' })),
  getServiceCluster: vi.fn(),
  applyManifest: vi.fn(),
}));

import { applyManifest, getServiceCluster } from '../api';

const recipe = MOCK_SERVICE_CATALOG.find(r => r.id === 'kubernetes_cluster')!;
const now = Math.floor(Date.now() / 1000);

function kube(over: Partial<Service> = {}): Service {
  return {
    name: 'lab',
    recipe: 'kubernetes_cluster',
    recipe_name: 'Kubernetes cluster',
    category: 'containers',
    size: recipe.sizes[1],
    state: 'ready',
    ready_at_unix: now - 600,
    vms: ['lab-server', 'lab-agent-1', 'lab-agent-2'],
    members: [
      { role: 'server', role_label: 'Control plane', vm: 'lab-server', status: 'running', phase: 'ready', ip: '10.0.0.10' },
      { role: 'agent', role_label: 'Worker', vm: 'lab-agent-1', status: 'running', phase: 'ready', ip: '10.0.0.11' },
      { role: 'agent', role_label: 'Worker', vm: 'lab-agent-2', status: 'running', phase: 'ready', ip: '10.0.0.12' },
    ],
    ip: '10.0.0.10',
    created_at_unix: now - 900,
    outputs: [],
    files: [],
    endpoints: [{ id: 'api', label: 'Kubernetes API', port: 6443, host_port: 30443, address: 'localhost:30443' }],
    shared: false,
    vm_user: 'nanocloud',
    vm_password: 'pw',
    console: 'kubernetes',
    scale: { role: 'agent', label: 'Worker', count: 2 },
    ...over,
  };
}

const cluster: Cluster = {
  version: 'v1.36.5+k3s1',
  nodes: [
    {
      name: 'lab-server',
      roles: ['control-plane'],
      ready: true,
      version: 'v1.36.5+k3s1',
      ip: '10.0.0.10',
      pods: 9,
      cpu_percent: 31,
      memory_percent: 47,
    },
    { name: 'lab-agent-1', roles: [], ready: true, version: 'v1.36.5+k3s1', ip: '10.0.0.11', pods: 3, cpu_percent: 12, memory_percent: 30 },
    { name: 'lab-agent-2', roles: [], ready: false, version: 'v1.36.5+k3s1', ip: '10.0.0.12', pods: 0 },
  ],
  deployments: [
    { namespace: 'default', name: 'web', ready: 3, desired: 3, images: ['nginx:1.27'], created_unix: now - 7200 },
    { namespace: 'kube-system', name: 'coredns', ready: 1, desired: 1, images: ['coredns:1.12'], created_unix: now - 10800 },
  ],
  exposed: [
    { namespace: 'default', name: 'web', type: 'NodePort', ports: [{ name: 'http', port: 80, node_port: 30080, protocol: 'TCP' }] },
    { namespace: 'default', name: 'db', type: 'NodePort', ports: [{ port: 5432, node_port: 30432, protocol: 'TCP' }] },
  ],
  pods: { total: 13, running: 12, pending: 1, failed: 0, succeeded: 0 },
  read_at_unix: now,
};

function renderDetail(svc: Service, handlers: Partial<Parameters<typeof ServiceDetail>[0]> = {}) {
  const props = {
    onShare: vi.fn(),
    onRetry: vi.fn(),
    onBack: vi.fn(),
    onStart: vi.fn(),
    onStop: vi.fn(),
    onDelete: vi.fn(),
    onOpenVM: vi.fn(),
    onAddWorker: vi.fn(),
    onRemoveWorker: vi.fn(),
    onPublish: vi.fn(),
    onUnpublish: vi.fn(),
    ...handlers,
  };
  render(<ServiceDetail svc={svc} busy={false} hostname="mac" localViewer {...props} />);
  return props;
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(getServiceCluster).mockResolvedValue(cluster);
});

describe('Kubernetes console', () => {
  it('adds Nodes and Workloads tabs only for a Kubernetes service', async () => {
    const { unmount } = render(
      <ServiceDetail
        svc={kube({ console: undefined })}
        busy={false}
        hostname="mac"
        localViewer
        onShare={vi.fn()}
        onRetry={vi.fn()}
        onBack={vi.fn()}
        onStart={vi.fn()}
        onStop={vi.fn()}
        onDelete={vi.fn()}
        onOpenVM={vi.fn()}
      />,
    );
    expect(screen.queryByRole('tab', { name: 'Nodes' })).not.toBeInTheDocument();
    expect(getServiceCluster).not.toHaveBeenCalled();
    unmount();

    renderDetail(kube());
    expect(screen.getAllByRole('tab').map(t => t.textContent)).toEqual(['Overview', 'Nodes', 'Workloads', 'Access', 'Activity']);
    await screen.findByText('2 / 3');
  });

  it('sums the cluster up on the Overview and goes to its nodes', async () => {
    const user = userEvent.setup();
    renderDetail(kube());
    const summary = await screen.findByRole('region', { name: 'Cluster' });
    expect(await within(summary).findByText('2 / 3')).toBeInTheDocument();
    expect(within(summary).getByText('12 · 1 pending')).toBeInTheDocument();
    expect(within(summary).getByText('v1.36.5+k3s1')).toBeInTheDocument();
    expect(getServiceCluster).toHaveBeenCalledWith('lab');

    await user.click(within(summary).getByRole('button', { name: 'Manage nodes →' }));
    expect(screen.getByRole('tab', { name: 'Nodes' })).toHaveAttribute('aria-selected', 'true');
  });

  it('lists nodes with their state and load', async () => {
    const user = userEvent.setup();
    renderDetail(kube());
    await user.click(screen.getByRole('tab', { name: 'Nodes' }));
    expect(await screen.findByText('3 nodes · 1 control plane, 2 workers')).toBeInTheDocument();

    const row = (name: string) => screen.getByRole('cell', { name }).closest('tr')!;
    expect(await within(row('lab-server')).findByText('Ready')).toBeInTheDocument();
    expect(within(row('lab-agent-2')).getByText('Not ready')).toBeInTheDocument();
    expect(within(row('lab-server')).getByRole('meter', { name: 'CPU of lab-server' })).toHaveAttribute('aria-valuenow', '31');
    // The control plane cannot be removed; workers can.
    expect(within(row('lab-server')).queryByRole('button', { name: /Remove/ })).not.toBeInTheDocument();
    expect(within(row('lab-agent-1')).getByRole('button', { name: 'Remove lab-agent-1' })).toBeEnabled();
  });

  it('adds a worker and removes one only after confirming', async () => {
    const user = userEvent.setup();
    const props = renderDetail(kube());
    await user.click(screen.getByRole('tab', { name: 'Nodes' }));

    await user.click(screen.getByRole('button', { name: '+ Add worker' }));
    expect(props.onAddWorker).toHaveBeenCalledTimes(1);

    await user.click(screen.getByRole('button', { name: 'Remove lab-agent-2' }));
    expect(props.onRemoveWorker).not.toHaveBeenCalled();
    const dialog = screen.getByRole('alertdialog', { name: 'Remove lab-agent-2?' });
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Remove lab-agent-2' }));
    await user.click(screen.getByRole('button', { name: 'Drain and remove' }));
    expect(props.onRemoveWorker).toHaveBeenCalledWith('lab-agent-2');
  });

  it('holds changes while a worker is leaving, and keeps the last worker', async () => {
    const user = userEvent.setup();
    renderDetail(kube({ removing: ['lab-agent-2'] }));
    await user.click(screen.getByRole('tab', { name: 'Nodes' }));
    expect(await screen.findByText('Leaving · moving its pods')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '+ Add worker' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Remove lab-agent-1' })).toBeDisabled();
    cleanup();
    renderDetail(
      kube({
        scale: { role: 'agent', label: 'Worker', count: 1 },
        vms: ['lab-server', 'lab-agent-1'],
        members: kube().members!.slice(0, 2),
      }),
    );
    await user.click(screen.getByRole('tab', { name: 'Nodes' }));
    expect(screen.queryByRole('button', { name: /^Remove / })).not.toBeInTheDocument();
  });

  it('lists deployments by namespace and applies a manifest', async () => {
    const user = userEvent.setup();
    vi.mocked(applyManifest).mockResolvedValue({ ok: true, output: 'deployment.apps/hello created' });
    renderDetail(kube());
    await user.click(screen.getByRole('tab', { name: 'Workloads' }));

    const table = await screen.findByRole('table', { name: 'Deployments' });
    expect(within(table).getByText('web')).toBeInTheDocument();
    expect(within(table).getByText('coredns')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'default' }));
    expect(within(table).queryByText('coredns')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Apply a manifest' }));
    const apply = screen.getByRole('button', { name: 'Apply' });
    expect(apply).toBeDisabled();
    await user.type(screen.getByLabelText('Manifest (YAML)'), 'kind: Deployment');
    await user.click(apply);
    expect(applyManifest).toHaveBeenCalledWith('lab', 'kind: Deployment');
    expect(await screen.findByRole('status')).toHaveTextContent('deployment.apps/hello created');
    // The cluster is read again so the new deployment shows.
    expect(vi.mocked(getServiceCluster).mock.calls.length).toBeGreaterThanOrEqual(2);
  });

  it('shows what kubectl said when a manifest is refused', async () => {
    const user = userEvent.setup();
    vi.mocked(applyManifest).mockResolvedValue({ ok: false, output: 'error: error parsing STDIN' });
    renderDetail(kube());
    await user.click(screen.getByRole('tab', { name: 'Workloads' }));
    await user.click(await screen.findByRole('button', { name: 'Apply a manifest' }));
    await user.type(screen.getByLabelText('Manifest (YAML)'), 'nope');
    await user.click(screen.getByRole('button', { name: 'Apply' }));
    expect(await screen.findByRole('status')).toHaveTextContent('error: error parsing STDIN');
  });

  it('publishes NodePorts and unpublishes them, even once gone from the cluster', async () => {
    const user = userEvent.setup();
    const svc = kube({
      endpoints: [
        ...kube().endpoints,
        { id: 'np30432', label: 'default/db', port: 30432, host_port: 30432, address: 'localhost:30432', published: true },
        { id: 'np31000', label: 'default/old', port: 31000, host_port: 31000, address: 'localhost:31000', published: true },
      ],
    });
    const props = renderDetail(svc);
    await user.click(screen.getByRole('tab', { name: 'Workloads' }));

    await user.click(await screen.findByRole('button', { name: 'Publish default/web' }));
    expect(props.onPublish).toHaveBeenCalledWith(30080, 'default/web', 'http');

    expect(screen.getByText('localhost:30432')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Unpublish default/db' }));
    expect(props.onUnpublish).toHaveBeenCalledWith(30432);

    expect(screen.getByText(/no longer in the cluster/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Unpublish default/old' }));
    expect(props.onUnpublish).toHaveBeenCalledWith(31000);
  });

  it('does not read a stopped cluster', async () => {
    const user = userEvent.setup();
    renderDetail(kube({ state: 'stopped' }));
    await user.click(screen.getByRole('tab', { name: 'Nodes' }));
    expect(screen.getByText('Start the service to see its cluster.')).toBeInTheDocument();
    expect(getServiceCluster).not.toHaveBeenCalled();
  });

  it('says when the cluster cannot be read', async () => {
    vi.mocked(getServiceCluster).mockRejectedValue(new Error('lab-server is not up yet'));
    renderDetail(kube());
    const summary = await screen.findByRole('region', { name: 'Cluster' });
    expect(await within(summary).findByText('lab-server is not up yet')).toBeInTheDocument();
  });
});

describe('schemeOf', () => {
  it('guesses web ports, and leaves the rest alone', () => {
    expect(schemeOf(80)).toBe('http');
    expect(schemeOf(9000, 'http')).toBe('http');
    expect(schemeOf(443)).toBe('https');
    expect(schemeOf(5432)).toBeUndefined();
  });
});
