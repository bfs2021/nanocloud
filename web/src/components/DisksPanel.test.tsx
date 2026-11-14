import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { VMSpec, VMStatus } from '../types';
import { DisksPanel } from './DisksPanel';
import { attachDisk, detachDisk, resizeDisk } from '../api';

vi.mock('../api', () => ({
  attachDisk: vi.fn(),
  detachDisk: vi.fn(),
  resizeDisk: vi.fn(),
}));

const boot = { target: 'vda', path: '/pool/web.qcow2', size_gb: 20, boot: true };
const data = { target: 'vdb', path: '/pool/web-vdb.qcow2', size_gb: 50, boot: false };

function spec(disks = [boot, data]): VMSpec {
  return {
    pci_devices: [],
    usb_devices: [],
    cpu: { mode: 'host-model', topology: null, cpu_set: '' },
    network: { mode: 'nat', bridge: '', vlan_id: 0 },
    disks,
    restart_required: false,
    warnings: [],
  };
}

function renderPanel(status: VMStatus = 'stopped', disks = [boot, data]) {
  const onChanged = vi.fn();
  render(<DisksPanel vmId="web" status={status} spec={spec(disks)} onChanged={onChanged} onError={vi.fn()} />);
  return { onChanged };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('DisksPanel', () => {
  // The boot disk's path is derived from the pool and the VM name, so there is
  // nothing in the spec to detach.
  it('offers no way to detach the boot disk', async () => {
    renderPanel();

    expect(screen.getAllByRole('button', { name: 'Detach' })).toHaveLength(1);
    expect(screen.getByText('BOOT')).toBeInTheDocument();
  });

  // qemu-img cannot write to an image QEMU holds open. Letting the click through
  // returns a lock error phrased in terms of neither the VM nor its state.
  it('will not resize while the VM is running, and says why', async () => {
    renderPanel('running');

    const resize = screen.getAllByRole('button', { name: 'Resize' })[0];
    expect(resize).toBeDisabled();
    expect(resize).toHaveAttribute('title', expect.stringContaining('Stop the VM first'));
  });

  // Growing a disk is the one operation whose obvious reading is wrong: the
  // block device is bigger and the filesystem on it is not, until someone grows
  // the partition from inside the guest.
  it('states that the guest filesystem is not grown with the disk', async () => {
    renderPanel();

    await userEvent.click(screen.getAllByRole('button', { name: 'Resize' })[0]);

    expect(screen.getByText(/not the filesystem on it/i)).toBeInTheDocument();
    expect(screen.getByText(/grow the partition inside it/i)).toBeInTheDocument();
  });

  it('refuses to send a resize that is not a growth', async () => {
    renderPanel();

    await userEvent.click(screen.getAllByRole('button', { name: 'Resize' })[0]);
    const size = screen.getByLabelText('new size for vda');
    await userEvent.clear(size);
    await userEvent.type(size, '20');

    expect(screen.getByRole('button', { name: 'Grow' })).toBeDisabled();
    expect(resizeDisk).not.toHaveBeenCalled();
  });

  // Two buttons, not a checkbox: whether the data survives is the whole
  // decision, and it should be the thing being clicked.
  it('makes keeping or deleting the file an explicit choice', async () => {
    const { onChanged } = renderPanel();
    vi.mocked(detachDisk).mockResolvedValue(spec([boot]));

    await userEvent.click(screen.getByRole('button', { name: 'Detach' }));
    await userEvent.click(screen.getByRole('button', { name: /keep the file/i }));

    expect(detachDisk).toHaveBeenCalledWith('web', 'vdb', false);
    expect(onChanged).toHaveBeenCalledWith(spec([boot]));
  });

  it('deletes the file only on the destructive button', async () => {
    renderPanel();
    vi.mocked(detachDisk).mockResolvedValue(spec([boot]));

    await userEvent.click(screen.getByRole('button', { name: 'Detach' }));
    await userEvent.click(screen.getByRole('button', { name: /delete the file/i }));

    expect(detachDisk).toHaveBeenCalledWith('web', 'vdb', true);
  });

  it('adds a disk of the requested size', async () => {
    const { onChanged } = renderPanel();
    const next = spec([boot, data, { target: 'vdc', path: '/pool/web-vdc.qcow2', size_gb: 5, boot: false }]);
    vi.mocked(attachDisk).mockResolvedValue(next);

    await userEvent.click(screen.getByRole('button', { name: /add disk/i }));
    const size = screen.getByLabelText('new disk size in GB');
    await userEvent.clear(size);
    await userEvent.type(size, '5');
    await userEvent.click(screen.getByRole('button', { name: 'Create' }));

    expect(attachDisk).toHaveBeenCalledWith('web', 5);
    expect(onChanged).toHaveBeenCalledWith(next);
  });
});
