import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, within, fireEvent } from '@testing-library/react';
import { ServiceDetail } from './ServiceDetail';
import { AlertBanner, LineChart, formatBytes, niceMax, retentionText } from './MonitoringViews';
import { MOCK_SERVICE_CATALOG } from '../mock';
import type { Monitoring, Service } from '../types';

vi.mock('../api', () => ({
  serviceFileURL: (name: string, file: string) => `/services/${name}/files/${file}`,
  getServiceLog: vi.fn(async (_name: string, vm: string) => ({ vm, state: 'ready', log: 'ready\n' })),
  getServiceMonitoring: vi.fn(),
}));

import { getServiceMonitoring } from '../api';

const recipe = MOCK_SERVICE_CATALOG.find(r => r.id === 'observability')!;
const now = Math.floor(Date.now() / 1000);

function obs(over: Partial<Service> = {}): Service {
  return {
    name: 'obs',
    recipe: 'observability',
    recipe_name: recipe.name,
    category: 'monitoring',
    size: recipe.sizes[1],
    state: 'ready',
    ready_at_unix: now - 600,
    vms: ['obs'],
    ip: '10.0.0.5',
    created_at_unix: now - 900,
    outputs: [],
    files: [],
    endpoints: [
      { id: 'grafana', label: 'Grafana', port: 3000, host_port: 30000, address: 'localhost:30000', scheme: 'http' },
      { id: 'prometheus', label: 'Prometheus', port: 9090, host_port: 30090, address: 'localhost:30090', scheme: 'http' },
    ],
    shared: false,
    vm_user: 'nanocloud',
    vm_password: 'pw',
    console: 'monitoring',
    ...over,
  };
}

const reading: Monitoring = {
  targets_up: 3,
  targets_total: 4,
  vms: 2,
  services: 2,
  watching: [
    { service: 'db', recipe: 'postgres', vms: 1, jobs: ['node', 'postgres'], up: 1, total: 2 },
    { service: 'obs', recipe: 'observability', vms: 1, jobs: ['node', 'prometheus'], up: 2, total: 2 },
  ],
  rules: [
    { name: 'VM down', when: 'no answer for 2 min', state: 'ok' },
    { name: 'Service unreachable', when: 'an endpoint stops answering', state: 'firing' },
  ],
  firing: 1,
  stored_bytes: 2_254_857_830,
  retention: '15d',
  cpu: [
    {
      name: 'db',
      points: [
        [now - 120, 10],
        [now - 60, 20],
        [now, 30],
      ],
    },
    {
      name: 'obs',
      points: [
        [now - 120, 5],
        [now - 60, 5],
        [now, 8],
      ],
    },
  ],
  dashboards: [
    { uid: 'nanocloud-overview', title: 'nano-cloud overview', about: 'Every VM', home: true },
    { uid: 'nanocloud-postgres', title: 'PostgreSQL', about: 'Connections', var: 'db', recipes: ['postgres'] },
  ],
  provisioned: true,
  setup: [{ service: 'db', vm: 'db', what: 'PostgreSQL metrics', problem: 'E: Unable to locate package' }],
  read_at_unix: now,
};

function renderDetail(svc: Service) {
  render(
    <ServiceDetail
      svc={svc}
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
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(getServiceMonitoring).mockResolvedValue(reading);
});

describe('monitoring console', () => {
  it('shows what it watches, in numbers', async () => {
    renderDetail(obs());
    await screen.findByText('3 / 4');
    const numbers = screen.getByRole('region', { name: 'Monitoring' });
    expect(within(numbers).getByText('3 / 4')).toBeInTheDocument();
    expect(within(numbers).getByText('2 VMs')).toBeInTheDocument();
    expect(within(numbers).getByText('in 2 services')).toBeInTheDocument();
    expect(within(numbers).getByText('2 rules on')).toBeInTheDocument();
    expect(within(numbers).getByText('2.1 GB')).toBeInTheDocument();
    expect(within(numbers).getByText('15 days kept')).toBeInTheDocument();
    expect(getServiceMonitoring).toHaveBeenCalledWith('obs');
  });

  it('links each dashboard in Grafana, and each service to its own', async () => {
    renderDetail(obs());
    const home = await screen.findByRole('link', { name: 'Open nano-cloud overview in Grafana' });
    expect(home).toHaveAttribute('href', 'http://localhost:30000/d/nanocloud-overview');
    expect(screen.getByRole('link', { name: 'PostgreSQL dashboard for db' })).toHaveAttribute(
      'href',
      'http://localhost:30000/d/nanocloud-postgres?var-db=db',
    );
    expect(screen.getByText('HOME')).toBeInTheDocument();
  });

  it('lists the watched services, the setups that failed and the rules with their state', async () => {
    renderDetail(obs());
    const watching = await screen.findByRole('region', { name: 'What it watches' });
    expect(within(watching).getByText('1 VM · node, PostgreSQL')).toBeInTheDocument();
    expect(within(watching).getByText('1 of 2 up')).toBeInTheDocument();
    expect(within(watching).getByText(/Could not set up PostgreSQL metrics on db: E: Unable to locate package/)).toBeInTheDocument();
    const rules = screen.getByRole('region', { name: 'Alert rules' });
    expect(within(rules).getByText('OK')).toBeInTheDocument();
    expect(within(rules).getByText('Firing')).toBeInTheDocument();
  });

  it('says when its dashboards are still being set up', async () => {
    vi.mocked(getServiceMonitoring).mockResolvedValue({ ...reading, provisioned: false });
    renderDetail(obs());
    expect(await screen.findByText(/Setting up the dashboards and alert rules on obs/)).toBeInTheDocument();
  });

  it('says why Prometheus cannot be read', async () => {
    vi.mocked(getServiceMonitoring).mockRejectedValue(new Error("obs's Prometheus does not answer: connection refused"));
    renderDetail(obs());
    expect(await screen.findByText("obs's Prometheus does not answer: connection refused")).toBeInTheDocument();
  });

  it('does not read a monitoring service that is not ready', () => {
    renderDetail(obs({ state: 'installing', ready_at_unix: undefined }));
    expect(getServiceMonitoring).not.toHaveBeenCalled();
    expect(screen.queryByRole('region', { name: 'Monitoring' })).not.toBeInTheDocument();
  });
});

describe('AlertBanner', () => {
  it('lists the alerts firing about a service', () => {
    render(
      <AlertBanner
        alerts={[
          { name: 'DiskFillingUp', title: 'Disk filling up', summary: "db's disk is 91% full", severity: 'warning' },
          { name: 'VMDown', title: 'VM down', summary: 'db has not answered for 2 minutes', severity: 'critical' },
        ]}
      />,
    );
    const banner = screen.getByRole('region', { name: 'Alerts firing' });
    expect(within(banner).getByRole('heading')).toHaveTextContent('2 alerts firing');
    expect(within(banner).getByText(/db's disk is 91% full/)).toBeInTheDocument();
  });

  it('shows on the page of the service it is about', () => {
    renderDetail(
      obs({
        console: undefined,
        alerts: [{ name: 'VMDown', title: 'VM down', summary: 'obs has not answered for 2 minutes', severity: 'critical' }],
      }),
    );
    expect(screen.getByRole('region', { name: 'Alerts firing' })).toHaveTextContent('obs has not answered for 2 minutes');
  });
});

describe('LineChart', () => {
  const series = reading.cpu;

  it('draws a line per series with a legend and a table of the same numbers', () => {
    const { container } = render(<LineChart title="CPU" series={series} />);
    expect(container.querySelectorAll('polyline')).toHaveLength(2);
    expect(screen.getByRole('img', { name: /CPU\. db 30% now, obs 8% now/ })).toBeInTheDocument();
    const table = screen.getByRole('table');
    const db = within(table).getByText('db').closest('tr')!;
    expect(
      within(db)
        .getAllByRole('cell')
        .map(c => c.textContent),
    ).toEqual(['db', '30%', '20%', '30%']);
  });

  it('reads each minute with the arrow keys', () => {
    render(<LineChart title="CPU" series={series} />);
    const chart = screen.getByRole('img');
    fireEvent.keyDown(chart, { key: 'ArrowRight' });
    expect(screen.getByRole('status')).toHaveTextContent('now');
    fireEvent.keyDown(chart, { key: 'ArrowLeft' });
    const tip = screen.getByRole('status');
    expect(tip).toHaveTextContent('1 min ago');
    expect(tip).toHaveTextContent('20%db');
    fireEvent.keyDown(chart, { key: 'Escape' });
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('waits for enough data', () => {
    render(<LineChart title="CPU" series={[{ name: 'db', points: [[now, 3]] }]} />);
    expect(screen.getByText(/Not enough data yet/)).toBeInTheDocument();
  });
});

describe('formatting', () => {
  it('says sizes, retention and chart tops the way people read them', () => {
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(2_254_857_830)).toBe('2.1 GB');
    expect(formatBytes(40 * 1024 ** 3)).toBe('40 GB');
    expect(retentionText('15d')).toBe('15 days kept');
    expect(retentionText('720h')).toBe('30 days kept');
    expect(retentionText('1w')).toBe('1 week kept');
    expect(retentionText('')).toBeNull();
    expect(niceMax(37, true)).toBe(50);
    expect(niceMax(130, true)).toBe(200);
    expect(niceMax(3.2, false)).toBe(5);
  });
});
