import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { CapabilitiesContext, LIBVIRT_CAPABILITIES, VZ_CAPABILITIES } from '../capabilities';
import { VMDetail } from './VMDetail';
import { NetworkPicker } from './CreateVMModal';
import { SerialConsole } from './SerialConsole';
import { MOCK_VMS } from '../mock';

// What a macOS (Apple Virtualization) daemon's UI shows differently from a
// Linux one. Every other test renders without a provider, which is the
// libvirt set — so these are the only tests that see the vz capabilities.

vi.mock('../api', () => ({
  getVMStats: vi.fn().mockResolvedValue({ cpu_percent: 0, memory_used_mb: 0, memory_total_mb: 0 }),
  getStatsHistory: vi.fn().mockResolvedValue([]),
  listSnapshots: vi.fn().mockResolvedValue([]),
  createSnapshot: vi.fn(),
  deleteSnapshot: vi.fn(),
  restoreSnapshot: vi.fn(),
  consoleWebSocketURL: vi.fn().mockReturnValue('ws://localhost/vms/web/console'),
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

// xterm needs a real canvas; the terminal is stubbed and its traffic recorded.
const term = vi.hoisted(() => ({
  written: [] as (string | Uint8Array)[],
  onData: null as ((d: string) => void) | null,
}));
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    loadAddon() {}
    open() {}
    focus() {}
    dispose() {}
    write(d: string | Uint8Array) {
      term.written.push(d);
    }
    onData(cb: (d: string) => void) {
      term.onData = cb;
      return { dispose() {} };
    }
  },
}));
vi.mock('@xterm/addon-fit', () => ({
  FitAddon: class {
    fit() {}
  },
}));
vi.mock('@xterm/xterm/css/xterm.css', () => ({}));
// noVNC is never loaded on macOS; stubbed so a wrong choice fails loudly.
vi.mock('@novnc/novnc', () => ({
  default: class {
    constructor() {
      throw new Error('VNC console used on a serial-console backend');
    }
  },
}));

class FakeWebSocket {
  static OPEN = 1;
  static last: FakeWebSocket | null = null;
  readyState = 0;
  binaryType = 'blob';
  sent: unknown[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: (() => void) | null = null;
  constructor(public url: string) {
    FakeWebSocket.last = this;
  }
  send(d: unknown) {
    this.sent.push(d);
  }
  close() {}
}

import { listSnapshots } from '../api';

function onVZ(node: ReactNode) {
  return <CapabilitiesContext.Provider value={VZ_CAPABILITIES}>{node}</CapabilitiesContext.Provider>;
}

const runningVM = MOCK_VMS.find(v => v.status === 'running')!;
const handlers = { onBack: vi.fn(), onStart: vi.fn(), onStop: vi.fn(), onForceStop: vi.fn(), onError: vi.fn(), onRefreshVMs: vi.fn() };

beforeEach(() => {
  vi.clearAllMocks();
  term.written = [];
  term.onData = null;
  vi.stubGlobal('WebSocket', FakeWebSocket);
});

describe('macOS backend', () => {
  it('opens a serial console instead of VNC', async () => {
    const user = userEvent.setup();
    render(onVZ(<VMDetail vm={runningVM} leases={[]} bridges={[]} uplink="" {...handlers} />));
    await user.click(screen.getByRole('tab', { name: /console/i }));
    expect(await screen.findByTestId('serial-console')).toBeInTheDocument();
    expect(FakeWebSocket.last?.url).toBe('ws://localhost/vms/web/console');
  });

  it('has no hardware tab, and offers snapshots', async () => {
    const user = userEvent.setup();
    render(onVZ(<VMDetail vm={runningVM} leases={[]} bridges={[]} uplink="" {...handlers} />));
    expect(screen.queryByRole('tab', { name: /^hardware$/i })).not.toBeInTheDocument();
    expect(screen.getByText('Apple Virtualization')).toBeInTheDocument();
    await user.click(screen.getByRole('tab', { name: /storage/i }));
    expect(await screen.findByText('No snapshots yet for this VM.')).toBeVisible();
    expect(listSnapshots).toHaveBeenCalledWith(runningVM.id);
  });

  it('hides snapshots from a backend that cannot take them', async () => {
    const user = userEvent.setup();
    render(
      <CapabilitiesContext.Provider value={{ ...VZ_CAPABILITIES, snapshots: false }}>
        <VMDetail vm={runningVM} leases={[]} bridges={[]} uplink="" {...handlers} />
      </CapabilitiesContext.Provider>,
    );
    await user.click(screen.getByRole('tab', { name: /storage/i }));
    expect(screen.queryByText('No snapshots yet for this VM.')).not.toBeVisible();
    expect(listSnapshots).not.toHaveBeenCalled();
  });

  it('keeps the Linux tabs on libvirt', () => {
    render(
      <CapabilitiesContext.Provider value={LIBVIRT_CAPABILITIES}>
        <VMDetail vm={runningVM} leases={[]} bridges={[]} uplink="eth0" {...handlers} />
      </CapabilitiesContext.Provider>,
    );
    expect(screen.getByRole('tab', { name: /^hardware$/i })).toBeInTheDocument();
    expect(screen.getByText('QEMU/KVM')).toBeInTheDocument();
  });

  it('offers no bridged networking, even with an uplink', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(
      onVZ(
        <NetworkPicker
          value={{ mode: 'nat', bridge: '', vlan_id: 0 }}
          bridges={[{ name: 'br0', uplink: 'en0' }]}
          uplink="en0"
          onChange={onChange}
        />,
      ),
    );
    await user.click(screen.getByText('Your network (bridge)'));
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByLabelText(/Your network \(bridge\)/)).toBeDisabled();
    expect(screen.getByText('A Mac runs VMs on NAT only.')).toBeInTheDocument();
    expect(screen.queryByText(/Set up a bridge on this host first/)).not.toBeInTheDocument();
  });

  it('relays serial bytes both ways as binary', () => {
    render(<SerialConsole vmId="web" />);
    const ws = FakeWebSocket.last!;
    expect(ws.binaryType).toBe('arraybuffer');
    act(() => {
      ws.readyState = FakeWebSocket.OPEN;
      ws.onopen?.();
      ws.onmessage?.({ data: new TextEncoder().encode('web login: ').buffer });
    });
    expect(new TextDecoder().decode(term.written[0] as Uint8Array)).toBe('web login: ');
    expect(screen.queryByText(/Connecting to serial console/)).not.toBeInTheDocument();

    act(() => term.onData?.('root\r'));
    expect(ws.sent).toHaveLength(1);
    // A typed array (binary frame), not a string (text frame), which the
    // daemon's proxy would reject. jsdom's Uint8Array is another realm's.
    expect(ArrayBuffer.isView(ws.sent[0])).toBe(true);
    expect(new TextDecoder().decode(ws.sent[0] as Uint8Array)).toBe('root\r');

    act(() => ws.onclose?.());
    expect(screen.getByText('Serial console disconnected.')).toBeInTheDocument();
  });
});
