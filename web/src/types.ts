// VM types
export type VMStatus = 'running' | 'stopped' | 'error';
export type NetworkMode = 'nat' | 'bridge';
// Readiness derived server-side from status, IP and uptime: "booting" is
// running with no IP yet, "no-network" still has none past the grace period.
//
// "stopping" is client-side only: a graceful shutdown is a request the guest
// may ignore, so only the browser that sent it knows one is outstanding.
//
// "gave-up" and "crash-looping" come from the reconcile loop and only arrive
// with a non-running status.
export type VMPhase = 'stopped' | 'booting' | 'ready' | 'stopping' | 'no-network' | 'gave-up' | 'crash-looping';

export interface VM {
  id: string;
  name: string;
  status: VMStatus;
  phase: VMPhase;
  cpu_count: number;
  memory_mb: number;
  uptime_seconds: number;
  username?: string;
  // The service this VM was made for, when it was made by one.
  service?: string;
  // from metadata
  os?: string;
  disk_gb?: number;
  pool_name?: string;
  pool_path?: string;
  network_mode?: NetworkMode;
  // The bridge a bridge-mode VM is attached to, already resolved server-side
  // (an entry naming none means the host default). Empty on NAT.
  bridge?: string;
  // 802.1Q tag; 0 or absent is untagged.
  vlan_id?: number;
  ip?: string;
  mac?: string;
}

export interface VMStats {
  cpu_percent: number;
  memory_used_mb: number;
  memory_total_mb: number;
}

export interface Snapshot {
  name: string;
  created_at: string;
  description?: string;
}

// Image types
export type ImageState = 'cached' | 'idle' | 'pulling';

export interface CachedImage {
  id: string;
  name: string;
  os: string;
  size_bytes: number;
  cached_at: string;
}

export interface CatalogImage {
  id: string;
  name: string;
  os: string;
  arch: string;
  size_bytes: number;
  state: ImageState;
  progress?: number;
}

// Storage types
export interface StoragePool {
  name: string;
  path: string;
  free_bytes: number;
  total_bytes: number;
  default?: boolean;
}

// Network types

// The host's real Linux bridges, from GET /network/interfaces. A non-empty
// list is the authoritative signal that Bridge mode is available; the
// interface list carries no bridge flag to derive it from.
export interface HostBridge {
  name: string;
  // The bridge's own physical uplink, if any. Unused by the wizard, which puts
  // VLANs on the response's top-level `uplink`.
  uplink: string;
}

// A VM's networking, as POST /vms and the spec both carry it. One group,
// since a bridge and a tag mean nothing without the mode.
export interface VMNetwork {
  mode: NetworkMode;
  bridge: string;
  vlan_id: number;
}

export interface NetworkInterface {
  name: string;
  flags: string;
  addrs: string[];
  available: boolean;
}

export interface NetworkLease {
  vm_name: string;
  mac: string;
  ip: string;
  hostname?: string;
}

// System readiness checks (GET /system/deps)
// `remedy` is only present (Go `omitempty`) when `ok` is false and there is
// something actionable to run — treat it as optional, not an empty string.
export interface HealthCheck {
  name: string;
  ok: boolean;
  required: boolean;
  message: string;
  remedy?: string;
}

export interface SystemDeps {
  checks: HealthCheck[];
}

// Host passthrough inventory (GET /system/devices). A host with IOMMU off is
// a 200 with `enabled: false` plus a remedy — a state to report, not an error.

export interface IOMMUStatus {
  enabled: boolean;
  groups_seen: number;
  // Always populated, and truthful even when both device lists are empty.
  message: string;
  // Go `omitempty`: present only when something needs fixing.
  remedy?: string;
}

export interface PCIDevice {
  address: string;
  vendor: string;
  product: string;
  // Numeric IDs ("10de", "13ba"). libvirt does not name every device on every
  // host; where it does not and the host ships no pci.ids either, these are
  // the only thing telling two cards of the same class apart.
  vendor_id?: string;
  product_id?: string;
  class: string;
  // A path to other devices rather than an endpoint. Always unusable, and
  // skipped rather than carried along when it shares an IOMMU group.
  bridge?: boolean;
  // Host driver currently bound; "" when nothing is.
  driver: string;
  // -1 when libvirt reported no group, i.e. IOMMU is off or unknown.
  iommu_group: number;
  // The card driving the host console. Orthogonal to `usable`: such a card can
  // be perfectly passable and still be the wrong one to pass.
  boot_vga: boolean;
  // Other addresses in the same IOMMU group, passed through together or not at
  // all. Guaranteed non-null by the backend.
  siblings: string[];
  usable: boolean;
  // Present only when `usable` is false, and then always non-empty — the
  // backend never refuses a device without saying why.
  reason?: string;
}

export interface USBDevice {
  vendor_id: string;
  product_id: string;
  description: string;
  // Informational only: bus and device numbers are reassigned on replug, so
  // they are never the thing a passthrough spec matches on.
  bus_seen: string;
}

export interface HostDeviceReport {
  iommu: IOMMUStatus;
  pci: PCIDevice[];
  usb: USBDevice[];
}

// A VM's assigned devices (GET/PATCH /vms/{id}/spec). These are the *recorded*
// devices, which is not always what was picked: selecting a GPU brings its
// IOMMU-group siblings, and the backend returns the expanded set.

export interface AssignedPCIDevice {
  address: string;
  vendor?: string;
  product?: string;
}

export interface AssignedUSBDevice {
  vendor_id: string;
  product_id: string;
  description?: string;
}

// A VM's CPU shape: the CPU the guest sees, its layout, and the host CPUs it
// may run on. `mode` is always filled in on read; no recorded mode is host-model.
export interface CPUShape {
  mode: 'host-model' | 'host-passthrough';
  // null when the layout is left to libvirt. sockets × cores × threads must
  // equal the VM's vCPU count; libvirt refuses to define the domain otherwise.
  topology: CPUTopology | null;
  // libvirt's cpuset syntax ('4-7', '0,2,4', '0-7,^4'). Empty is unpinned.
  cpu_set: string;
}

export interface CPUTopology {
  sockets: number;
  cores: number;
  threads: number;
}

// One disk a VM has. The boot overlay is listed even though the spec's own
// disks leave it out; `boot` marks it so the client needn't know what "vda" means.
export interface VMDisk {
  target: string;
  path: string;
  size_gb: number;
  boot: boolean;
}

export interface VMSpec {
  pci_devices: AssignedPCIDevice[];
  usb_devices: AssignedUSBDevice[];
  cpu: CPUShape;
  network: VMNetwork;
  disks: VMDisk[];
  // The running machine differs from the spec. A rebuilt domain only reaches
  // the guest at its next boot, so this describes the VM, not the last request.
  restart_required: boolean;
  // Caveats that do not block the attach: a passed-through boot GPU, an IOMMU
  // group member libvirt could not describe. Recomputed on every read, so they
  // survive a reload and reflect the host as it is now.
  warnings: string[];
}

// The PATCH body. An omitted list means "leave this alone"; an empty array
// means "detach everything" — the distinction the backend's pointer fields
// preserve, and which a single optional array would collapse.
export interface VMSpecPatch {
  pci_devices?: string[];
  usb_devices?: string[];
  // The CPU is patched as one group: "no topology" is a value that a
  // per-field patch couldn't tell apart from "leave it alone".
  cpu?: CPUShape;
  // Patched as one group, for the same reason the CPU is.
  network?: VMNetwork;
}

// One file cloud-init writes into the guest on first boot. Create-time only:
// cloud-init runs user-data once, so there is no editing this afterwards.
export interface WriteFile {
  path: string;
  content: string;
  // An octal mode like "0644". A bare "644" is read as decimal by cloud-init.
  permissions?: string;
  owner?: string;
}

// A saved machine configuration (GET /templates), captured from a VM that
// already works. It describes a shape, not a machine: the name, hostname,
// storage pool and attached disks are deliberately not part of it, so a
// template can be stamped out repeatedly without two VMs ever sharing a file.
export interface VMTemplate {
  name: string;
  created_at_unix: number;
  // The VM the configuration came from. Provenance only — it may since have
  // been deleted or changed.
  source_vm: string;
  spec: TemplateSpec;
}

// Shaped like the POST /vms body rather than like the stored spec, because
// turning it back into a create request is the only thing a template is for.
export interface TemplateSpec {
  image_id: string;
  os: string;
  vcpus: number;
  memory_mb: number;
  disk_gb: number;
  network_mode: string;
  // '' for the BIOS default, matching what the create request sends.
  firmware: string;
  secure_boot: boolean;
  tpm: boolean;
  cpu: CPUShape;
  // In the create request's form: PCI addresses and "vendor:product" pairs.
  // Re-resolved against the live host at create time, so a template can name a
  // card that has since been removed and say so then rather than at save time.
  pci_devices: string[];
  usb_devices: string[];
  cloud_init: {
    username: string;
    packages: string[];
    runcmd: string[];
    write_files: WriteFile[];
    ssh_keys: string[];
  };
}

// Operational feed (GET /events). In-memory on the backend, resets on daemon
// restart — it narrates the reconcile loop's live state, not durable history.
export type EventLevel = 'info' | 'warn' | 'error';

export interface NanoEvent {
  seq: number;
  at: string; // RFC3339
  level: EventLevel;
  kind: string;
  vm?: string;
  message: string;
  detail?: string;
}

// One sample from GET /stats/history. 5s cadence, 1h retention server-side.
export interface StatsSample {
  at: number; // unix seconds
  cpu_percent: number;
  mem_used_mb: number;
  mem_total_mb: number;
}

// Services — a recipe from GET /services/catalog turned into a VM that runs it.
export interface ServiceSize {
  id: string;
  label: string;
  vcpus: number;
  memory_mb: number;
  disk_gb: number;
  default?: boolean;
}

// A setting a recipe takes. One with `generate` is a secret the daemon makes
// up and nobody types; one with `default` may be changed, within `pattern`.
export interface ServiceParam {
  id: string;
  label: string;
  default?: string;
  pattern?: string;
  hint?: string;
  generate?: number;
  secret?: boolean;
}

// One kind of machine in a service made of several, such as a cluster's
// control plane and its workers. A recipe without roles runs on one VM.
export interface ServiceRole {
  id: string;
  label: string;
  count?: number;
  // The setting whose value is the count, when a person chooses it.
  count_param?: string;
  after?: string;
  ports?: number[];
}

export interface ServiceRecipe {
  id: string;
  name: string;
  category: string;
  description: string;
  image: string;
  sizes: ServiceSize[];
  params: ServiceParam[];
  ports: number[];
  roles?: ServiceRole[];
  // "kubernetes" adds the console's Nodes and Workloads tabs.
  console?: string;
  endpoints: { id: string; label: string; role?: string; port: number; scheme?: string }[];
  outputs: { id: string; label: string; secret?: boolean }[];
  files: { id: string; label: string }[];
}

// The build steps in order, then how a built service is doing now.
export type ServiceState = 'pulling' | 'creating' | 'waiting-ip' | 'installing' | 'ready' | 'failed' | 'stopped' | 'starting' | 'missing';

// What a person connects to. nano-cloud forwards host_port on this host to
// port on the VM, so `address` stays the same while the VM's changes.
export interface ServiceEndpoint {
  id: string;
  label: string;
  // "http" or "https" for something a browser opens; absent for other TCP.
  scheme?: string;
  port: number;
  host_port?: number;
  // host:port through nano-cloud, as this browser reaches it.
  address?: string;
  // The VM's own address on the VM network, once it has one.
  direct?: string;
  // Why nano-cloud is not forwarding it, such as a port taken by another program.
  problem?: string;
  // A NodePort a person published, which they may unpublish.
  published?: boolean;
}

// A Kubernetes service's cluster as it is now (GET /services/{name}/cluster).
export interface ClusterNode {
  name: string;
  roles: string[];
  ready: boolean;
  unschedulable?: boolean;
  version: string;
  ip?: string;
  pods: number;
  cpu_percent?: number;
  memory_percent?: number;
}

export interface ClusterDeployment {
  namespace: string;
  name: string;
  ready: number;
  desired: number;
  images: string[];
  created_unix?: number;
}

export interface ClusterExposed {
  namespace: string;
  name: string;
  type: string;
  ports: { name?: string; port: number; node_port?: number; protocol: string }[];
}

export interface Cluster {
  version: string;
  nodes: ClusterNode[];
  deployments: ClusterDeployment[];
  exposed: ClusterExposed[];
  pods: { total: number; running: number; pending: number; failed: number; succeeded: number };
  read_at_unix: number;
}

// An alert firing in a monitoring service's Prometheus.
export interface ServiceAlert {
  name: string;
  title: string;
  summary: string;
  severity?: string;
  service?: string;
  vm?: string;
  since_unix?: number;
}

// What a monitoring service watches (GET /services/{name}/monitoring).
export interface Monitoring {
  targets_up: number;
  targets_total: number;
  vms: number;
  services: number;
  watching: { service: string; recipe?: string; vms: number; jobs: string[]; up: number; total: number }[];
  rules: { name: string; when?: string; state: 'ok' | 'pending' | 'firing' | string }[];
  firing: number;
  stored_bytes: number;
  retention?: string;
  // Each service's CPU use over the last hour: [unix seconds, percent].
  cpu: { name: string; points: [number, number][] }[];
  dashboards: { uid: string; title: string; about: string; home?: boolean; var?: string; recipes?: string[] }[];
  // Whether nano-cloud's dashboards and rules are installed yet.
  provisioned: boolean;
  // Exporters on their way to a VM, or that failed to set up.
  setup?: { service: string; vm: string; what: string; running?: boolean; problem?: string }[];
  read_at_unix: number;
}

export interface ApplyResult {
  ok: boolean;
  output: string;
}

export interface ServiceOutput {
  id: string;
  label: string;
  value: string;
  secret?: boolean;
}

export interface Service {
  name: string;
  recipe: string;
  recipe_name: string;
  category: string;
  size: ServiceSize;
  state: ServiceState;
  reason?: string;
  failed_in?: ServiceState;
  // Image download percentage, while pulling.
  progress?: number;
  vms: string[];
  ip?: string;
  created_at_unix: number;
  ready_at_unix?: number;
  // Filled in only once the service is ready and its VM has an address.
  outputs: ServiceOutput[];
  files: { id: string; label: string }[];
  endpoints: ServiceEndpoint[];
  // Whether other machines may connect to the endpoints, or only this host.
  shared: boolean;
  // "kubernetes" adds the console's Nodes and Workloads tabs; "monitoring"
  // what the service watches.
  console?: string;
  // Alerts a monitoring service has firing about this one.
  alerts?: ServiceAlert[];
  // The role a person may add machines to or remove them from.
  scale?: { role: string; label: string; count: number };
  // Machines on their way in or out of the service.
  adding?: string[];
  removing?: string[];
  // The service's VMs, each with its role and how it is doing now.
  members?: ServiceMember[];
  vm_user: string;
  vm_password: string;
}

export interface ServiceMember {
  role: string;
  role_label: string;
  vm: string;
  // The VM's own status, or "missing" once it has been deleted.
  status: string;
  phase?: string;
  ip?: string;
  // While the service is being built, and after its build failed: what its
  // install last reported (waiting, installing, ready, or failed with
  // failure saying why), the role it waits for, or why nano-cloud cannot
  // ask it and since when.
  install?: string;
  failure?: string;
  waits_for?: string;
  unreachable?: string;
  unreachable_since?: number;
}

// The end of one of a service's VMs' install log.
export interface ServiceLog {
  vm: string;
  // installing, waiting, ready or "failed: …"; cloud-init's status before
  // the install has started.
  state: string;
  log: string;
}

export interface CreateServicePayload {
  name: string;
  recipe: string;
  size?: string;
  params?: Record<string, string>;
  ssh_public_key?: string;
  github_user?: string;
}

// App routing
export type View = 'services' | 'service' | 'dashboard' | 'images' | 'detail' | 'settings';

// GET /system/update: whether a newer release exists. The daemon never
// installs one; `sudo nanocloud update` on the host does.
export interface UpdateRelease {
  tag: string;
  url: string;
  published_at: string;
}

export interface UpdateStatus {
  current: string;
  latest?: UpdateRelease;
  available: boolean;
  checked_at?: string;
  error?: string;
  // update_check: false in config.yaml — nothing is asked.
  disabled?: boolean;
}

// Settings: GET /settings. The editable values are the effective ones
// (settings.json over config.yaml); `file` is what only config.yaml changes.
export interface SettingsPool {
  name: string;
  path: string;
  default: boolean;
  // Added in Settings, so removable there; config.yaml's pools are not.
  removable: boolean;
}

export interface FileSettings {
  config_path: string;
  listen: string;
  libvirt_uri?: string;
  image_cache_dir: string;
  metadata_path: string;
  vz_network?: string;
  tls_cert?: string;
  tls_fingerprint?: string;
}

export interface Settings {
  self_healing: boolean;
  update_check: boolean;
  pools: SettingsPool[];
  file: FileSettings;
}

// null hands a value back to config.yaml.
export interface SettingsPatch {
  self_healing?: boolean | null;
  update_check?: boolean | null;
  default_pool?: string | null;
}

export interface APIToken {
  id: string;
  name: string;
  created_at: string;
  // The zero time (year 1) when the token has never been used.
  last_used?: string;
}

export interface NewAPIToken extends APIToken {
  // The secret, shown once: the daemon keeps only its hash.
  token: string;
}
