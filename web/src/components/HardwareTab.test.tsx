import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { HostDeviceReport, PCIDevice, USBDevice, VMSpec } from '../types';
import { HardwareTab } from './HardwareTab';
import { getHostDevices, getSystemInfo, getVMSpec, patchVMSpec } from '../api';

vi.mock('../api', () => ({
  getHostDevices: vi.fn(),
  getSystemInfo: vi.fn(),
  getVMSpec: vi.fn(),
  patchVMSpec: vi.fn(),
}));

const mockedDevices = vi.mocked(getHostDevices);
const mockedSystem = vi.mocked(getSystemInfo);
const mockedSpec = vi.mocked(getVMSpec);
const mockedPatch = vi.mocked(patchVMSpec);

// The fixture VM has 4 vCPUs, so 4 is the product a topology has to reach.
const VCPUS = 4;

const DEFAULT_CPU: VMSpec['cpu'] = { mode: 'host-model', topology: null, cpu_set: '' };

function pci(overrides: Partial<PCIDevice> = {}): PCIDevice {
  return {
    address: '0000:01:00.0',
    vendor: 'NVIDIA Corporation',
    product: 'GP107 [GeForce GTX 1050 Ti]',
    class: 'VGA compatible controller',
    driver: 'vfio-pci',
    iommu_group: 13,
    boot_vga: false,
    siblings: [],
    usable: true,
    ...overrides,
  };
}

function usb(overrides: Partial<USBDevice> = {}): USBDevice {
  return {
    vendor_id: '0x1050',
    product_id: '0x0407',
    description: 'Yubico.com YubiKey OTP+FIDO+CCID',
    bus_seen: 'bus 1 device 5',
    ...overrides,
  };
}

const EMPTY_SPEC: VMSpec = {
  pci_devices: [],
  usb_devices: [],
  cpu: DEFAULT_CPU,
  network: { mode: 'nat', bridge: '', vlan_id: 0 },
  disks: [],
  restart_required: false,
  warnings: [],
};

function host(overrides: Partial<HostDeviceReport> = {}): HostDeviceReport {
  return {
    iommu: { enabled: true, groups_seen: 24, message: 'IOMMU is enabled; the kernel reports 24 device groups' },
    pci: [],
    usb: [],
    ...overrides,
  };
}

// Both mount-time fetches have to settle before any assertion — every test here
// is about the resolved state, not the loading frame.
async function renderTab(report: HostDeviceReport, spec: VMSpec) {
  mockedDevices.mockResolvedValue(report);
  mockedSpec.mockResolvedValue(spec);
  mockedSystem.mockResolvedValue({ hostname: 'h', cpu_percent: 0, memory_used_mb: 0, memory_total_mb: 0, cpus: 8 });
  const view = render(<HardwareTab vmId="gpu-vm" vcpus={VCPUS} onError={() => {}} />);
  // The picker's own section label, which only renders once the inventory has
  // resolved. Exact, not a regex: "No PCI devices reported for this host" would
  // match a loose one, and a host reporting nothing is a case under test.
  await screen.findByText('PCI devices');
  return view;
}

// Both cards on this tab have checkboxes in them, so device rows are looked up
// inside the passthrough card rather than by their position on the page.
function deviceBoxes(): HTMLElement[] {
  const card = screen.getByRole('heading', { name: 'Passthrough devices' }).parentElement as HTMLElement;
  return within(card).getAllByRole('checkbox');
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('HardwareTab', () => {
  // A change rebuilds the domain, which the guest picks up only at its next
  // boot, so the tab must not present a new device as live.
  it('says a restart is needed whenever the spec and the running machine differ', async () => {
    await renderTab(host({ pci: [pci()] }), { ...EMPTY_SPEC, restart_required: true });

    expect(screen.getByText(/restart to apply/i)).toBeInTheDocument();
    expect(screen.getByText(/only picks up when it next boots/i)).toBeInTheDocument();
  });

  it('says nothing about restarting when the machine already matches its spec', async () => {
    await renderTab(host({ pci: [pci()] }), EMPTY_SPEC);

    expect(screen.queryByText(/restart to apply/i)).not.toBeInTheDocument();
  });

  // A device is never refused without a reason.
  it('offers an unusable device unselectable, with the reason attached', async () => {
    const reason =
      'IOMMU group 0 also contains 0000:00:00.0 (Host bridge, which cannot be released by the host); a group is passed through as one unit';
    await renderTab(host({ pci: [pci({ usable: false, reason })] }), EMPTY_SPEC);

    // The chipset functions that can never move sit behind a disclosure;
    // nothing is dropped.
    await userEvent.click(screen.getByRole('button', { name: /cannot release/i }));

    expect(deviceBoxes()[0]).toBeDisabled();
    expect(screen.getByText(reason)).toBeInTheDocument();
  });

  // The IOMMU-group rule stated before the click rather than as a save error.
  it('warns that selecting a device drags its IOMMU group along', async () => {
    await renderTab(host({ pci: [pci({ siblings: ['0000:01:00.1'] })] }), EMPTY_SPEC);

    expect(screen.getByText(/also attaches 1 other device in IOMMU group 13/i)).toBeInTheDocument();
    expect(screen.getByText(/0000:01:00\.1/)).toBeInTheDocument();
  });

  // PCI passthrough needs the kernel's IOMMU; USB does not. Disabling both, or
  // neither, would misstate what this host can actually do.
  it('blocks PCI but not USB when the host has IOMMU off', async () => {
    await renderTab(
      host({
        iommu: { enabled: false, groups_seen: 0, message: 'IOMMU is off — PCI passthrough is unavailable on this host' },
        pci: [pci()],
        usb: [usb()],
      }),
      EMPTY_SPEC,
    );

    const boxes = deviceBoxes();
    expect(boxes[0]).toBeDisabled();
    expect(boxes[1]).toBeEnabled();
  });

  // The response is authoritative, not the click: the backend expands a PCI
  // pick to its whole IOMMU group, so rendering the optimistic selection would
  // show one device where the VM now has two.
  it('re-seeds the selection from the saved spec, not from what was clicked', async () => {
    await renderTab(
      host({
        pci: [pci({ siblings: ['0000:01:00.1'] }), pci({ address: '0000:01:00.1', product: 'HD Audio', siblings: ['0000:01:00.0'] })],
      }),
      EMPTY_SPEC,
    );

    mockedPatch.mockResolvedValue({
      pci_devices: [{ address: '0000:01:00.0' }, { address: '0000:01:00.1' }],
      usb_devices: [],
      cpu: DEFAULT_CPU,
      network: { mode: 'nat', bridge: '', vlan_id: 0 },
      disks: [],
      restart_required: true,
      warnings: [],
    });

    await userEvent.click(deviceBoxes()[0]);
    await userEvent.click(screen.getByRole('button', { name: /apply changes/i }));

    // Both lists are always sent, so clearing every device stays expressible.
    expect(mockedPatch).toHaveBeenCalledWith('gpu-vm', {
      pci_devices: ['0000:01:00.0'],
      usb_devices: [],
      cpu: DEFAULT_CPU,
    });

    const boxes = deviceBoxes();
    expect(boxes[0]).toBeChecked();
    expect(boxes[1]).toBeChecked();
    expect(await screen.findByText(/restart to apply/i)).toBeInTheDocument();
  });

  // Nothing to apply means nothing to press: an enabled button here would send
  // a redefine for a spec that has not changed.
  it('keeps Apply inert until the selection actually differs', async () => {
    await renderTab(host({ usb: [usb()] }), EMPTY_SPEC);

    expect(screen.getByRole('button', { name: /apply changes/i })).toBeDisabled();
    await userEvent.click(deviceBoxes()[0]);
    expect(screen.getByRole('button', { name: /apply changes/i })).toBeEnabled();
  });

  // libvirt refuses a topology that disagrees with the vCPU count, without
  // naming either field.
  it('refuses to save a topology that does not multiply out to the vCPU count', async () => {
    await renderTab(host(), EMPTY_SPEC);

    await userEvent.click(screen.getByRole('checkbox', { name: /fixed socket/i }));
    // Opens valid — 4 sockets for 4 vCPUs — so the error is only ever one the
    // user actually made.
    expect(screen.getByRole('button', { name: /apply changes/i })).toBeEnabled();

    await userEvent.clear(screen.getByLabelText('sockets'));
    await userEvent.type(screen.getByLabelText('sockets'), '3');

    expect(screen.getByText(/3 × 1 × 1 is 3 vCPUs, but this VM has 4/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /apply changes/i })).toBeDisabled();
  });

  // The host's CPU count is on screen, so a cpuset naming a missing CPU is
  // caught here rather than by a 422.
  it('refuses a cpuset naming CPUs this host does not have', async () => {
    await renderTab(host(), EMPTY_SPEC);

    await userEvent.type(screen.getByLabelText('cpu_set'), '0-15');

    expect(screen.getByText(/This host has 8 CPUs, numbered 0-7, so CPU 15 does not exist/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /apply changes/i })).toBeDisabled();
  });

  it('sends the whole CPU shape when the mode changes', async () => {
    await renderTab(host(), EMPTY_SPEC);
    mockedPatch.mockResolvedValue({ ...EMPTY_SPEC, cpu: { ...DEFAULT_CPU, mode: 'host-passthrough' }, restart_required: true });

    await userEvent.click(screen.getByRole('radio', { name: /host-passthrough/i }));
    await userEvent.click(screen.getByRole('button', { name: /apply changes/i }));

    expect(mockedPatch).toHaveBeenCalledWith('gpu-vm', {
      pci_devices: [],
      usb_devices: [],
      cpu: { mode: 'host-passthrough', topology: null, cpu_set: '' },
    });
    // A CPU change rebuilds the domain like any other, so it reaches the guest
    // at its next boot and the badge has to say so.
    expect(await screen.findByText(/restart to apply/i)).toBeInTheDocument();
  });

  // A boot-VGA card is passable and still usually the wrong choice, so the
  // caveat has to survive a reload — it comes back from the server on read.
  it('renders the server warnings about the current selection', async () => {
    await renderTab(host({ pci: [pci({ boot_vga: true })] }), {
      ...EMPTY_SPEC,
      pci_devices: [{ address: '0000:01:00.0' }],
      warnings: ["0000:01:00.0 (NVIDIA) is the card driving this host's console."],
    });

    expect(screen.getByText(/is the card driving this host's console/i)).toBeInTheDocument();
  });
});
