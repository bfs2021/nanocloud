import type { CPUShape, HostBridge, VMNetwork } from '../../types';
import type { CreateVMPayload } from '../../api';
import type { DeviceSelection } from '../DevicePicker';
import { DEFAULT_CPU_SHAPE, cpuSetError, isDefaultCPUShape, topologyError } from '../CPUShapePicker';
import { EMPTY_EXTRAS, toList, type CloudInitExtrasValue } from '../CloudInitExtras';

// Everything the wizard collects, in one place, so the steps are views of this
// object and the request is a function of it.
export interface FormState {
  imageId: string;
  name: string;
  cpu: number;
  ram: number; // GB
  disk: number; // GB
  pool: string;
  username: string;
  password: string;
  sshKey: string;
  network: VMNetwork;
  firmware: 'bios' | 'uefi';
  secureBoot: boolean;
  tpm: boolean;
  devices: DeviceSelection;
  cpuShape: CPUShape;
  extras: CloudInitExtrasValue;
}

// The order here is the order of the flow; nothing else lists the steps.
export const STEPS = [
  { id: 'image', label: 'Image' },
  { id: 'resources', label: 'Resources' },
  { id: 'access', label: 'Access' },
  { id: 'network', label: 'Network' },
  { id: 'review', label: 'Review' },
] as const;

export type StepId = (typeof STEPS)[number]['id'];

export function stepIndex(id: StepId): number {
  return STEPS.findIndex(s => s.id === id);
}

export function initialForm(defaultPool: string): FormState {
  return {
    imageId: '',
    name: 'my-vm',
    cpu: 2,
    ram: 4,
    disk: 20,
    pool: defaultPool,
    username: 'admin',
    password: '',
    sshKey: '',
    network: { mode: 'nat', bridge: '', vlan_id: 0 },
    // BIOS is the default, because Alpine has no signed shim and every catalog
    // image boots BIOS. UEFI is a deliberate choice, never a surprise.
    firmware: 'bios',
    secureBoot: false,
    tpm: false,
    devices: { pci: [], usb: [] },
    cpuShape: DEFAULT_CPU_SHAPE,
    extras: EMPTY_EXTRAS,
  };
}

// Mirrors vmNamePattern in internal/api/server.go so the person is told before
// submitting. The server remains the enforcer.
export const VM_NAME_PATTERN = /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$/;

export const NAME_RULE = 'Use only letters, digits, dots, dashes, and underscores, starting with a letter or digit (max 63 characters).';

// What the sliders may reach. 0 means the host's size is not known, and the
// wizard then falls back to the ceilings it always had.
export interface HostLimits {
  cpus: number;
  memoryGB: number;
}

export const DEFAULT_MAX_CPUS = 16;
export const DEFAULT_MAX_MEMORY_GB = 64;

export function hostLimits(hostCPUs?: number, hostMemoryMB?: number): HostLimits {
  return {
    cpus: hostCPUs && hostCPUs > 0 ? hostCPUs : 0,
    // Whole gigabytes, never below 1: the slider counts in GB and a host
    // reporting under one is not one a VM could be made on anyway.
    memoryGB: hostMemoryMB && hostMemoryMB > 0 ? Math.max(1, Math.floor(hostMemoryMB / 1024)) : 0,
  };
}

export function clampToLimits(form: Pick<FormState, 'cpu' | 'ram'>, limits: HostLimits): Pick<FormState, 'cpu' | 'ram'> {
  return {
    cpu: limits.cpus ? Math.min(form.cpu, limits.cpus) : form.cpu,
    ram: limits.memoryGB ? Math.min(form.ram, limits.memoryGB) : form.ram,
  };
}

export interface StepContext {
  limits: HostLimits;
  // The host's CPU count for cpuset checks: the prop when given, otherwise
  // what the CPU section fetched when it was opened. 0 = unknown.
  hostCPUs: number;
  bridges: HostBridge[];
}

export type FieldErrors = Partial<Record<'image' | 'name' | 'cpu' | 'ram' | 'disk' | 'cpuShape' | 'vlan', string>>;

// validateStep is the one place that says whether a step may be left. Continue
// is disabled on any error, and Review refuses to create while any step still
// has one — so a value that went stale after the step was passed (a template
// applied against smaller host limits) cannot reach the API.
export function validateStep(step: StepId, form: FormState, ctx: StepContext): FieldErrors {
  const errors: FieldErrors = {};

  if (step === 'image' && !form.imageId) errors.image = 'Choose an image to continue.';

  if (step === 'resources') {
    const name = form.name.trim();
    if (!name) errors.name = 'Give the VM a name.';
    else if (!VM_NAME_PATTERN.test(name)) errors.name = NAME_RULE;

    if (ctx.limits.cpus && form.cpu > ctx.limits.cpus) errors.cpu = `This host has ${ctx.limits.cpus} CPUs, so ${form.cpu} is too many.`;
    if (ctx.limits.memoryGB && form.ram > ctx.limits.memoryGB) {
      errors.ram = `This host has ${ctx.limits.memoryGB} GB of memory, so ${form.ram} GB is too much.`;
    }
    if (!Number.isInteger(form.disk) || form.disk < 1) errors.disk = 'Disk size must be a whole number of at least 1 GB.';

    // A CPU shape the backend would refuse blocks Continue here rather than
    // surfacing as a failed create on Review, a screen away from its field.
    const shape = topologyError(form.cpuShape, form.cpu) ?? cpuSetError(form.cpuShape.cpu_set, ctx.hostCPUs);
    if (shape) errors.cpuShape = shape;
  }

  if (step === 'network' && needsVlan(form.network, ctx.bridges)) {
    errors.vlan = 'Bridge mode needs a VLAN ID on this host, because it has no bridge with a physical port.';
  }

  return errors;
}

// needsVlan: with no host bridge, only a tagged VM can ride the nc-vlan<id>
// bridge nano-cloud builds itself, and an untagged one is refused by the API.
export function needsVlan(network: VMNetwork, bridges: HostBridge[]): boolean {
  return bridges.length === 0 && network.mode === 'bridge' && network.vlan_id === 0;
}

export function hasErrors(e: FieldErrors): boolean {
  return Object.keys(e).length > 0;
}

// firstInvalidStep finds the earliest step that would refuse to be left, so
// Review can say which one needs attention instead of failing on submit.
export function firstInvalidStep(form: FormState, ctx: StepContext): StepId | null {
  return STEPS.find(s => hasErrors(validateStep(s.id, form, ctx)))?.id ?? null;
}

// buildRequest is the POST /vms body. Every optional key is omitted for the
// default machine.
export function buildRequest(form: FormState): CreateVMPayload {
  const { extras } = form;
  const packages = toList(extras.packages, true);
  const runcmd = toList(extras.runcmd, false);
  const writeFiles = extras.writeFiles.filter(f => f.path.trim());

  return {
    name: form.name.trim(),
    image_id: form.imageId,
    vcpus: form.cpu,
    memory_mb: form.ram * 1024,
    disk_gb: form.disk,
    pool_name: form.pool,
    network_mode: form.network.mode,
    // The group only when it says something network_mode cannot: a named
    // bridge or a tag.
    ...(form.network.bridge || form.network.vlan_id ? { network: form.network } : {}),
    cloud_init: {
      username: form.username,
      password: form.password,
      ssh_public_key: form.sshKey,
      ...(extras.githubUser.trim() ? { github_user: extras.githubUser.trim() } : {}),
      ...(packages.length ? { packages } : {}),
      ...(runcmd.length ? { runcmd } : {}),
      ...(writeFiles.length ? { write_files: writeFiles } : {}),
    },
    ...(form.firmware === 'uefi' ? { firmware: 'uefi' as const } : {}),
    ...(form.secureBoot ? { secure_boot: true } : {}),
    ...(form.tpm ? { tpm: true } : {}),
    ...(form.devices.pci.length ? { pci_devices: form.devices.pci } : {}),
    ...(form.devices.usb.length ? { usb_devices: form.devices.usb } : {}),
    ...(isDefaultCPUShape(form.cpuShape) ? {} : { cpu: form.cpuShape }),
  };
}
