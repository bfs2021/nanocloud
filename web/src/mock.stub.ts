// Production stub — replaces mock.ts in production builds via vite alias.
// All exports are empty so tree-shaking eliminates them entirely.
// No mock data ever ships in production.
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
  ServiceRecipe,
  Cluster,
  Monitoring,
  UpdateStatus,
  Settings,
  APIToken,
} from './types';

export const MOCK_VMS: VM[] = [];
export const MOCK_STATS: Record<string, VMStats> = {};
export const MOCK_SNAPSHOTS: Record<string, Snapshot[]> = {};
export function mockSetPower(_id: string, _on: boolean): void {}
export type MockNewVM = unknown;
export function mockCreateVM(_p: MockNewVM): void {}
export function mockDeleteVM(_id: string): void {}
export function mockSnapshotChange(_vmId: string, _op: string, _name: string): void {}
export const MOCK_CACHED_IMAGES: CachedImage[] = [];
export const MOCK_CATALOG_IMAGES: CatalogImage[] = [];
export const MOCK_STORAGE_POOLS: StoragePool[] = [];
export const MOCK_BRIDGES: HostBridge[] = [];
export const MOCK_NETWORK_INTERFACES: NetworkInterface[] = [];
export const MOCK_UPLINK = '';
export const MOCK_NETWORK_LEASES: NetworkLease[] = [];
export const MOCK_SYSTEM_DEPS: SystemDeps = { checks: [] };
export const MOCK_HOST_DEVICES: HostDeviceReport = { iommu: { enabled: false, groups_seen: 0, message: '' }, pci: [], usb: [] };
export const MOCK_EVENTS: NanoEvent[] = [];
export const MOCK_STATS_HISTORY: Record<string, StatsSample[]> = {};
export const MOCK_VM_SPECS: Record<string, VMSpec> = {};
export const EMPTY_VM_SPEC: VMSpec = {
  pci_devices: [],
  usb_devices: [],
  cpu: { mode: 'host-model', topology: null, cpu_set: '' },
  network: { mode: 'nat', bridge: '', vlan_id: 0 },
  disks: [],
  restart_required: false,
  warnings: [],
};
export const MOCK_HOST_CPUS = 0;
export function applyMockSpecPatch(_id: string, _patch: VMSpecPatch): VMSpec {
  return EMPTY_VM_SPEC;
}
export function applyMockDiskChange(_id: string, _op: string, _args: unknown): VMSpec {
  return EMPTY_VM_SPEC;
}
export function mockTemplates(): VMTemplate[] {
  return [];
}
export function applyMockTemplateChange(_op: string, _args: unknown): VMTemplate {
  throw new Error('mock only');
}
export const MOCK_SERVICE_CATALOG: ServiceRecipe[] = [];
export function mockServices(): Service[] {
  return [];
}
export function applyMockServiceChange(_op: string, _args: unknown): Service | undefined {
  throw new Error('mock only');
}
export function mockServiceLog(_name: string, _vm: string): ServiceLog {
  throw new Error('mock only');
}
export function mockServiceVMs(): VM[] {
  return [];
}
export function mockCluster(_name: string): Cluster {
  throw new Error('mock only');
}
export function mockMonitoring(_name: string): Monitoring {
  throw new Error('mock only');
}
export function mockRestartVM(_vm: string): void {
  throw new Error('mock only');
}
export const MOCK_UPDATE_STATUS: UpdateStatus = { current: '', available: false };
export const MOCK_SETTINGS: Settings = {
  self_healing: false,
  update_check: false,
  pools: [],
  file: { config_path: '', listen: '', image_cache_dir: '', metadata_path: '' },
};
export const MOCK_TOKENS: APIToken[] = [];
