// API client — wraps fetch calls, handles mock mode for dev without a backend.
// Set VITE_MOCK=true in .env.local to use mock data.

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
  CPUShape,
  VMNetwork,
  WriteFile,
  VMTemplate,
  Service,
  ServiceLog,
  Cluster,
  ApplyResult,
  Monitoring,
  ServiceRecipe,
  CreateServicePayload,
  UpdateStatus,
  Settings,
  SettingsPatch,
  APIToken,
  NewAPIToken,
} from './types';

import {
  MOCK_VMS,
  MOCK_STATS,
  MOCK_SNAPSHOTS,
  MOCK_CACHED_IMAGES,
  MOCK_CATALOG_IMAGES,
  MOCK_STORAGE_POOLS,
  MOCK_BRIDGES,
  MOCK_NETWORK_INTERFACES,
  MOCK_UPLINK,
  MOCK_NETWORK_LEASES,
  MOCK_SYSTEM_DEPS,
  MOCK_UPDATE_STATUS,
  MOCK_SETTINGS,
  MOCK_TOKENS,
  MOCK_HOST_DEVICES,
  MOCK_EVENTS,
  MOCK_STATS_HISTORY,
  MOCK_VM_SPECS,
  EMPTY_VM_SPEC,
  MOCK_HOST_CPUS,
  applyMockSpecPatch,
  applyMockDiskChange,
  mockTemplates,
  applyMockTemplateChange,
  MOCK_SERVICE_CATALOG,
  mockServices,
  applyMockServiceChange,
  mockServiceLog,
  mockServiceVMs,
  mockRestartVM,
  mockSetPower,
  mockCreateVM,
  mockDeleteVM,
  mockSnapshotChange,
  mockCluster,
  mockMonitoring,
} from './mock';
import { DEMO } from './demo';
import { LIBVIRT_CAPABILITIES, VZ_CAPABILITIES, type Capabilities } from './capabilities';

const IS_MOCK = import.meta.env.VITE_MOCK === 'true';

function delay(ms = 200): Promise<void> {
  return new Promise(r => setTimeout(r, ms));
}

// jsonInit is the RequestInit for a request carrying body as JSON.
function jsonInit(method: string, body: unknown): RequestInit {
  return { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) };
}

// UNAUTHORIZED_EVENT fires on window whenever the daemon answers 401, so the
// auth gate can trade the app for the login page from any request, however
// deep in the tree it was made.
export const UNAUTHORIZED_EVENT = 'nanocloud:unauthorized';

// The page's half of the session. The daemon splits a browser session's
// secret: the cookie carries the other half, and browsers send a cookie to
// every port on the host, including services forwarded into VMs. This half is
// kept in this origin's storage, which no other port can read.
const SESSION_KEY = 'nanocloud.session-key';

function sessionKey(): string {
  try {
    return localStorage.getItem(SESSION_KEY) ?? '';
  } catch {
    return '';
  }
}

function setSessionKey(key: string | null) {
  try {
    if (key) localStorage.setItem(SESSION_KEY, key);
    else localStorage.removeItem(SESSION_KEY);
  } catch {
    // Without storage the session lasts as long as nothing reloads the page.
  }
}

// withSessionKey adds the key to a URL that cannot carry a header: a
// WebSocket, or a link the browser downloads.
function withSessionKey(url: string): string {
  const key = sessionKey();
  return key ? `${url}${url.includes('?') ? '&' : '?'}key=${encodeURIComponent(key)}` : url;
}

async function apiFetch<T>(path: string, options?: RequestInit): Promise<T> {
  const headers = new Headers(options?.headers);
  const key = sessionKey();
  if (key) headers.set('X-Nanocloud-Key', key);
  const res = await fetch(path, { ...options, headers });
  if (res.status === 401) window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    throw new Error(text || `HTTP ${res.status}`);
  }
  // 202 and 204 replies have empty bodies, which res.json() rejects. Read the
  // text once and parse only when there is any; void callers get undefined.
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

// Auth
export interface AuthStatus {
  authenticated: boolean;
  // No password has been set on the host yet; nothing can sign in until one is.
  setup_required: boolean;
}

export async function getAuthStatus(): Promise<AuthStatus> {
  if (IS_MOCK) return { authenticated: true, setup_required: false };
  return apiFetch<AuthStatus>('/auth/status');
}

export async function login(password: string): Promise<void> {
  const { key } = await apiFetch<{ key: string }>('/auth/login', jsonInit('POST', { password }));
  setSessionKey(key);
}

export async function logout(): Promise<void> {
  if (IS_MOCK) return;
  try {
    await apiFetch<unknown>('/auth/logout', { method: 'POST' });
  } finally {
    setSessionKey(null);
  }
}

export async function changePassword(current: string, next: string): Promise<void> {
  if (IS_MOCK) {
    await delay();
    return;
  }
  await apiFetch<unknown>('/auth/password', jsonInit('POST', { current, new: next }));
}

export async function listTokens(): Promise<APIToken[]> {
  if (IS_MOCK) {
    await delay();
    return [...MOCK_TOKENS];
  }
  return apiFetch<APIToken[]>('/auth/tokens');
}

export async function createToken(name: string): Promise<NewAPIToken> {
  if (IS_MOCK) {
    await delay();
    const tok = { id: `tok_${Date.now()}`, name, created_at: new Date().toISOString() };
    MOCK_TOKENS.push(tok);
    return { ...tok, token: 'nc_mock0123456789abcdefghijklmnopqrstuvwxyzABCD' };
  }
  return apiFetch<NewAPIToken>('/auth/tokens', jsonInit('POST', { name }));
}

export async function revokeToken(id: string): Promise<void> {
  if (IS_MOCK) {
    await delay();
    MOCK_TOKENS.splice(
      MOCK_TOKENS.findIndex(t => t.id === id),
      1,
    );
    return;
  }
  await apiFetch<void>(`/auth/tokens/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

// Settings
function mockSettings(): Settings {
  return { ...MOCK_SETTINGS, pools: MOCK_SETTINGS.pools.map(p => ({ ...p })) };
}

export async function getSettings(): Promise<Settings> {
  if (IS_MOCK) {
    await delay();
    return mockSettings();
  }
  return apiFetch<Settings>('/settings');
}

export async function patchSettings(patch: SettingsPatch): Promise<Settings> {
  if (IS_MOCK) {
    await delay();
    if (patch.self_healing != null) MOCK_SETTINGS.self_healing = patch.self_healing;
    if (patch.update_check != null) MOCK_SETTINGS.update_check = patch.update_check;
    if (patch.default_pool != null) MOCK_SETTINGS.pools.forEach(p => (p.default = p.name === patch.default_pool));
    return mockSettings();
  }
  return apiFetch<Settings>('/settings', jsonInit('PATCH', patch));
}

export async function addStoragePool(name: string, path: string): Promise<Settings> {
  if (IS_MOCK) {
    await delay(400);
    MOCK_SETTINGS.pools.push({ name, path, default: false, removable: true });
    return mockSettings();
  }
  return apiFetch<Settings>('/settings/pools', jsonInit('POST', { name, path }));
}

export async function removeStoragePool(name: string): Promise<Settings> {
  if (IS_MOCK) {
    await delay();
    MOCK_SETTINGS.pools = MOCK_SETTINGS.pools.filter(p => p.name !== name);
    return mockSettings();
  }
  return apiFetch<Settings>(`/settings/pools/${encodeURIComponent(name)}`, { method: 'DELETE' });
}

// VMs
export async function listVMs(): Promise<VM[]> {
  if (IS_MOCK) {
    await delay();
    return [...MOCK_VMS, ...mockServiceVMs()];
  }
  return apiFetch<VM[]>('/vms');
}

export async function startVM(id: string): Promise<void> {
  if (IS_MOCK) {
    await delay();
    mockRestartVM(id);
    mockSetPower(id, true);
    return;
  }
  await apiFetch<unknown>(`/vms/${id}/start`, { method: 'POST' });
}

// stopVM requests a graceful ACPI shutdown by default. Pass force to pull the
// power instead — the only way to stop a guest that runs no ACPI daemon and so
// ignores the polite request entirely.
export async function stopVM(id: string, force = false): Promise<void> {
  if (IS_MOCK) {
    await delay();
    mockSetPower(id, false);
    return;
  }
  const query = force ? '?force=true' : '';
  await apiFetch<unknown>(`/vms/${id}/stop${query}`, { method: 'POST' });
}

export async function deleteVM(id: string): Promise<void> {
  if (IS_MOCK) {
    await delay();
    mockDeleteVM(id);
    return;
  }
  await apiFetch<unknown>(`/vms/${id}`, { method: 'DELETE' });
}

// consoleWebSocketURL follows the page's protocol and host, so it works both
// behind Vite's dev proxy and in production.
export function consoleWebSocketURL(id: string): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return withSessionKey(`${proto}//${window.location.host}/vms/${encodeURIComponent(id)}/console`);
}

export interface CreateVMPayload {
  name: string;
  image_id: string;
  vcpus: number;
  memory_mb: number;
  disk_gb: number;
  pool_name: string;
  network_mode: string;
  // The network group, omitted while it adds nothing to network_mode.
  network?: VMNetwork;
  cloud_init: {
    username: string;
    password: string;
    ssh_public_key: string;
    hostname?: string;
    // First-boot provisioning, omitted when unused.
    github_user?: string;
    packages?: string[];
    runcmd?: string[];
    write_files?: WriteFile[];
  };
  // Firmware is omitted for the BIOS default; the backend treats absent and
  // 'bios' the same, and sending nothing keeps the key out of metadata.json.
  firmware?: 'uefi';
  secure_boot?: boolean;
  tpm?: boolean;
  // Host devices to assign: PCI addresses ("0000:01:00.0") and USB
  // "vendor:product" pairs ("0x1050:0x0407"). The backend expands PCI picks to
  // their whole IOMMU group, so the created VM can hold more than was sent.
  pci_devices?: string[];
  usb_devices?: string[];
  // Omitted for the default machine (host-model, no topology, unpinned), which
  // keeps the key out of metadata.json.
  cpu?: CPUShape;
}

// createVM returns any non-blocking caveats alongside the name — passing the
// host's boot GPU is legitimate but worth saying out loud.
export async function createVM(payload: CreateVMPayload): Promise<{ name: string; warnings?: string[] }> {
  if (IS_MOCK) {
    await delay(800);
    mockCreateVM({ ...payload, username: payload.cloud_init.username });
    return { name: payload.name };
  }
  return apiFetch<{ name: string; warnings?: string[] }>('/vms', jsonInit('POST', payload));
}

export async function getVMStats(id: string): Promise<VMStats> {
  if (IS_MOCK) {
    await delay(100);
    const base = MOCK_STATS[id] || { cpu_percent: 0, memory_used_mb: 0, memory_total_mb: 0 };
    // Add slight random noise for animation feel
    return {
      cpu_percent: Math.max(1, Math.min(99, base.cpu_percent + (Math.random() - 0.5) * 10)),
      memory_used_mb: base.memory_used_mb,
      memory_total_mb: base.memory_total_mb,
    };
  }
  return apiFetch<VMStats>(`/vms/${id}/stats`);
}

// Snapshots
export async function listSnapshots(vmId: string): Promise<Snapshot[]> {
  if (IS_MOCK) {
    await delay();
    return MOCK_SNAPSHOTS[vmId] || [];
  }
  return apiFetch<Snapshot[]>(`/vms/${vmId}/snapshots`);
}

export async function createSnapshot(vmId: string, name: string): Promise<void> {
  if (IS_MOCK) {
    await delay(500);
    mockSnapshotChange(vmId, 'create', name);
    return;
  }
  await apiFetch<unknown>(`/vms/${vmId}/snapshots`, jsonInit('POST', { name }));
}

export async function deleteSnapshot(vmId: string, snapName: string): Promise<void> {
  if (IS_MOCK) {
    await delay();
    mockSnapshotChange(vmId, 'delete', snapName);
    return;
  }
  await apiFetch<unknown>(`/vms/${vmId}/snapshots/${snapName}`, { method: 'DELETE' });
}

// restoreSnapshot always leaves the VM stopped, whatever its state when the
// snapshot was taken, so callers must refresh VM state afterwards.
export async function restoreSnapshot(vmId: string, snapName: string): Promise<void> {
  if (IS_MOCK) {
    await delay();
    return;
  }
  await apiFetch<unknown>(`/vms/${vmId}/snapshots/${snapName}/restore`, { method: 'POST' });
}

// Images
export async function listCachedImages(): Promise<CachedImage[]> {
  if (IS_MOCK) {
    await delay();
    return MOCK_CACHED_IMAGES;
  }
  return apiFetch<CachedImage[]>('/images');
}

// Raw catalog shape from the backend. The backend uses `status`
// ("cached"|"pulling"|"available"); the frontend uses `state`. Normalize here
// so every consumer works against the real API, not just mock mode.
interface RawCatalogEntry {
  id: string;
  name: string;
  os: string;
  arch: string;
  size_bytes: number;
  status: 'cached' | 'pulling' | 'available';
  progress: number;
}

function normalizeState(status: RawCatalogEntry['status']): CatalogImage['state'] {
  if (status === 'cached') return 'cached';
  if (status === 'pulling') return 'pulling';
  return 'idle';
}

export async function listCatalog(): Promise<CatalogImage[]> {
  if (IS_MOCK) {
    await delay();
    return MOCK_CATALOG_IMAGES;
  }
  const raw = await apiFetch<RawCatalogEntry[]>('/images/catalog');
  return raw.map(r => ({
    id: r.id,
    name: r.name,
    os: r.os,
    arch: r.arch,
    size_bytes: r.size_bytes,
    state: normalizeState(r.status),
    progress: r.progress,
  }));
}

export async function pullImage(imageId: string): Promise<void> {
  if (IS_MOCK) {
    await delay();
    return;
  }
  await apiFetch<unknown>('/images/pull', jsonInit('POST', { id: imageId }));
}

export interface ImageStatus {
  state: 'idle' | 'pulling' | 'cached';
  progress?: number;
}

// Raw pull-status shape from the backend: {id, progress, done, error}.
interface RawImageStatus {
  id: string;
  progress: number;
  done: boolean;
  error: string;
}

// Mock-mode download progress, advanced on each poll so a pull actually
// completes in the UI without a backend.
const mockPullProgress: Record<string, number> = {};

export async function getImageStatus(imageId: string): Promise<ImageStatus> {
  if (IS_MOCK) {
    await delay(100);
    const next = Math.min(100, (mockPullProgress[imageId] ?? 0) + 25);
    mockPullProgress[imageId] = next;
    if (next >= 100) return { state: 'cached', progress: 100 };
    return { state: 'pulling', progress: next };
  }
  const raw = await apiFetch<RawImageStatus>(`/images/${imageId}/status`);
  if (raw.error) throw new Error(raw.error);
  return {
    state: raw.done ? 'cached' : 'pulling',
    progress: raw.progress,
  };
}

export async function deleteImage(imageId: string): Promise<void> {
  if (IS_MOCK) {
    await delay();
    return;
  }
  await apiFetch<unknown>(`/images/${imageId}`, { method: 'DELETE' });
}

export async function cancelPull(imageId: string): Promise<void> {
  if (IS_MOCK) {
    await delay(100);
    // Reset mock progress so the next pull starts fresh.
    delete mockPullProgress[imageId];
    return;
  }
  await apiFetch<unknown>(`/images/${imageId}/pull`, { method: 'DELETE' });
}

// Storage
export async function listStoragePools(): Promise<StoragePool[]> {
  if (IS_MOCK) {
    await delay();
    return MOCK_STORAGE_POOLS;
  }
  return apiFetch<StoragePool[]>('/storage/pools');
}

// System
export interface SystemInfo {
  hostname: string;
  cpu_percent: number;
  memory_used_mb: number;
  memory_total_mb: number;
  // The host's CPU count, 0 when libvirt could not be reached. It names the
  // range a cpuset may address, so the pinning field can say what is valid
  // instead of waiting for the backend to say what was not.
  cpus: number;
  // Which hypervisor runs this daemon's VMs, and what it can do. Absent from
  // daemons that predate the macOS backend, which were all libvirt.
  backend?: string;
  capabilities?: Capabilities;
}

export async function getSystemInfo(): Promise<SystemInfo> {
  if (IS_MOCK) {
    await delay(100);
    // VITE_MOCK_BACKEND=vz previews the UI a macOS daemon gets.
    const capabilities = import.meta.env.VITE_MOCK_BACKEND === 'vz' ? VZ_CAPABILITIES : LIBVIRT_CAPABILITIES;
    return {
      hostname: 'dev-machine',
      cpu_percent: 14,
      memory_used_mb: 4096,
      memory_total_mb: 16384,
      cpus: MOCK_HOST_CPUS,
      backend: capabilities.backend,
      capabilities,
    };
  }
  return apiFetch<SystemInfo>('/system');
}

// fetchGitHubKeys previews what a GitHub import would install, before the VM
// exists. The daemon does the fetching — the guest never contacts GitHub — so
// this is also a check that the daemon itself can reach it.
export async function fetchGitHubKeys(user: string): Promise<string[]> {
  if (IS_MOCK) {
    await delay(400);
    if (user === 'nobody') throw new Error(`cloudinit: GitHub has no account named "${user}"`);
    return [`ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMockKey ${user}@github`];
  }
  const res = await apiFetch<{ keys: string[] }>(`/cloud-init/github-keys?user=${encodeURIComponent(user)}`);
  return res.keys;
}

// System deps
export async function getUpdateStatus(): Promise<UpdateStatus> {
  if (IS_MOCK) {
    await delay();
    // The demo is the current release, not one with an update waiting.
    if (DEMO) return { ...MOCK_UPDATE_STATUS, current: MOCK_UPDATE_STATUS.latest?.tag ?? '', available: false };
    return MOCK_UPDATE_STATUS;
  }
  return apiFetch<UpdateStatus>('/system/update');
}

export async function getSystemDeps(): Promise<SystemDeps> {
  if (IS_MOCK) {
    await delay();
    // The failing checks are there to exercise the warnings in development;
    // a visitor to the demo would read them as the product being broken.
    if (DEMO) return { checks: MOCK_SYSTEM_DEPS.checks.filter(c => c.ok || !c.required) };
    return MOCK_SYSTEM_DEPS;
  }
  return apiFetch<SystemDeps>('/system/deps');
}

// getHostDevices reads the passthrough inventory. IOMMU off is a 200 with
// `iommu.enabled: false`, not an error; only an unreachable libvirt rejects.
export async function getHostDevices(): Promise<HostDeviceReport> {
  if (IS_MOCK) {
    await delay();
    return MOCK_HOST_DEVICES;
  }
  return apiFetch<HostDeviceReport>('/system/devices');
}

// The spec's restart_required and warnings are derived from the live domain
// and host, so the Hardware tab fetches it on every mount rather than caching.
export async function getVMSpec(id: string): Promise<VMSpec> {
  if (IS_MOCK) {
    await delay();
    return MOCK_VM_SPECS[id] || EMPTY_VM_SPEC;
  }
  return apiFetch<VMSpec>(`/vms/${id}/spec`);
}

// patchVMSpec writes the device selection and rebuilds the domain. The response
// is the resolved spec — expanded with any IOMMU-group siblings — so callers
// should render what comes back rather than what they sent.
export async function patchVMSpec(id: string, patch: VMSpecPatch): Promise<VMSpec> {
  if (IS_MOCK) {
    await delay(500);
    const next = applyMockSpecPatch(id, patch);
    return next;
  }
  return apiFetch<VMSpec>(`/vms/${id}/spec`, jsonInit('PATCH', patch));
}

// Disks. All three reply with the whole spec, so the caller re-renders that
// rather than patching its own copy.
export async function attachDisk(id: string, sizeGB: number): Promise<VMSpec> {
  if (IS_MOCK) {
    await delay(500);
    return applyMockDiskChange(id, 'attach', { sizeGB });
  }
  return apiFetch<VMSpec>(`/vms/${id}/disks`, jsonInit('POST', { size_gb: sizeGB }));
}

// deleteFile is the destructive half and defaults to off: detaching is how a
// disk moves between VMs, so it must not lose data unless asked.
export async function detachDisk(id: string, target: string, deleteFile: boolean): Promise<VMSpec> {
  if (IS_MOCK) {
    await delay(400);
    return applyMockDiskChange(id, 'detach', { target });
  }
  const query = deleteFile ? '?delete_file=true' : '';
  return apiFetch<VMSpec>(`/vms/${id}/disks/${target}${query}`, { method: 'DELETE' });
}

export async function resizeDisk(id: string, target: string, sizeGB: number): Promise<VMSpec> {
  if (IS_MOCK) {
    await delay(500);
    return applyMockDiskChange(id, 'resize', { target, sizeGB });
  }
  return apiFetch<VMSpec>(`/vms/${id}/disks/${target}/resize`, jsonInit('POST', { size_gb: sizeGB }));
}

// Templates — saved machine configurations, captured from a VM that already
// works and replayed into the create wizard.
export async function listTemplates(): Promise<VMTemplate[]> {
  if (IS_MOCK) {
    await delay();
    return mockTemplates();
  }
  return apiFetch<VMTemplate[]>('/templates');
}

export async function saveTemplate(name: string, fromVM: string): Promise<VMTemplate> {
  if (IS_MOCK) {
    await delay(400);
    return applyMockTemplateChange('save', { name, fromVM });
  }
  return apiFetch<VMTemplate>('/templates', jsonInit('POST', { name, from_vm: fromVM }));
}

export async function deleteTemplate(name: string): Promise<void> {
  if (IS_MOCK) {
    await delay(300);
    applyMockTemplateChange('delete', { name });
    return;
  }
  await apiFetch<void>(`/templates/${encodeURIComponent(name)}`, { method: 'DELETE' });
}

// Services — what can be launched, and what has been. A create answers at
// once with the service as it starts building; its progress shows in later
// listServices polls.
export async function listServiceCatalog(): Promise<ServiceRecipe[]> {
  if (IS_MOCK) {
    await delay();
    return MOCK_SERVICE_CATALOG;
  }
  const recipes = await apiFetch<ServiceRecipe[]>('/services/catalog');
  return recipes.map(withRecipeLists);
}

// withRecipeLists fills in lists a recipe leaves out; older daemons sent null.
export function withRecipeLists(r: ServiceRecipe): ServiceRecipe {
  return {
    ...r,
    params: r.params ?? [],
    ports: r.ports ?? [],
    endpoints: r.endpoints ?? [],
    outputs: r.outputs ?? [],
    files: r.files ?? [],
  };
}

export async function listServices(): Promise<Service[]> {
  if (IS_MOCK) {
    await delay();
    return mockServices();
  }
  return apiFetch<Service[]>('/services');
}

export async function createService(payload: CreateServicePayload): Promise<Service> {
  if (IS_MOCK) {
    await delay(400);
    return applyMockServiceChange('create', { name: payload.name, payload })!;
  }
  return apiFetch<Service>('/services', jsonInit('POST', payload));
}

export async function deleteService(name: string): Promise<void> {
  if (IS_MOCK) {
    await delay(600);
    applyMockServiceChange('delete', { name });
    return;
  }
  await apiFetch<void>(`/services/${encodeURIComponent(name)}`, { method: 'DELETE' });
}

export async function startService(name: string): Promise<void> {
  if (IS_MOCK) {
    await delay();
    applyMockServiceChange('start', { name });
    return;
  }
  await apiFetch<void>(`/services/${encodeURIComponent(name)}/start`, { method: 'POST' });
}

export async function stopService(name: string): Promise<void> {
  if (IS_MOCK) {
    await delay();
    applyMockServiceChange('stop', { name });
    return;
  }
  await apiFetch<void>(`/services/${encodeURIComponent(name)}/stop`, { method: 'POST' });
}

// setServiceShared lets other machines reach a service's endpoints, or only
// the host nano-cloud runs on.
export async function setServiceShared(name: string, shared: boolean): Promise<Service> {
  if (IS_MOCK) {
    await delay();
    return applyMockServiceChange(shared ? 'share' : 'unshare', { name })!;
  }
  return apiFetch<Service>(`/services/${encodeURIComponent(name)}/share`, jsonInit('POST', { shared }));
}

// retryService picks a failed service up again at the step it failed in.
export async function retryService(name: string): Promise<Service> {
  if (IS_MOCK) {
    await delay();
    return applyMockServiceChange('retry', { name })!;
  }
  return apiFetch<Service>(`/services/${encodeURIComponent(name)}/retry`, { method: 'POST' });
}

// getServiceLog reads the end of one of a service's VMs' install log.
export async function getServiceLog(name: string, vm: string, lines = 200): Promise<ServiceLog> {
  if (IS_MOCK) {
    await delay(150);
    return mockServiceLog(name, vm);
  }
  return apiFetch<ServiceLog>(`/services/${encodeURIComponent(name)}/logs/${encodeURIComponent(vm)}?lines=${lines}`);
}

// getServiceCluster reads a Kubernetes service's nodes and workloads.
export async function getServiceCluster(name: string): Promise<Cluster> {
  if (IS_MOCK) {
    await delay(150);
    return mockCluster(name);
  }
  return apiFetch<Cluster>(`/services/${encodeURIComponent(name)}/cluster`);
}

// getServiceMonitoring reads what a monitoring service watches.
export async function getServiceMonitoring(name: string): Promise<Monitoring> {
  if (IS_MOCK) {
    await delay(150);
    return mockMonitoring(name);
  }
  return apiFetch<Monitoring>(`/services/${encodeURIComponent(name)}/monitoring`);
}

// addServiceWorkers adds machines to a running service's scalable role.
export async function addServiceWorkers(name: string, add = 1): Promise<Service> {
  if (IS_MOCK) {
    await delay();
    return applyMockServiceChange('add-worker', { name })!;
  }
  return apiFetch<Service>(`/services/${encodeURIComponent(name)}/workers`, jsonInit('POST', { add }));
}

// removeServiceWorker drains one machine, then takes it out and deletes it.
export async function removeServiceWorker(name: string, vm: string): Promise<Service> {
  if (IS_MOCK) {
    await delay();
    return applyMockServiceChange('remove-worker', { name, vm })!;
  }
  return apiFetch<Service>(`/services/${encodeURIComponent(name)}/workers/${encodeURIComponent(vm)}`, { method: 'DELETE' });
}

// applyManifest runs kubectl apply on a Kubernetes service's control plane.
export async function applyManifest(name: string, manifest: string): Promise<ApplyResult> {
  if (IS_MOCK) {
    await delay(400);
    return { ok: true, output: 'deployment.apps/hello created' };
  }
  return apiFetch<ApplyResult>(`/services/${encodeURIComponent(name)}/apply`, jsonInit('POST', { manifest }));
}

// publishNodePort forwards one of a cluster's NodePorts from this host.
export async function publishNodePort(name: string, nodePort: number, label: string, scheme?: string): Promise<Service> {
  if (IS_MOCK) {
    await delay();
    return applyMockServiceChange('publish', { name, nodePort, label, scheme })!;
  }
  return apiFetch<Service>(`/services/${encodeURIComponent(name)}/published`, jsonInit('POST', { node_port: nodePort, label, scheme }));
}

// unpublishNodePort stops forwarding a published NodePort.
export async function unpublishNodePort(name: string, nodePort: number): Promise<Service> {
  if (IS_MOCK) {
    await delay();
    return applyMockServiceChange('unpublish', { name, nodePort })!;
  }
  return apiFetch<Service>(`/services/${encodeURIComponent(name)}/published/${nodePort}`, { method: 'DELETE' });
}

// serviceFileURL is a plain link target: the browser downloads the file
// itself, named by the daemon's Content-Disposition.
export function serviceFileURL(name: string, file: string): string {
  return withSessionKey(`/services/${encodeURIComponent(name)}/files/${encodeURIComponent(file)}`);
}

// cloneVM copies every disk, so it can take minutes; callers must show that it
// is working. Warnings ride along: a clone does not inherit passed-through
// host devices.
export async function cloneVM(id: string, name: string, start: boolean): Promise<{ name: string; warnings?: string[] }> {
  if (IS_MOCK) {
    await delay(1500);
    return { name };
  }
  return apiFetch<{ name: string; warnings?: string[] }>(`/vms/${encodeURIComponent(id)}/clone`, jsonInit('POST', { name, start }));
}

// Network
export async function listNetworkInterfaces(): Promise<{ bridges: HostBridge[]; interfaces: NetworkInterface[]; uplink: string }> {
  if (IS_MOCK) {
    await delay();
    // VITE_MOCK_BRIDGES=none is an ordinary Ubuntu host (an uplink, no bridge);
    // =isolated also has no default route.
    const shape = import.meta.env.VITE_MOCK_BRIDGES;
    return {
      bridges: shape ? [] : MOCK_BRIDGES,
      interfaces: MOCK_NETWORK_INTERFACES,
      uplink: shape === 'isolated' ? '' : MOCK_UPLINK,
    };
  }
  return apiFetch<{ bridges: HostBridge[]; interfaces: NetworkInterface[]; uplink: string }>('/network/interfaces');
}

export async function listNetworkLeases(): Promise<NetworkLease[]> {
  if (IS_MOCK) {
    await delay();
    return MOCK_NETWORK_LEASES;
  }
  return apiFetch<NetworkLease[]>('/network/leases');
}

// Events feed
export interface EventsPage {
  events: NanoEvent[];
  cursor: number;
}

// listEvents polls the operational feed. Pass the previous call's cursor as
// `since`; a cursor ahead of the in-memory ring (after a daemon restart) is
// replayed from the oldest event server-side.
export async function listEvents(since = 0, limit = 200): Promise<EventsPage> {
  if (IS_MOCK) {
    await delay(150);
    // Mock has no real ring to advance through — report everything once,
    // then nothing, so a polling caller doesn't see the same events forever.
    return since > 0 ? { events: [], cursor: since } : { events: MOCK_EVENTS, cursor: MOCK_EVENTS.length };
  }
  return apiFetch<EventsPage>(`/events?since=${since}&limit=${limit}`);
}

// Stats history
// getStatsHistory returns the 5s-sampled CPU/memory series. Omit vm for the
// host series; a single VM name switches to that VM's own series.
export async function getStatsHistory(vm?: string, since = 0): Promise<StatsSample[]> {
  if (IS_MOCK) {
    await delay(100);
    return MOCK_STATS_HISTORY[vm ?? 'host'] ?? [];
  }
  const params = new URLSearchParams();
  if (vm) params.set('vm', vm);
  if (since) params.set('since', String(since));
  const q = params.toString();
  const resp = await apiFetch<{ samples: StatsSample[] }>(`/stats/history${q ? `?${q}` : ''}`);
  return resp.samples;
}
