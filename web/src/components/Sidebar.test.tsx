import { describe, it, expect, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Sidebar } from './Sidebar';
import type { Service } from '../types';

function svc(name: string, state: Service['state']): Service {
  return {
    name,
    recipe: 'postgres',
    recipe_name: 'PostgreSQL',
    category: 'database',
    size: { id: 'medium', label: 'Medium', vcpus: 2, memory_mb: 2048, disk_gb: 20 },
    state,
    vms: [name],
    created_at_unix: 0,
    outputs: [],
    files: [],
    endpoints: [],
    shared: false,
    vm_user: 'nanocloud',
    vm_password: 'pw',
  };
}

describe('Sidebar services', () => {
  it('lists the services, says which need a look, and opens one', async () => {
    const onOpenService = vi.fn();
    render(
      <Sidebar
        view="service"
        onNavigate={vi.fn()}
        onNewVM={vi.fn()}
        cpuPercent={0}
        memUsedGb={0}
        memTotalGb={0}
        hostname="h"
        services={[svc('pg-dev', 'ready'), svc('cluster-2', 'installing'), svc('broken', 'failed')]}
        currentService="pg-dev"
        onOpenService={onOpenService}
      />,
    );
    const list = screen.getByRole('navigation', { name: 'Your services' });
    expect(within(list).getByRole('button', { name: 'pg-dev' })).toHaveAttribute('aria-current', 'page');
    expect(within(list).getByRole('button', { name: 'cluster-2 installing' })).toBeInTheDocument();
    expect(within(list).getByRole('button', { name: 'broken failed' })).toBeInTheDocument();
    await userEvent.setup().click(within(list).getByRole('button', { name: 'cluster-2 installing' }));
    expect(onOpenService).toHaveBeenCalledWith('cluster-2');
  });

  it('says when an alert fires about a service', () => {
    const alerting = {
      ...svc('pg-dev', 'ready'),
      alerts: [{ name: 'DiskFillingUp', title: 'Disk filling up', summary: 'full', severity: 'warning' }],
    };
    render(
      <Sidebar
        view="dashboard"
        onNavigate={vi.fn()}
        onNewVM={vi.fn()}
        cpuPercent={0}
        memUsedGb={0}
        memTotalGb={0}
        hostname="h"
        services={[alerting, svc('quiet', 'ready')]}
        onOpenService={vi.fn()}
      />,
    );
    const list = screen.getByRole('navigation', { name: 'Your services' });
    expect(within(list).getByRole('button', { name: 'pg-dev alert' })).toBeInTheDocument();
    expect(within(list).getByRole('button', { name: 'quiet' })).toBeInTheDocument();
  });

  it('lists nothing without services', () => {
    render(
      <Sidebar
        view="dashboard"
        onNavigate={vi.fn()}
        onNewVM={vi.fn()}
        cpuPercent={0}
        memUsedGb={0}
        memTotalGb={0}
        hostname="h"
        services={[]}
        onOpenService={vi.fn()}
      />,
    );
    expect(screen.queryByRole('navigation', { name: 'Your services' })).not.toBeInTheDocument();
  });
});
