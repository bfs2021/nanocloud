import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { HostDeviceReport, PCIDevice } from '../types';
import { HostDevices } from './HostDevices';
import { MOCK_HOST_DEVICES } from '../mock';
import { getHostDevices } from '../api';

vi.mock('../api', () => ({ getHostDevices: vi.fn() }));

const mockedGet = vi.mocked(getHostDevices);

// Wait for the mount-time fetch to settle before asserting; every test here is
// about what the resolved report renders, not the loading frame.
async function renderReport(report: HostDeviceReport) {
  mockedGet.mockResolvedValue(report);
  const view = render(<HostDevices />);
  await screen.findByRole('heading', { name: /passthrough devices/i });
  await screen.findByText(report.iommu.message);
  return view;
}

// Devices that answer "no" sit behind a disclosure, so anything asserting on
// one has to open it first.
async function revealEveryDevice() {
  const toggle = screen.queryByRole('button', { name: /show the other/i });
  if (toggle) await userEvent.click(toggle);
}

const IOMMU_OFF_REMEDY =
  'Add intel_iommu=on (Intel) or amd_iommu=on iommu=pt (AMD) to GRUB_CMDLINE_LINUX in /etc/default/grub, then run: sudo update-grub && sudo reboot';

const EMPTY_HOST: HostDeviceReport = {
  iommu: {
    enabled: false,
    groups_seen: 0,
    message: 'IOMMU is off — PCI passthrough is unavailable on this host',
    remedy: IOMMU_OFF_REMEDY,
  },
  pci: [],
  usb: [],
};

function pciDevice(overrides: Partial<PCIDevice> = {}): PCIDevice {
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

function reportWith(pci: PCIDevice[]): HostDeviceReport {
  return { ...MOCK_HOST_DEVICES, pci };
}

beforeEach(() => {
  mockedGet.mockReset();
});

describe('HostDevices', () => {
  it('leads with the IOMMU state and offers its remedy as a copyable command', async () => {
    await renderReport(EMPTY_HOST);

    expect(screen.getByText(EMPTY_HOST.iommu.message)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /copy command/i })).toHaveTextContent('GRUB_CMDLINE_LINUX');
  });

  it('does not offer a remedy when the IOMMU is already on', async () => {
    await renderReport({ ...MOCK_HOST_DEVICES, pci: [], usb: [] });

    expect(screen.getByText(MOCK_HOST_DEVICES.iommu.message)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /copy command/i })).not.toBeInTheDocument();
  });

  // A device is never dimmed without saying why, and the verdict is advice:
  // the backend's strongest reason is a risk, not a refusal.
  it.each(MOCK_HOST_DEVICES.pci.filter(d => !d.usable).map(d => [d.address, d] as const))(
    'shows the reason next to unusable device %s',
    async (_address, dev) => {
      await renderReport(reportWith([dev]));
      await revealEveryDevice();

      // Rendered exactly as the backend composed it — no re-capitalisation,
      // which would mangle a reason opening on a driver name.
      expect(screen.getByText(dev.reason!)).toBeInTheDocument();
      expect(screen.getByText('Not recommended')).toBeInTheDocument();
      expect(screen.queryByText('Passable')).not.toBeInTheDocument();
    },
  );

  it('warns about a boot-VGA card separately from the passable verdict', async () => {
    const bootVGA = pciDevice({ boot_vga: true, product: 'HD Graphics 530', driver: 'i915' });
    await renderReport(reportWith([bootVGA]));

    // Orthogonal verdicts: the card is passable and is still the wrong choice.
    expect(screen.getByText('Passable')).toBeInTheDocument();
    expect(screen.getByText(/driving the host's own display/i)).toBeInTheDocument();
  });

  it('states an IOMMU group as the consequence of passing the device through', async () => {
    const gpu = pciDevice({ siblings: ['0000:01:00.1'], iommu_group: 13 });
    await renderReport(reportWith([gpu]));

    const note = screen.getByText(/moves together with 1 other device in IOMMU group 13/i);
    // The addresses are context for the sentence, never the sentence itself.
    expect(note).toHaveTextContent('0000:01:00.1');
  });

  it('renders an unknown group and an unbound driver as their meaning, not as -1 and ""', async () => {
    const bare = pciDevice({ driver: '', iommu_group: -1 });
    const { container } = await renderReport(reportWith([bare]));

    expect(screen.getByText(/no driver bound/)).toBeInTheDocument();
    expect(screen.getByText(/IOMMU group unknown/)).toBeInTheDocument();
    expect(container.textContent).not.toContain('-1');
  });

  // libvirt can report every PCI vendor and product as an empty string while
  // filling them for USB. The class alone can't tell two cards apart, so the
  // numeric IDs come first.
  it('identifies a device libvirt did not name by its numeric IDs, not just its class', async () => {
    const nameless = pciDevice({ vendor: '', product: '', vendor_id: '10de', product_id: '13ba' });
    await renderReport(reportWith([nameless]));

    expect(screen.getByText(/\[10de:13ba\]/)).toBeInTheDocument();
    // The class still qualifies it, but no longer stands in as its identity.
    expect(screen.getByText(/VGA compatible controller/)).toBeInTheDocument();
  });

  // test:///default and macOS report no devices, but a real board has 25–60 PCI
  // functions, and the two that matter must not get lost among them.
  it('leads with the count and holds the rest back when a host reports dozens of devices', async () => {
    const wall = [
      pciDevice({ address: '0000:01:00.0', usable: true }),
      ...Array.from({ length: 33 }, (_, i) =>
        pciDevice({
          address: `0000:00:${String(i).padStart(2, '0')}.0`,
          product: `Chipset function ${i}`,
          usable: false,
          reason: 'this device is a Host bridge, which cannot be released by the host',
        }),
      ),
    ];
    await renderReport(reportWith(wall));

    expect(screen.getByText(/of 34 PCI devices on this host can be passed through/)).toBeInTheDocument();
    expect(screen.getAllByText('Passable')).toHaveLength(1);
    expect(screen.queryAllByText('Not recommended')).toHaveLength(0);

    // Held back, never dropped: every reason is still one click away.
    await userEvent.click(screen.getByRole('button', { name: /show the other 33 devices/i }));
    expect(screen.getAllByText('Not recommended')).toHaveLength(33);
  });

  it('renders a host with nothing to pass through as a calm, complete screen', async () => {
    await renderReport(EMPTY_HOST);

    expect(screen.getByText('No PCI devices reported for this host.')).toBeInTheDocument();
    expect(screen.getByText('No USB devices reported for this host.')).toBeInTheDocument();
    expect(screen.queryByText(/reading host devices/i)).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('identifies USB devices by vendor:product and demotes the bus it was seen on', async () => {
    await renderReport(MOCK_HOST_DEVICES);
    const usb = MOCK_HOST_DEVICES.usb[0];

    expect(screen.getByText(`${usb.vendor_id}:${usb.product_id}`)).toBeInTheDocument();
    expect(screen.getByText(new RegExp(`Seen at ${usb.bus_seen}`, 'i'))).toBeInTheDocument();
  });

  // A 503 means libvirt is unreachable, which the readiness checks already report.
  it('degrades quietly when the endpoint fails, without competing with the readiness banner', async () => {
    mockedGet.mockRejectedValue(new Error('libvirt: connection refused'));
    render(<HostDevices />);

    expect(await screen.findByText(/couldn't read this host's devices/i)).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.queryByText(/libvirt/i)).not.toBeInTheDocument();
  });
});
