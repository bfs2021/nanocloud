import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ManagedDeleteDialog } from './ManagedDeleteDialog';
import { MOCK_VMS } from '../mock';
import type { Service } from '../types';

const lab: Service = {
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
};
const agent = { ...MOCK_VMS[0], id: 'lab-agent-1', name: 'lab-agent-1', service: 'lab' };

function renderDialog() {
  const handlers = { onOpenService: vi.fn(), onDeleteVM: vi.fn(), onClose: vi.fn() };
  render(<ManagedDeleteDialog vm={agent} service={lab} {...handlers} />);
  return handlers;
}

describe('ManagedDeleteDialog', () => {
  it('says what the VM is to its service and points there first', async () => {
    const { onOpenService } = renderDialog();
    const dialog = screen.getByRole('dialog', { name: 'Delete lab-agent-1?' });
    expect(dialog).toHaveTextContent('This VM is a worker of lab (Kubernetes cluster).');
    expect(screen.getByRole('button', { name: 'Open lab' })).toHaveFocus();
    await userEvent.setup().click(screen.getByRole('button', { name: 'Open lab' }));
    expect(onOpenService).toHaveBeenCalled();
  });

  it('deletes the VM alone only once asked to, deliberately', async () => {
    const user = userEvent.setup();
    const { onDeleteVM } = renderDialog();
    const anyway = screen.getByRole('button', { name: 'Delete VM anyway' });
    expect(anyway).toBeDisabled();
    await user.click(screen.getByRole('checkbox', { name: /Delete only this VM/ }));
    await user.click(anyway);
    expect(onDeleteVM).toHaveBeenCalled();
  });

  it('closes on Escape', async () => {
    const { onClose } = renderDialog();
    await userEvent.setup().keyboard('{Escape}');
    expect(onClose).toHaveBeenCalled();
  });
});
