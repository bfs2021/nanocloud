// Mock data layer — only used when VITE_MOCK=true
// Production always hits the real API.

import type {
  VM,
  VMStats,
  Snapshot,
  CachedImage,
  CatalogImage,
  StoragePool,
  HostBridge,
  NetworkInterface,
  NetworkLease,
  SystemDeps,
  HostDeviceReport,
  NanoEvent,
  StatsSample,
  VMSpec,
  VMSpecPatch,
  VMTemplate,
  Service,
  ServiceLog,
  Cluster,
  ServiceRecipe,
  CreateServicePayload,
  AssignedPCIDevice,
  AssignedUSBDevice,
  Monitoring,
  UpdateStatus,
  Settings,
  APIToken,
} from './types';

export const MOCK_VMS: VM[] = [
  {
    id: 'vm1',
    name: 'web-prod-01',
    status: 'running',
    phase: 'ready',
    uptime_seconds: 3 * 86400 + 4521,
    username: 'admin',
    cpu_count: 4,
    memory_mb: 8192,
    os: 'ubuntu',
    disk_gb: 80,
    pool_name: 'local-ssd',
    pool_path: '/var/lib/nano/images',
    network_mode: 'nat',
    ip: '192.168.122.12',
    mac: '52:54:00:a1:b2:c3',
  },
  {
    id: 'vm2',
    name: 'db-main',
    status: 'running',
    phase: 'ready',
    uptime_seconds: 21 * 86400 + 900,
    username: 'admin',
    cpu_count: 8,
    memory_mb: 32768,
    os: 'debian',
    disk_gb: 500,
    pool_name: 'nvme-fast',
    pool_path: '/mnt/nvme',
    network_mode: 'bridge',
    bridge: 'br0',
    vlan_id: 20,
    ip: '10.0.0.13',
    mac: '52:54:00:d4:e5:f6',
  },
  {
    id: 'vm3',
    name: 'dev-sandbox',
    status: 'stopped',
    phase: 'stopped',
    uptime_seconds: 0,
    username: 'admin',
    cpu_count: 2,
    memory_mb: 4096,
    os: 'fedora',
    disk_gb: 40,
    pool_name: 'local-ssd',
    pool_path: '/var/lib/nano/images',
    network_mode: 'nat',
    ip: undefined,
    mac: '52:54:00:1a:2b:3c',
  },
  {
    id: 'vm4',
    name: 'edge-cache',
    status: 'running',
    phase: 'ready',
    uptime_seconds: 47 * 60,
    username: 'admin',
    cpu_count: 2,
    memory_mb: 2048,
    os: 'alpine',
    disk_gb: 20,
    pool_name: 'local-ssd',
    pool_path: '/var/lib/nano/images',
    network_mode: 'nat',
    ip: '192.168.122.20',
    mac: '52:54:00:9f:8e:7d',
  },
  {
    id: 'vm5',
    name: 'ci-runner',
    status: 'error',
    phase: 'stopped',
    uptime_seconds: 0,
    username: 'admin',
    cpu_count: 4,
    memory_mb: 16384,
    os: 'ubuntu',
    disk_gb: 120,
    pool_name: 'data-hdd',
    pool_path: '/mnt/data',
    network_mode: 'nat',
    ip: undefined,
    mac: '52:54:00:cc:bb:aa',
  },
];

export const MOCK_STATS: Record<string, VMStats> = {
  vm1: { cpu_percent: 34, memory_used_mb: 4980, memory_total_mb: 8192 },
  vm2: { cpu_percent: 58, memory_used_mb: 23592, memory_total_mb: 32768 },
  vm4: { cpu_percent: 12, memory_used_mb: 820, memory_total_mb: 2048 },
};

export const MOCK_SNAPSHOTS: Record<string, Snapshot[]> = {
  vm1: [
    { name: 'pre-deploy', created_at: '2026-06-20T10:00:00Z' },
    { name: 'clean-install', created_at: '2026-06-01T08:00:00Z' },
  ],
  vm2: [{ name: 'nightly-0630', created_at: '2026-06-30T02:00:00Z' }],
  vm3: [],
  vm4: [{ name: 'base', created_at: '2026-06-25T12:00:00Z' }],
  vm5: [],
};

// The VM actions change the lists above in place, so the public demo, which
// has no daemon behind it, keeps a stopped VM stopped and a new one listed
// for the rest of the visit. A VM that starts boots for a few seconds first.
const MOCK_BOOT_MS = 5000;

function mockBoot(vm: VM): void {
  Object.assign(vm, { status: 'running', phase: 'booting', uptime_seconds: 0 });
  setTimeout(() => {
    if (vm.status !== 'running') return;
    vm.phase = 'ready';
    vm.ip ??= `192.168.122.${100 + MOCK_VMS.indexOf(vm)}`;
  }, MOCK_BOOT_MS);
}

export function mockSetPower(id: string, on: boolean): void {
  const vm = MOCK_VMS.find(v => v.id === id);
  if (!vm) return;
  if (on) mockBoot(vm);
  else Object.assign(vm, { status: 'stopped', phase: 'stopped', uptime_seconds: 0 });
}

export interface MockNewVM {
  name: string;
  image_id: string;
  vcpus: number;
  memory_mb: number;
  disk_gb: number;
  pool_name: string;
  network_mode: string;
  username: string;
}

export function mockCreateVM(p: MockNewVM): void {
  if (MOCK_VMS.some(v => v.name === p.name)) throw new Error(`a VM named "${p.name}" already exists`);
  const pool = MOCK_SETTINGS.pools.find(x => x.name === p.pool_name);
  const vm: VM = {
    id: p.name,
    name: p.name,
    status: 'running',
    phase: 'booting',
    uptime_seconds: 0,
    username: p.username,
    cpu_count: p.vcpus,
    memory_mb: p.memory_mb,
    os: MOCK_CATALOG_IMAGES.find(i => i.id === p.image_id)?.os,
    disk_gb: p.disk_gb,
    pool_name: p.pool_name,
    pool_path: pool?.path,
    network_mode: p.network_mode === 'bridge' ? 'bridge' : 'nat',
    mac: `52:54:00:${[0, 0, 0]
      .map(() =>
        Math.floor(Math.random() * 256)
          .toString(16)
          .padStart(2, '0'),
      )
      .join(':')}`,
  };
  MOCK_VMS.push(vm);
  MOCK_STATS[vm.id] = { cpu_percent: 6, memory_used_mb: Math.round(p.memory_mb * 0.2), memory_total_mb: p.memory_mb };
  MOCK_SNAPSHOTS[vm.id] = [];
  mockBoot(vm);
}

export function mockDeleteVM(id: string): void {
  const i = MOCK_VMS.findIndex(v => v.id === id);
  if (i >= 0) MOCK_VMS.splice(i, 1);
}

export function mockSnapshotChange(vmId: string, op: 'create' | 'delete', name: string): void {
  const list = (MOCK_SNAPSHOTS[vmId] ??= []);
  if (op === 'create') list.unshift({ name, created_at: new Date().toISOString() });
  else
    list.splice(
      list.findIndex(s => s.name === name),
      1,
    );
}

export const MOCK_CACHED_IMAGES: CachedImage[] = [
  { id: 'img1', name: 'Ubuntu 24.04 LTS', os: 'ubuntu', size_bytes: 2254857830, cached_at: '2026-06-12T00:00:00Z' },
  { id: 'img2', name: 'Debian 12', os: 'debian', size_bytes: 1932735283, cached_at: '2026-05-30T00:00:00Z' },
  { id: 'img3', name: 'Alpine 3.20', os: 'alpine', size_bytes: 214748364, cached_at: '2026-06-28T00:00:00Z' },
];

export const MOCK_CATALOG_IMAGES: CatalogImage[] = [
  { id: 'c1', name: 'Ubuntu 24.04 LTS', os: 'ubuntu', arch: 'amd64', size_bytes: 2254857830, state: 'cached' },
  { id: 'c2', name: 'Ubuntu 22.04 LTS', os: 'ubuntu', arch: 'amd64', size_bytes: 2147483648, state: 'idle' },
  { id: 'c3', name: 'Debian 12', os: 'debian', arch: 'amd64', size_bytes: 1932735283, state: 'cached' },
  { id: 'c4', name: 'Fedora 40', os: 'fedora', arch: 'amd64', size_bytes: 2576980377, state: 'idle' },
  { id: 'c5', name: 'Alpine 3.20', os: 'alpine', arch: 'amd64', size_bytes: 214748364, state: 'cached' },
  { id: 'c6', name: 'Rocky Linux 9', os: 'rocky', arch: 'amd64', size_bytes: 2791728742, state: 'idle' },
  { id: 'c7', name: 'AlmaLinux 9', os: 'alma', arch: 'amd64', size_bytes: 2684354560, state: 'idle' },
  { id: 'c8', name: 'openSUSE Leap 15.6', os: 'opensuse', arch: 'amd64', size_bytes: 2147483648, state: 'idle' },
];

export const MOCK_STORAGE_POOLS: StoragePool[] = [
  { name: 'local-ssd', path: '/var/lib/nano/images', free_bytes: 450971566080, total_bytes: 1030792151040, default: true },
  { name: 'nvme-fast', path: '/mnt/nvme', free_bytes: 956301426688, total_bytes: 2147483648000 },
  { name: 'data-hdd', path: '/mnt/data', free_bytes: 1932735283200, total_bytes: 8589934592000 },
];

// Two bridges, both with a physical port — the picker's bridge select only
// appears above one bridge.
export const MOCK_BRIDGES: HostBridge[] = [
  { name: 'br0', uplink: 'eth0' },
  { name: 'br-lan', uplink: 'eth1' },
];

export const MOCK_NETWORK_INTERFACES: NetworkInterface[] = [
  { name: 'eth0', flags: 'up|broadcast|multicast', addrs: ['192.168.1.100/24'], available: true },
  { name: 'virbr0', flags: 'up|broadcast|multicast', addrs: ['192.168.122.1/24'], available: true },
];

// The host's default-route interface — what a VLAN rides. Non-empty so
// VITE_MOCK=true dev shows the wizard's ordinary path (VLAN offered); the
// no-default-route case is covered by CreateVMModal.test.tsx directly.
export const MOCK_UPLINK = 'eth0';

export const MOCK_NETWORK_LEASES: NetworkLease[] = [
  { vm_name: 'web-prod-01', mac: '52:54:00:a1:b2:c3', ip: '192.168.122.12', hostname: 'web-prod-01' },
  { vm_name: 'db-main', mac: '52:54:00:d4:e5:f6', ip: '10.0.0.13', hostname: 'db-main' },
  { vm_name: 'edge-cache', mac: '52:54:00:9f:8e:7d', ip: '192.168.122.20', hostname: 'edge-cache' },
];

// Ascending by seq, like GET /events; the feed reverses it for display.
// Messages omit the VM name, which the feed renders as its own chip. The
// backlog has one closed incident and one open: edge-cache recovered,
// ci-runner did not.
export const MOCK_EVENTS: NanoEvent[] = [
  {
    seq: 1,
    at: new Date(Date.now() - 40 * 60_000).toISOString(),
    level: 'error',
    kind: 'vm.gave_up',
    vm: 'ci-runner',
    message: 'gave up restarting after 6 attempts',
  },
  { seq: 2, at: new Date(Date.now() - 6 * 60_000).toISOString(), level: 'error', kind: 'libvirt.lost', message: 'libvirt connection lost' },
  {
    seq: 3,
    at: new Date(Date.now() - 5 * 60_000).toISOString(),
    level: 'info',
    kind: 'libvirt.connected',
    message: 'libvirt connection restored',
  },
  {
    seq: 4,
    at: new Date(Date.now() - 3 * 60_000).toISOString(),
    level: 'warn',
    kind: 'vm.restarted',
    vm: 'edge-cache',
    message: 'restart attempt 1/6 failed, retrying at 2026-08-08T09:14:02Z',
    detail: 'internal error: process exited while connecting to monitor',
  },
  {
    seq: 5,
    at: new Date(Date.now() - 105_000).toISOString(),
    level: 'info',
    kind: 'vm.restarted',
    vm: 'edge-cache',
    message: 'restarted (attempt 2/6)',
  },
  {
    seq: 6,
    at: new Date(Date.now() - 45_000).toISOString(),
    level: 'info',
    kind: 'vm.recovered',
    vm: 'edge-cache',
    message: 'recovered — running steadily for 1m0s',
  },
  {
    seq: 7,
    at: new Date(Date.now() - 20_000).toISOString(),
    level: 'info',
    kind: 'vm.redefined',
    vm: 'web-01',
    message: 'rebuilt from its spec and started (attempt 1/6)',
  },
];

// A light sine wobble around a base value — enough for the mock chart to look
// alive without a backend. Production always renders the real
// /stats/history series; this data never ships (see mock.stub.ts).
function genHistory(cpuBase: number, memBase: number, memTotal: number): StatsSample[] {
  const now = Math.floor(Date.now() / 1000);
  return Array.from({ length: 48 }, (_, i) => ({
    at: now - (48 - i) * 5,
    cpu_percent: Math.max(1, Math.min(99, cpuBase + Math.sin(i * 0.4) * 12)),
    mem_used_mb: Math.max(0, memBase + Math.sin(i * 0.25) * memBase * 0.03),
    mem_total_mb: memTotal,
  }));
}

export const MOCK_STATS_HISTORY: Record<string, StatsSample[]> = {
  host: genHistory(14, 4096, 16384),
  vm1: genHistory(34, 4980, 8192),
  vm2: genHistory(58, 23592, 32768),
  vm4: genHistory(12, 820, 2048),
};

// A plausible desktop: integrated graphics driving the console, an NVIDIA card
// on vfio-pci with its audio function as a sibling, a chipset group mixing a
// USB controller with the SATA controller holding the host's disks, and group
// 0 holding the onboard NIC behind a host bridge. Both blocking reasons
// appear: a storage controller is a risk the operator may accept, a host
// bridge can never be released.
//
// To develop against IOMMU off, set `enabled: false` and `groups_seen: 0`,
// and use the commented-out message and remedy.
//
// `reason` strings follow the backend's wording (internal/hostdev).
export const MOCK_HOST_DEVICES: HostDeviceReport = {
  iommu: {
    enabled: true,
    groups_seen: 24,
    message: 'IOMMU is enabled; the kernel reports 24 device groups',
    // message: 'IOMMU is off — PCI passthrough is unavailable on this host',
    // remedy: 'Add intel_iommu=on (Intel) or amd_iommu=on iommu=pt (AMD) to GRUB_CMDLINE_LINUX in /etc/default/grub, then run: sudo update-grub && sudo reboot',
  },
  pci: [
    {
      // Group 0 usually holds the host bridge, which the host cannot release,
      // so nothing sharing it can be passed through.
      address: '0000:00:00.0',
      vendor: 'Intel Corporation',
      product: '',
      class: 'Host bridge',
      driver: '',
      iommu_group: 0,
      boot_vga: false,
      siblings: ['0000:00:1c.0'],
      usable: false,
      reason: 'this device is a Host bridge, which cannot be released by the host',
    },
    {
      address: '0000:00:02.0',
      vendor: 'Intel Corporation',
      product: 'HD Graphics 530',
      class: 'VGA compatible controller',
      driver: 'i915',
      iommu_group: 1,
      boot_vga: true,
      siblings: [],
      usable: true,
    },
    {
      address: '0000:00:14.0',
      vendor: 'Intel Corporation',
      product: 'C610/X99 series chipset USB xHCI Host Controller',
      class: 'Serial bus controller',
      driver: 'xhci_hcd',
      iommu_group: 6,
      boot_vga: false,
      siblings: ['0000:00:1f.2'],
      usable: false,
      reason:
        "IOMMU group 6 also contains 0000:00:1f.2 (SATA controller, which probably carries the host's own disks); a group is passed through as one unit",
    },
    {
      // The collateral form of the same impossibility: a perfectly ordinary
      // NIC that can never be passed through because of what shares its group.
      address: '0000:00:1c.0',
      vendor: 'Intel Corporation',
      product: 'Ethernet Connection (2) I219-V',
      class: 'Network controller',
      driver: 'e1000e',
      iommu_group: 0,
      boot_vga: false,
      siblings: ['0000:00:00.0'],
      usable: false,
      reason:
        'IOMMU group 0 also contains 0000:00:00.0 (Host bridge, which cannot be released by the host); a group is passed through as one unit',
    },
    {
      address: '0000:00:1f.2',
      vendor: 'Intel Corporation',
      product: 'C610/X99 series chipset 6-Port SATA Controller [AHCI mode]',
      class: 'SATA controller',
      driver: 'ahci',
      iommu_group: 6,
      boot_vga: false,
      siblings: ['0000:00:14.0'],
      usable: false,
      reason: "this device is a SATA controller, which probably carries the host's own disks",
    },
    {
      address: '0000:01:00.0',
      vendor: 'NVIDIA Corporation',
      product: 'GP107 [GeForce GTX 1050 Ti]',
      class: 'VGA compatible controller',
      driver: 'vfio-pci',
      iommu_group: 13,
      boot_vga: false,
      siblings: ['0000:01:00.1'],
      usable: true,
    },
    {
      address: '0000:01:00.1',
      vendor: 'NVIDIA Corporation',
      product: 'GP107GL High Definition Audio Controller',
      class: 'Audio device',
      driver: 'vfio-pci',
      iommu_group: 13,
      boot_vga: false,
      siblings: ['0000:01:00.0'],
      usable: true,
    },
  ],
  usb: [
    { vendor_id: '0x046d', product_id: '0xc52b', description: 'Logitech, Inc. Unifying Receiver', bus_seen: 'bus 1 device 4' },
    { vendor_id: '0x0781', product_id: '0x5583', description: 'SanDisk Corp. Ultra Fit', bus_seen: 'bus 2 device 7' },
  ],
};

// One failing required check ("default network") so mock dev shows the
// readiness banner; set its `ok` to true to see the healthy state.
export const MOCK_SYSTEM_DEPS: SystemDeps = {
  checks: [
    { name: 'xorriso', ok: true, required: true, message: 'xorriso installed' },
    { name: 'virsh', ok: true, required: true, message: 'virsh installed' },
    { name: 'libvirtd', ok: true, required: true, message: 'libvirtd installed and running' },
    {
      name: 'default network',
      ok: false,
      required: true,
      message: "'default' network exists but is inactive — VM networking will fail",
      remedy: 'sudo virsh net-start default && sudo virsh net-autostart default',
    },
    { name: 'libvirt connection', ok: true, required: true, message: 'connected to qemu:///system' },
    {
      name: 'bridge',
      ok: true,
      required: false,
      message: 'no host bridge found — Bridge network mode disabled (NAT still works)',
    },
    // The two firmware checks the create wizard gates its toggles on: OVMF
    // present so the UEFI paths are reachable, swtpm missing so the
    // disabled-with-a-remedy state is too.
    {
      name: 'ovmf',
      ok: true,
      required: false,
      message: 'UEFI firmware present at /usr/share/OVMF — UEFI and secure boot available',
    },
    {
      name: 'swtpm',
      ok: false,
      required: false,
      message: 'swtpm not found — VMs cannot be given an emulated TPM 2.0',
      remedy: 'sudo apt install swtpm swtpm-tools  # or: sudo dnf install swtpm swtpm-tools',
    },
  ],
};

// Per-VM hardware specs. Mutable, so a change in the Hardware tab shows the
// restart badge afterwards.
export const MOCK_VM_SPECS: Record<string, VMSpec> = {
  'web-server-01': {
    pci_devices: [],
    usb_devices: [{ vendor_id: '0x1050', product_id: '0x0407', description: 'Yubico.com YubiKey OTP+FIDO+CCID' }],
    cpu: { mode: 'host-model', topology: null, cpu_set: '' },
    network: { mode: 'bridge', bridge: 'br0', vlan_id: 20 },
    disks: [
      { target: 'vda', path: '/var/lib/nano-cloud/vm-disks/web-server-01.qcow2', size_gb: 20, boot: true },
      { target: 'vdb', path: '/var/lib/nano-cloud/vm-disks/web-server-01-vdb.qcow2', size_gb: 50, boot: false },
    ],
    restart_required: true,
    warnings: [],
  },
};

export const EMPTY_VM_SPEC: VMSpec = {
  pci_devices: [],
  usb_devices: [],
  cpu: { mode: 'host-model', topology: null, cpu_set: '' },
  network: { mode: 'nat', bridge: '', vlan_id: 0 },
  disks: [{ target: 'vda', path: '/var/lib/nano-cloud/vm-disks/boot.qcow2', size_gb: 20, boot: true }],
  restart_required: false,
  warnings: [],
};

// MOCK_HOST_CPUS is what the mock host has, so the picker's hint and the
// cpuset refusal below agree with each other.
export const MOCK_HOST_CPUS = 16;

// applyMockSpecPatch mirrors the backend closely enough to exercise the UI:
// unknown devices are refused, a PCI pick drags its IOMMU siblings in, and a
// boot-VGA card warns.
export function applyMockSpecPatch(id: string, patch: VMSpecPatch): VMSpec {
  const current = MOCK_VM_SPECS[id] || { ...EMPTY_VM_SPEC };

  const pciAddrs = patch.pci_devices ?? current.pci_devices.map(d => d.address);
  const usbIds = patch.usb_devices ?? current.usb_devices.map(d => `${d.vendor_id}:${d.product_id}`);

  const warnings: string[] = [];
  const picked = new Map<string, AssignedPCIDevice>();
  for (const addr of pciAddrs) {
    const dev = MOCK_HOST_DEVICES.pci.find(d => d.address === addr);
    if (!dev) throw new Error(`hostdev: device unavailable: this host has no PCI device at ${addr}`);
    if (!dev.usable) throw new Error(`hostdev: device unavailable: ${addr} cannot be passed through — ${dev.reason}`);
    if (dev.boot_vga) {
      warnings.push(
        `${addr} (${dev.vendor} ${dev.product}) is the card driving this host's console. Passing it to a guest takes the console with it: the host display goes dark until the VM stops.`,
      );
    }
    for (const a of [addr, ...dev.siblings]) {
      const sib = MOCK_HOST_DEVICES.pci.find(d => d.address === a);
      if (sib) picked.set(a, { address: sib.address, vendor: sib.vendor, product: sib.product });
    }
  }

  const usb: AssignedUSBDevice[] = [];
  for (const idPair of usbIds) {
    const [vendor_id, product_id] = idPair.split(':');
    const dev = MOCK_HOST_DEVICES.usb.find(d => d.vendor_id === vendor_id && d.product_id === product_id);
    if (!dev) throw new Error(`hostdev: device unavailable: no USB device ${idPair} is plugged into this host`);
    usb.push({ vendor_id: dev.vendor_id, product_id: dev.product_id, description: dev.description });
  }

  // The CPU is refused for the same reasons the backend refuses it, so mock
  // mode cannot teach a shape the real daemon then rejects.
  const cpu = patch.cpu ?? current.cpu;
  if (cpu.topology) {
    const { sockets, cores, threads } = cpu.topology;
    if (sockets < 1 || cores < 1 || threads < 1)
      throw new Error('vm: invalid CPU shape: a topology needs at least one socket, core and thread');
  }
  const highest = Math.max(...(cpu.cpu_set.match(/\d+/g) ?? ['-1']).map(Number));
  if (highest >= MOCK_HOST_CPUS) {
    throw new Error(
      `vm: invalid CPU shape: cpu ${highest} does not exist — this host has ${MOCK_HOST_CPUS} CPUs (0-${MOCK_HOST_CPUS - 1})`,
    );
  }

  // No network refusal to mirror: the backend builds any VLAN it is given.
  const network = patch.network ?? current.network;

  const next: VMSpec = {
    pci_devices: [...picked.values()].sort((a, b) => a.address.localeCompare(b.address)),
    usb_devices: usb,
    cpu,
    network,
    disks: current.disks,
    restart_required: true,
    warnings: warnings.sort(),
  };
  MOCK_VM_SPECS[id] = next;
  return next;
}

// applyMockDiskChange mirrors the three disk endpoints closely enough that mock
// mode teaches the same rules the daemon enforces — the boot disk cannot be
// detached, targets are allocated in order, and a disk only ever grows.
export function applyMockDiskChange(id: string, op: 'attach' | 'detach' | 'resize', args: { target?: string; sizeGB?: number }): VMSpec {
  const current = MOCK_VM_SPECS[id] || { ...EMPTY_VM_SPEC };
  let disks = [...current.disks];

  if (op === 'attach') {
    const taken = new Set(disks.map(d => d.target));
    const target = 'abcdefghijklmnopqrstuvwxyz'
      .split('')
      .map(c => `vd${c}`)
      .find(t => !taken.has(t));
    if (!target) throw new Error(`vm: no free disk target: ${id} already has the 26 disks a VM can hold`);
    disks.push({ target, path: `/var/lib/nano-cloud/vm-disks/${id}-${target}.qcow2`, size_gb: args.sizeGB!, boot: false });
  } else if (op === 'detach') {
    if (disks.find(d => d.target === args.target)?.boot) {
      throw new Error(`${args.target} is the disk ${id} boots from and cannot be detached — delete the VM instead`);
    }
    disks = disks.filter(d => d.target !== args.target);
  } else {
    disks = disks.map(d => {
      if (d.target !== args.target) return d;
      if (args.sizeGB! <= d.size_gb) {
        throw new Error(
          `${d.target} is already ${d.size_gb} GB — a disk can only be grown, because shrinking one discards whatever the guest had written past the new end`,
        );
      }
      return { ...d, size_gb: args.sizeGB! };
    });
  }

  // A resize needs no redefine, so it is the one change that does not put the
  // running machine out of step with its spec.
  const next: VMSpec = { ...current, disks, restart_required: op !== 'resize' || current.restart_required };
  MOCK_VM_SPECS[id] = next;
  return next;
}

// Templates are mutable in mock mode, so "Save as template" fills the picker.
const MOCK_TEMPLATES: VMTemplate[] = [
  {
    name: 'Ubuntu web server',
    created_at_unix: 1754000000,
    source_vm: 'web-01',
    spec: {
      image_id: 'ubuntu-24.04',
      os: 'ubuntu',
      vcpus: 2,
      memory_mb: 4096,
      disk_gb: 40,
      network_mode: 'bridge',
      firmware: '',
      secure_boot: false,
      tpm: false,
      cpu: { mode: 'host-model', topology: null, cpu_set: '' },
      pci_devices: [],
      usb_devices: [],
      cloud_init: {
        username: 'deploy',
        packages: ['nginx', 'ufw'],
        runcmd: ['systemctl enable --now nginx'],
        write_files: [],
        ssh_keys: ['ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexample deploy@laptop'],
      },
    },
  },
];

export function mockTemplates(): VMTemplate[] {
  return [...MOCK_TEMPLATES].sort((a, b) => a.name.localeCompare(b.name));
}

export function applyMockTemplateChange(op: 'save' | 'delete', args: { name: string; fromVM?: string }): VMTemplate {
  if (op === 'delete') {
    const i = MOCK_TEMPLATES.findIndex(t => t.name === args.name);
    if (i < 0) throw new Error(`template: no template with that name: "${args.name}"`);
    return MOCK_TEMPLATES.splice(i, 1)[0];
  }
  if (MOCK_TEMPLATES.some(t => t.name === args.name)) {
    throw new Error(`template: a template with that name already exists: "${args.name}"`);
  }
  const saved: VMTemplate = {
    ...MOCK_TEMPLATES[0],
    name: args.name,
    created_at_unix: Math.floor(Date.now() / 1000),
    source_vm: args.fromVM ?? '',
  };
  MOCK_TEMPLATES.push(saved);
  return saved;
}

// Services — the catalog mirrors internal/services/catalog/*.yaml; the
// instances advance one build step per poll so the UI can be watched working.
export const MOCK_SERVICE_CATALOG: ServiceRecipe[] = [
  {
    id: 'kubernetes',
    name: 'Kubernetes',
    category: 'containers',
    description:
      'A single-node k3s cluster on the latest stable release. Download the kubeconfig and drive it with kubectl from your own machine.',
    image: 'ubuntu-24.04',
    sizes: [
      { id: 'small', label: 'Small', vcpus: 2, memory_mb: 2048, disk_gb: 20 },
      { id: 'medium', label: 'Medium', vcpus: 2, memory_mb: 4096, disk_gb: 30, default: true },
      { id: 'large', label: 'Large', vcpus: 4, memory_mb: 8192, disk_gb: 50 },
    ],
    params: [],
    ports: [6443],
    console: 'kubernetes',
    endpoints: [{ id: 'api', label: 'Kubernetes API', port: 6443, scheme: 'https' }],
    outputs: [
      { id: 'api', label: 'API server' },
      { id: 'try', label: 'Try it' },
    ],
    files: [{ id: 'kubeconfig', label: 'kubeconfig' }],
  },
  {
    id: 'postgres',
    name: 'PostgreSQL',
    category: 'database',
    description: 'PostgreSQL from Ubuntu 24.04, with a database and a user ready to connect to from your machine.',
    image: 'ubuntu-24.04',
    sizes: [
      { id: 'small', label: 'Small', vcpus: 1, memory_mb: 1024, disk_gb: 10 },
      { id: 'medium', label: 'Medium', vcpus: 2, memory_mb: 2048, disk_gb: 20, default: true },
      { id: 'large', label: 'Large', vcpus: 4, memory_mb: 8192, disk_gb: 40 },
    ],
    params: [
      { id: 'database', label: 'Database', default: 'app', pattern: '^[a-z_][a-z0-9_]{0,62}$', hint: 'lowercase letters, digits and _' },
      { id: 'user', label: 'User', default: 'app', pattern: '^[a-z_][a-z0-9_]{0,62}$', hint: 'lowercase letters, digits and _' },
      { id: 'password', label: 'Password', generate: 24, secret: true },
    ],
    ports: [5432],
    endpoints: [{ id: 'db', label: 'PostgreSQL', port: 5432 }],
    outputs: [
      { id: 'url', label: 'Connection URL', secret: true },
      { id: 'psql', label: 'Connect with psql', secret: true },
      { id: 'host', label: 'Host' },
    ],
    files: [],
  },
  {
    id: 's3',
    name: 'S3 storage',
    category: 'storage',
    description: 'S3-compatible object storage (SeaweedFS), with an access key ready for the AWS CLI or any S3 client.',
    image: 'ubuntu-24.04',
    sizes: [
      { id: 'small', label: 'Small', vcpus: 1, memory_mb: 1024, disk_gb: 20 },
      { id: 'medium', label: 'Medium', vcpus: 2, memory_mb: 2048, disk_gb: 50, default: true },
      { id: 'large', label: 'Large', vcpus: 2, memory_mb: 4096, disk_gb: 100 },
    ],
    params: [
      { id: 'access_key', label: 'Access key', generate: 20 },
      { id: 'secret_key', label: 'Secret key', generate: 40, secret: true },
    ],
    ports: [8333],
    endpoints: [{ id: 's3', label: 'S3 API', port: 8333, scheme: 'http' }],
    outputs: [
      { id: 'endpoint', label: 'Endpoint' },
      { id: 'access_key', label: 'Access key' },
      { id: 'secret_key', label: 'Secret key', secret: true },
      { id: 'aws', label: 'Try it', secret: true },
    ],
    files: [],
  },
];

MOCK_SERVICE_CATALOG.push(
  {
    id: 'kubernetes_cluster',
    name: 'Kubernetes cluster',
    category: 'containers',
    description:
      'A k3s cluster on the latest stable release: one control-plane node and workers, each on its own VM. Download the kubeconfig and use kubectl from your own machine.',
    image: 'ubuntu-24.04',
    sizes: [
      { id: 'small', label: 'Small', vcpus: 2, memory_mb: 2048, disk_gb: 20 },
      { id: 'medium', label: 'Medium', vcpus: 2, memory_mb: 4096, disk_gb: 30, default: true },
      { id: 'large', label: 'Large', vcpus: 4, memory_mb: 8192, disk_gb: 50 },
    ],
    params: [
      { id: 'workers', label: 'Workers', default: '2', pattern: '^[1-5]$', hint: 'a number from 1 to 5' },
      { id: 'token', label: 'Cluster token', generate: 48, secret: true },
    ],
    ports: [],
    roles: [
      { id: 'server', label: 'Control plane', count: 1, ports: [6443] },
      { id: 'agent', label: 'Worker', count_param: 'workers', after: 'server' },
    ],
    console: 'kubernetes',
    endpoints: [{ id: 'api', label: 'Kubernetes API', role: 'server', port: 6443, scheme: 'https' }],
    outputs: [
      { id: 'api', label: 'API server' },
      { id: 'try', label: 'Try it' },
    ],
    files: [{ id: 'kubeconfig', label: 'kubeconfig' }],
  },
  {
    id: 'mysql',
    name: 'MySQL',
    category: 'database',
    description: 'MySQL from Ubuntu 24.04, with a database and a user ready to connect to from your machine.',
    image: 'ubuntu-24.04',
    sizes: MOCK_SERVICE_CATALOG.find(r => r.id === 'postgres')!.sizes,
    params: MOCK_SERVICE_CATALOG.find(r => r.id === 'postgres')!.params,
    ports: [3306],
    endpoints: [{ id: 'db', label: 'MySQL', port: 3306 }],
    outputs: [
      { id: 'url', label: 'Connection URL', secret: true },
      { id: 'cli', label: 'Connect with mysql', secret: true },
      { id: 'host', label: 'Host' },
    ],
    files: [],
  },
  {
    id: 'observability',
    name: 'Prometheus + Grafana',
    category: 'monitoring',
    description:
      'Prometheus and Grafana on one VM, watching every service nano-cloud runs: nano-cloud sets up their metrics and keeps Prometheus told about them, with dashboards and alert rules ready in Grafana on first login.',
    image: 'ubuntu-24.04',
    console: 'monitoring',
    sizes: [
      { id: 'small', label: 'Small', vcpus: 1, memory_mb: 2048, disk_gb: 20 },
      { id: 'medium', label: 'Medium', vcpus: 2, memory_mb: 4096, disk_gb: 40, default: true },
      { id: 'large', label: 'Large', vcpus: 4, memory_mb: 8192, disk_gb: 80 },
    ],
    params: [{ id: 'grafana_password', label: 'Grafana password', generate: 24, secret: true }],
    ports: [3000, 9090],
    endpoints: [
      { id: 'grafana', label: 'Grafana', port: 3000, scheme: 'http' },
      { id: 'prometheus', label: 'Prometheus', port: 9090, scheme: 'http' },
    ],
    outputs: [
      { id: 'grafana', label: 'Grafana' },
      { id: 'grafana_user', label: 'Grafana user' },
      { id: 'grafana_password', label: 'Grafana password', secret: true },
      { id: 'prometheus', label: 'Prometheus' },
    ],
    files: [],
  },
);
MOCK_SERVICE_CATALOG.sort((a, b) => a.name.localeCompare(b.name));

// mockMembers names a service's VMs the way internal/services does.
function mockMembers(name: string, recipe: ServiceRecipe, workers = 2, ready = false): NonNullable<Service['members']> {
  if (!recipe.roles?.length)
    return [{ role: 'main', role_label: recipe.name, vm: name, status: 'running', phase: ready ? 'ready' : 'booting' }];
  return [
    {
      role: 'server',
      role_label: 'Control plane',
      vm: `${name}-server`,
      status: 'running',
      phase: ready ? 'ready' : 'booting',
      ip: ready ? '192.168.122.50' : undefined,
    },
    ...Array.from({ length: workers }, (_, i) => ({
      role: 'agent',
      role_label: 'Worker',
      vm: workers > 1 ? `${name}-agent-${i + 1}` : `${name}-agent`,
      status: 'running',
      phase: ready ? 'ready' : 'booting',
      ip: ready ? `192.168.122.${51 + i}` : undefined,
    })),
  ];
}

const MOCK_BUILD_STEPS: Service['state'][] = ['pulling', 'creating', 'waiting-ip', 'installing', 'ready'];

// mockEndpoints gives a service's endpoints the ports internal/services
// would: 30000 plus the VM port's last three digits.
function mockEndpoints(svc: Service): Service['endpoints'] {
  const recipe = MOCK_SERVICE_CATALOG.find(r => r.id === svc.recipe);
  const host = svc.shared && !['localhost', '127.0.0.1'].includes(window.location.hostname) ? window.location.hostname : 'localhost';
  const own = (recipe?.endpoints ?? []).map(e => {
    const hostPort = 30000 + (e.port % 1000);
    return {
      id: e.id,
      label: e.label,
      scheme: e.scheme,
      port: e.port,
      host_port: hostPort,
      address: `${host}:${hostPort}`,
      direct: svc.ip ? `${svc.ip}:${e.port}` : undefined,
    };
  });
  return [...own, ...(svc.endpoints ?? []).filter(e => e.published)];
}

function mockOutputs(svc: Service): Service['outputs'] {
  const addr = (id: string) => svc.endpoints.find(e => e.id === id)?.address ?? '';
  switch (svc.recipe) {
    case 'kubernetes':
    case 'kubernetes_cluster':
      return [
        { id: 'api', label: 'API server', value: `https://${addr('api')}` },
        { id: 'try', label: 'Try it', value: `kubectl --kubeconfig ${svc.name}.yaml get nodes` },
      ];
    case 'observability':
      return [
        { id: 'grafana', label: 'Grafana', value: `http://${addr('grafana')}` },
        { id: 'grafana_user', label: 'Grafana user', value: 'admin' },
        { id: 'grafana_password', label: 'Grafana password', value: 'Gr4fN8vQ2mLx7KpZ5tWc9Rb3', secret: true },
        { id: 'prometheus', label: 'Prometheus', value: `http://${addr('prometheus')}` },
      ];
    case 'postgres':
      return [
        { id: 'url', label: 'Connection URL', value: `postgres://app:Xk29fQm4LpZr8VtN3sWb7Hc1@${addr('db')}/app`, secret: true },
        { id: 'psql', label: 'Connect with psql', value: `psql "postgres://app:Xk29fQm4LpZr8VtN3sWb7Hc1@${addr('db')}/app"`, secret: true },
        { id: 'host', label: 'Host', value: addr('db') },
      ];
    default:
      return [
        { id: 'endpoint', label: 'Endpoint', value: `http://${addr('s3')}` },
        { id: 'access_key', label: 'Access key', value: 'QH7M2VXK9TN4RB8CW1PA' },
        { id: 'secret_key', label: 'Secret key', value: 'p4Lz8XwQm2Rk7Vn1Jt5Hc9Gb3Fd6Ys0Ue8Ai2Ow4', secret: true },
      ];
  }
}

const MOCK_SERVICES: Service[] = [
  {
    name: 'pg-dev',
    recipe: 'postgres',
    recipe_name: 'PostgreSQL',
    category: 'database',
    size: MOCK_SERVICE_CATALOG.find(r => r.id === 'postgres')!.sizes[1],
    state: 'ready',
    vms: ['pg-dev'],
    ip: '192.168.122.40',
    created_at_unix: Math.floor(Date.now() / 1000) - 7200,
    ready_at_unix: Math.floor(Date.now() / 1000) - 7080,
    outputs: [],
    files: [],
    endpoints: [],
    shared: false,
    vm_user: 'nanocloud',
    vm_password: 'f8Kq2ZtR5mWx9NcB4vLp',
  },
  {
    name: 'k8s-lab',
    recipe: 'kubernetes_cluster',
    recipe_name: 'Kubernetes cluster',
    category: 'containers',
    size: { id: 'medium', label: 'Medium', vcpus: 2, memory_mb: 4096, disk_gb: 30, default: true },
    state: 'installing',
    vms: ['k8s-lab-server', 'k8s-lab-agent-1', 'k8s-lab-agent-2'],
    ip: '192.168.122.50',
    created_at_unix: Math.floor(Date.now() / 1000) - 90,
    outputs: [],
    files: [],
    endpoints: [],
    shared: false,
    vm_user: 'nanocloud',
    vm_password: 'Tn3Vb8Xq1Lz6Rk9Wm2Pc',
  },
  // A build held up by a guest that boots but never finishes answering SSH:
  // for looking at the stuck-machine states. Restarting its VM frees it.
  {
    name: 'stuck-lab',
    recipe: 'kubernetes_cluster',
    recipe_name: 'Kubernetes cluster',
    category: 'containers',
    size: { id: 'medium', label: 'Medium', vcpus: 2, memory_mb: 4096, disk_gb: 30, default: true },
    state: 'installing',
    vms: ['stuck-lab-server', 'stuck-lab-agent-1', 'stuck-lab-agent-2'],
    ip: '192.168.122.70',
    created_at_unix: Math.floor(Date.now() / 1000) - 900,
    outputs: [],
    files: [],
    endpoints: [],
    shared: false,
    vm_user: 'nanocloud',
    vm_password: 'Mock8Pass2Word5Here9',
  },
];
for (const svc of MOCK_SERVICES) svc.endpoints = mockEndpoints(svc);
MOCK_SERVICES[0].outputs = mockOutputs(MOCK_SERVICES[0]);
MOCK_SERVICES[0].members = [
  { role: 'main', role_label: 'PostgreSQL', vm: 'pg-dev', status: 'running', phase: 'ready', ip: '192.168.122.40' },
];
MOCK_SERVICES[1].members = mockMembers(
  'k8s-lab',
  MOCK_SERVICE_CATALOG.find(r => r.id === 'kubernetes_cluster')!,
  2,
  true,
);
MOCK_SERVICES[1].console = 'kubernetes';
MOCK_SERVICES[1].scale = { role: 'agent', label: 'Worker', count: 2 };
MOCK_SERVICES[0].alerts = [
  {
    name: 'DiskFillingUp',
    title: 'Disk filling up',
    summary: "pg-dev's disk is 87% full",
    severity: 'warning',
    service: 'pg-dev',
    vm: 'pg-dev',
    since_unix: Math.floor(Date.now() / 1000) - 1260,
  },
];
{
  const stuck = MOCK_SERVICES.find(s => s.name === 'stuck-lab')!;
  stuck.members = mockMembers(
    'stuck-lab',
    MOCK_SERVICE_CATALOG.find(r => r.id === 'kubernetes_cluster')!,
    2,
    true,
  ).map(m => ({
    ...m,
    install: m.role === 'server' ? 'installing' : 'waiting',
    waits_for: m.role === 'server' ? undefined : 'Control plane',
  }));
  stuck.members[1] = {
    ...stuck.members[1],
    install: undefined,
    waits_for: undefined,
    unreachable:
      "nano-cloud cannot read stuck-lab-agent-1's log at 192.168.122.51: SSH starts but does not finish connecting (ssh: handshake failed: read tcp 192.168.122.1:61669->192.168.122.51:22: read: connection reset by peer)",
    unreachable_since: Math.floor(Date.now() / 1000) - 400,
  };
  stuck.console = 'kubernetes';
}
{
  const recipe = MOCK_SERVICE_CATALOG.find(r => r.id === 'observability')!;
  const obs: Service = {
    name: 'obs-1',
    recipe: 'observability',
    recipe_name: recipe.name,
    category: recipe.category,
    size: recipe.sizes[1],
    state: 'ready',
    vms: ['obs-1'],
    members: [{ role: 'main', role_label: recipe.name, vm: 'obs-1', status: 'running', phase: 'ready', ip: '192.168.122.60' }],
    ip: '192.168.122.60',
    created_at_unix: Math.floor(Date.now() / 1000) - 5400,
    ready_at_unix: Math.floor(Date.now() / 1000) - 5100,
    outputs: [],
    files: [],
    endpoints: [],
    shared: false,
    console: 'monitoring',
    vm_user: 'nanocloud',
    vm_password: 'Hq7Wm2Xk9Lp4Rv8Nc3Zt',
  };
  obs.endpoints = mockEndpoints(obs);
  obs.outputs = mockOutputs(obs);
  MOCK_SERVICES.push(obs);
}

// mockServiceVMs are the mock services' VMs, so the VMs page shows them
// grouped under their services as it does against a daemon.
export function mockServiceVMs(): VM[] {
  return MOCK_SERVICES.flatMap(svc =>
    (svc.members ?? []).map((m, i) => ({
      id: m.vm,
      name: m.vm,
      status: (m.status === 'missing' ? 'stopped' : m.status) as VM['status'],
      phase: (m.phase ?? 'booting') as VM['phase'],
      uptime_seconds: 3600 + i,
      username: 'nanocloud',
      cpu_count: svc.size.vcpus,
      memory_mb: svc.size.memory_mb,
      os: 'ubuntu',
      disk_gb: svc.size.disk_gb,
      pool_name: 'local-ssd',
      pool_path: '/var/lib/nano/images',
      network_mode: 'nat' as const,
      ip: m.ip ?? '',
      mac: `52:54:00:5e:00:${(10 + i).toString(16)}`,
      service: svc.name,
    })),
  );
}

export function mockServices(): Service[] {
  for (const svc of MOCK_SERVICES) {
    // A worker being removed is gone by the next look; one being added has
    // joined once the build reaches ready.
    if (svc.removing?.length) {
      svc.members = (svc.members ?? []).filter(m => !svc.removing!.includes(m.vm));
      svc.vms = svc.members.map(m => m.vm);
      svc.removing = undefined;
    }
    if (svc.adding?.length && svc.state === 'installing') {
      for (const m of svc.members ?? []) {
        if (svc.adding.includes(m.vm)) Object.assign(m, { phase: 'ready', ip: `192.168.122.${60 + svc.vms.indexOf(m.vm)}` });
      }
      svc.adding = undefined;
    }
    const i = MOCK_BUILD_STEPS.indexOf(svc.state);
    // A build with a silent machine waits for it instead of moving on.
    if (svc.members?.some(m => m.unreachable)) continue;
    if (i >= 0 && i < MOCK_BUILD_STEPS.length - 1) {
      svc.state = MOCK_BUILD_STEPS[i + 1];
      if (svc.state === 'installing') svc.ip = '192.168.122.42';
      svc.endpoints = mockEndpoints(svc);
      if (svc.state === 'ready') {
        svc.ready_at_unix = svc.ready_at_unix ?? Math.floor(Date.now() / 1000);
        svc.outputs = mockOutputs(svc);
        svc.files = svc.recipe.startsWith('kubernetes') ? [{ id: 'kubeconfig', label: 'kubeconfig' }] : [];
      }
    }
  }
  return MOCK_SERVICES.map(s => ({ ...s }));
}

// mockServiceLog is a VM's install log as far as its service's build has got.
export function mockServiceLog(name: string, vm: string): ServiceLog {
  const svc = MOCK_SERVICES.find(s => s.name === name);
  if (!svc || !svc.vms.includes(vm)) throw new Error(`${name} has no VM named "${vm}"`);
  if (['pulling', 'creating', 'waiting-ip'].includes(svc.state)) {
    throw new Error(`${vm} is not up yet; its log can be read once it has an address`);
  }
  const silent = svc.members?.find(m => m.vm === vm)?.unreachable;
  if (silent) throw new Error(silent);
  const k3s = svc.recipe.startsWith('kubernetes');
  const worker = vm.includes('-agent');
  const lines = k3s
    ? [
        ...(worker ? ['waiting', '… waiting for the control plane'] : []),
        'installing',
        '[INFO]  Finding release for channel stable',
        '[INFO]  Using v1.36.5+k3s1 as release',
        '[INFO]  Downloading binary https://github.com/k3s-io/k3s/releases/download/v1.36.5+k3s1/k3s-arm64',
        '[INFO]  Installing k3s to /usr/local/bin/k3s',
        `[INFO]  systemd: Starting ${worker ? 'k3s-agent' : 'k3s'}`,
      ]
    : ['installing', 'Reading package lists...', 'Setting up postgresql-16 (16.4-0ubuntu0.24.04.1) ...', 'ALTER SYSTEM', 'CREATE DATABASE'];
  const state = svc.state === 'failed' ? 'failed: apt-get update (line 4)' : svc.state === 'installing' ? 'installing' : 'ready';
  if (state === 'ready') lines.push('ready');
  return { vm, state, log: lines.join('\n') + '\n' };
}

// mockRestartVM is a power-cycled VM answering SSH again.
export function mockRestartVM(vm: string): void {
  for (const m of MOCK_SERVICES.flatMap(s => s.members ?? [])) {
    if (m.vm === vm) {
      m.unreachable = undefined;
      m.unreachable_since = undefined;
      if (m.install === undefined) m.install = 'installing';
    }
  }
}

// mockCluster is a Kubernetes service's cluster as its members are now.
export function mockCluster(name: string): Cluster {
  const svc = MOCK_SERVICES.find(s => s.name === name);
  if (!svc || svc.console !== 'kubernetes') throw new Error(`${name} is not a Kubernetes service`);
  const nodes = (svc.members ?? []).filter(m => m.phase === 'ready' && !svc.adding?.includes(m.vm));
  return {
    version: 'v1.36.5+k3s1',
    nodes: nodes.map((m, i) => ({
      name: m.vm,
      roles: m.role === 'server' || m.role === 'main' ? ['control-plane'] : [],
      ready: true,
      unschedulable: svc.removing?.includes(m.vm),
      version: 'v1.36.5+k3s1',
      ip: m.ip,
      pods: i === 0 ? 9 : 3,
      cpu_percent: [31, 18, 12, 9][i % 4],
      memory_percent: [47, 40, 30, 22][i % 4],
    })),
    deployments: [
      {
        namespace: 'default',
        name: 'web',
        ready: 3,
        desired: 3,
        images: ['nginx:1.27'],
        created_unix: Math.floor(Date.now() / 1000) - 7200,
      },
      {
        namespace: 'kube-system',
        name: 'coredns',
        ready: 1,
        desired: 1,
        images: ['rancher/mirrored-coredns-coredns:1.12.3'],
        created_unix: Math.floor(Date.now() / 1000) - 10800,
      },
      {
        namespace: 'kube-system',
        name: 'traefik',
        ready: 1,
        desired: 1,
        images: ['rancher/mirrored-library-traefik:3.3.6'],
        created_unix: Math.floor(Date.now() / 1000) - 10800,
      },
      {
        namespace: 'kube-system',
        name: 'metrics-server',
        ready: 1,
        desired: 1,
        images: ['rancher/mirrored-metrics-server:v0.8.0'],
        created_unix: Math.floor(Date.now() / 1000) - 10800,
      },
    ],
    exposed: [
      { namespace: 'default', name: 'web', type: 'NodePort', ports: [{ name: 'http', port: 80, node_port: 30080, protocol: 'TCP' }] },
      {
        namespace: 'kube-system',
        name: 'traefik',
        type: 'LoadBalancer',
        ports: [{ name: 'web', port: 80, node_port: 31826, protocol: 'TCP' }],
      },
    ],
    pods: { total: 15, running: 14, pending: 1, failed: 0, succeeded: 0 },
    read_at_unix: Math.floor(Date.now() / 1000),
  };
}

// mockMonitoring is what a mock monitoring service watches: every ready
// service, with an hour of CPU that drifts the way real load does.
export function mockMonitoring(name: string): Monitoring {
  const svc = MOCK_SERVICES.find(s => s.name === name);
  if (!svc || svc.console !== 'monitoring') throw new Error(`${name} is not a monitoring service`);
  const watched = MOCK_SERVICES.filter(s => s.state === 'ready');
  const now = Math.floor(Date.now() / 1000);
  const jobsOf = (s: Service) =>
    s.recipe === 'postgres'
      ? ['node', 'postgres']
      : s.recipe.startsWith('kubernetes')
        ? ['node', 'kube-state-metrics']
        : s.console === 'monitoring'
          ? ['node', 'prometheus']
          : ['node'];
  const watching = watched.map(s => {
    const vms = s.members?.length ?? 1;
    const jobs = jobsOf(s);
    const total = vms + jobs.length - 1;
    return { service: s.name, recipe: s.recipe, vms, jobs, up: total, total };
  });
  const base: Record<string, number> = { 'k8s-lab': 34, 'pg-dev': 12, 'obs-1': 8 };
  const cpu = watched.map((s, i) => ({
    name: s.name,
    points: Array.from({ length: 61 }, (_, k): [number, number] => {
      const b = base[s.name] ?? 15;
      const v = b + Math.sin((k + i * 7) / 6) * b * 0.25 + Math.sin(k / 2.3 + i) * 2;
      return [now - (60 - k) * 60, Math.round(Math.max(0, v) * 10) / 10];
    }),
  }));
  return {
    targets_up: watching.reduce((n, w) => n + w.up, 0),
    targets_total: watching.reduce((n, w) => n + w.total, 0),
    vms: watching.reduce((n, w) => n + w.vms, 0),
    services: watching.length,
    watching,
    rules: [
      { name: 'VM down', when: 'no answer for 2 min', state: 'ok' },
      { name: 'Disk filling up', when: 'over 85% full', state: MOCK_SERVICES.some(s => s.alerts?.length) ? 'firing' : 'ok' },
      { name: 'Pod crash-looping', when: 'more than 3 restarts in 15 min', state: 'ok' },
      { name: 'Service unreachable', when: 'an endpoint stops answering', state: 'ok' },
    ],
    firing: MOCK_SERVICES.reduce((n, s) => n + (s.alerts?.length ?? 0), 0),
    stored_bytes: 2_254_857_830,
    retention: '15d',
    cpu,
    dashboards: [
      {
        uid: 'nanocloud-overview',
        title: 'nano-cloud overview',
        about: 'Every VM, grouped by service: up, CPU, memory, disk, network',
        home: true,
      },
      {
        uid: 'nanocloud-node',
        title: 'Node details',
        about: 'One VM in depth: CPU by mode, memory, disk I/O, filesystems, network',
        var: 'vm',
      },
      {
        uid: 'nanocloud-kubernetes',
        title: 'Kubernetes',
        about: 'Nodes, pods by namespace, restarts, deployments',
        var: 'cluster',
        recipes: ['kubernetes', 'kubernetes_cluster'],
      },
      {
        uid: 'nanocloud-postgres',
        title: 'PostgreSQL',
        about: 'Connections, transactions per second, cache hit ratio, database size',
        var: 'db',
        recipes: ['postgres'],
      },
      {
        uid: 'nanocloud-mysql',
        title: 'MySQL',
        about: 'Connections, queries per second, slow queries, InnoDB buffer pool',
        var: 'db',
        recipes: ['mysql'],
      },
      { uid: 'nanocloud-prometheus', title: 'Prometheus itself', about: 'Scrape health, samples per second, storage growth' },
    ],
    provisioned: true,
    setup: [],
    read_at_unix: now,
  };
}

export function applyMockServiceChange(
  op: 'create' | 'delete' | 'start' | 'stop' | 'share' | 'unshare' | 'retry' | 'add-worker' | 'remove-worker' | 'publish' | 'unpublish',
  args: { name: string; payload?: CreateServicePayload; vm?: string; nodePort?: number; label?: string; scheme?: string },
): Service | undefined {
  const i = MOCK_SERVICES.findIndex(s => s.name === args.name);
  if (op === 'create') {
    if (i >= 0) throw new Error(`a service named "${args.name}" already exists`);
    const recipe = MOCK_SERVICE_CATALOG.find(r => r.id === args.payload?.recipe) ?? MOCK_SERVICE_CATALOG[0];
    const size = recipe.sizes.find(z => z.id === args.payload?.size) ?? recipe.sizes.find(z => z.default) ?? recipe.sizes[0];
    const members = mockMembers(args.name, recipe, Number(args.payload?.params?.workers ?? 2));
    const svc: Service = {
      name: args.name,
      recipe: recipe.id,
      recipe_name: recipe.name,
      category: recipe.category,
      size,
      state: 'pulling',
      progress: 35,
      vms: members.map(m => m.vm),
      members,
      created_at_unix: Math.floor(Date.now() / 1000),
      outputs: [],
      files: [],
      endpoints: [],
      shared: false,
      vm_user: 'nanocloud',
      vm_password: 'Mock8Pass2Word5Here9',
    };
    svc.endpoints = mockEndpoints(svc);
    MOCK_SERVICES.push(svc);
    return { ...svc };
  }
  if (i < 0) throw new Error(`there is no service named "${args.name}"`);
  if (op === 'delete') {
    MOCK_SERVICES.splice(i, 1);
    return undefined;
  }
  if (op === 'add-worker' || op === 'remove-worker') {
    const svc = MOCK_SERVICES[i];
    const members = svc.members ?? [];
    if (op === 'add-worker') {
      const vm = `${svc.name}-agent-${members.filter(m => m.role === 'agent').length + 1}`;
      members.push({ role: 'agent', role_label: 'Worker', vm, status: 'running', phase: 'booting' });
      svc.adding = [vm];
      svc.state = 'creating';
    } else {
      svc.removing = [args.vm!];
    }
    svc.members = svc.members ?? members;
    svc.vms = svc.members.map(m => m.vm);
    svc.scale = {
      role: 'agent',
      label: 'Worker',
      count: svc.members.filter(m => m.role === 'agent' && !svc.removing?.includes(m.vm)).length,
    };
    return { ...svc };
  }
  if (op === 'publish' || op === 'unpublish') {
    const svc = MOCK_SERVICES[i];
    const id = `np${args.nodePort}`;
    svc.endpoints = svc.endpoints.filter(e => e.id !== id);
    if (op === 'publish') {
      svc.endpoints.push({
        id,
        label: args.label ?? `NodePort ${args.nodePort}`,
        scheme: args.scheme,
        port: args.nodePort!,
        host_port: args.nodePort,
        address: `localhost:${args.nodePort}`,
        published: true,
      });
    }
    return { ...svc };
  }
  if (op === 'retry') {
    const svc = MOCK_SERVICES[i];
    if (svc.state !== 'failed') throw new Error(`${svc.name} has not failed, so there is nothing to retry`);
    svc.state = svc.failed_in ?? 'installing';
    svc.failed_in = undefined;
    svc.reason = undefined;
    return { ...svc };
  }
  if (op === 'share' || op === 'unshare') {
    const svc = MOCK_SERVICES[i];
    svc.shared = op === 'share';
    svc.endpoints = mockEndpoints(svc);
    if (svc.state === 'ready') svc.outputs = mockOutputs(svc);
    return { ...svc };
  }
  MOCK_SERVICES[i].state = op === 'stop' ? 'stopped' : 'ready';
  return { ...MOCK_SERVICES[i] };
}

// Settings and tokens change in place, so the mock UI shows its own edits.
export const MOCK_SETTINGS: Settings = {
  self_healing: true,
  update_check: true,
  pools: [
    { name: 'local-ssd', path: '/var/lib/nano/images', default: true, removable: false },
    { name: 'nvme-fast', path: '/mnt/nvme', default: false, removable: true },
    { name: 'data-hdd', path: '/mnt/data', default: false, removable: true },
  ],
  file: {
    config_path: '/etc/nano-cloud/config.yaml',
    listen: ':8080',
    libvirt_uri: 'qemu:///system',
    image_cache_dir: '/var/lib/nano-cloud/images',
    metadata_path: '/var/lib/nano-cloud/metadata.json',
    tls_cert: '/var/lib/nano-cloud/tls/cert.pem',
    tls_fingerprint: '3A:91:5F:0C:7E:B2:44:19:D8:6A:0F:E3:21:9C:57:AB:60:4D:F1:88:2E:C9:13:7B:A5:0E:96:3F:D4:12:BC:70',
  },
};

export const MOCK_TOKENS: APIToken[] = [
  { id: 'tok_1', name: 'backup script', created_at: '2026-11-02T10:00:00Z', last_used: '2026-11-13T06:00:00Z' },
  { id: 'tok_2', name: 'laptop', created_at: '2026-11-10T18:30:00Z', last_used: '0001-01-01T00:00:00Z' },
];

export const MOCK_UPDATE_STATUS: UpdateStatus = {
  current: 'v0.1.77',
  latest: { tag: 'v0.2.0', url: 'https://github.com/nanocloud-io/nanocloud/releases/latest', published_at: '2026-11-14T20:00:00Z' },
  available: true,
  checked_at: '2026-11-13T08:00:00Z',
};
