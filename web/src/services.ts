// How services look and read: their colours, their states, and how far a
// build has come. Kept beside design.ts rather than in it so the VM-side
// helpers there stay about VMs.

import type { Service, ServiceMember, ServiceRecipe, ServiceState } from './types';
import type { StepState } from './ui/BootRail';

// Each recipe's chip colour: the brand colour where it has one, so a
// PostgreSQL card is recognisable before it is read.
export const SERVICE_COLORS: Record<string, string> = {
  kubernetes: '#326ce5',
  kubernetes_cluster: '#326ce5',
  postgres: '#336791',
  mysql: '#00758f',
  s3: '#c2410c',
  observability: '#e6522c',
};

export function serviceColor(recipe: string): string {
  return SERVICE_COLORS[recipe] ?? 'var(--nd-muted)';
}

// The same badge vocabulary as a VM's (design.ts STATUS_META): pulsing means
// work in progress, a ready service glows.
export const SERVICE_STATE_META: Record<
  ServiceState,
  { label: string; color: string; bg: string; dot: string; pulse: boolean; glow: boolean }
> = {
  pulling: { label: 'Downloading', color: 'var(--nd-warn)', bg: 'var(--nd-warn-bg)', dot: 'var(--nd-warn)', pulse: true, glow: false },
  creating: { label: 'Creating', color: 'var(--nd-warn)', bg: 'var(--nd-warn-bg)', dot: 'var(--nd-warn)', pulse: true, glow: false },
  'waiting-ip': { label: 'Booting', color: 'var(--nd-warn)', bg: 'var(--nd-warn-bg)', dot: 'var(--nd-warn)', pulse: true, glow: false },
  installing: { label: 'Installing', color: 'var(--nd-warn)', bg: 'var(--nd-warn-bg)', dot: 'var(--nd-warn)', pulse: true, glow: false },
  ready: { label: 'Ready', color: 'var(--nd-ready)', bg: 'var(--nd-ready-bg)', dot: 'var(--nd-ready)', pulse: false, glow: true },
  failed: { label: 'Failed', color: 'var(--nd-danger)', bg: 'var(--nd-danger-bg)', dot: 'var(--nd-danger)', pulse: false, glow: false },
  stopped: { label: 'Stopped', color: 'var(--nd-muted)', bg: 'var(--nd-surface-2)', dot: 'var(--nd-muted)', pulse: false, glow: false },
  starting: { label: 'Starting', color: 'var(--nd-warn)', bg: 'var(--nd-warn-bg)', dot: 'var(--nd-warn)', pulse: true, glow: false },
  missing: { label: 'VM missing', color: 'var(--nd-alert)', bg: 'var(--nd-alert-bg)', dot: 'var(--nd-alert)', pulse: false, glow: false },
};

export function serviceStateMeta(state: string) {
  return SERVICE_STATE_META[state as ServiceState] ?? SERVICE_STATE_META.stopped;
}

// The build steps, in the order the boot rail shows them: Image, VM,
// Network, Install, Ready.
const BUILD_STEPS: ServiceState[] = ['pulling', 'creating', 'waiting-ip', 'installing'];

export function building(state: ServiceState): boolean {
  return BUILD_STEPS.includes(state);
}

// serviceRail says how far a build has come, or null for a service with no
// build to show (stopped, starting, missing).
export function serviceRail(svc: Pick<Service, 'state' | 'failed_in'>): StepState[] | null {
  const at = (i: number, last: StepState): StepState[] => [0, 1, 2, 3, 4].map(n => (n < i ? 'done' : n === i ? last : 'todo'));
  if (svc.state === 'ready') return at(5, 'done');
  if (svc.state === 'failed') {
    const i = svc.failed_in ? BUILD_STEPS.indexOf(svc.failed_in) : -1;
    return i >= 0 ? at(i, 'failed') : null;
  }
  const i = BUILD_STEPS.indexOf(svc.state);
  return i >= 0 ? at(i, 'active') : null;
}

// STUCK_AFTER is how long a machine may not answer over SSH before it is
// called out: a booting VM's SSH comes up a little after its address.
const STUCK_AFTER = 120;

export type MemberTone = 'ready' | 'busy' | 'idle' | 'problem';

// memberDoing says what one machine of a building service is doing, from
// nano-cloud's last look at its install; null when it has nothing to add.
export function memberDoing(
  m: Pick<ServiceMember, 'install' | 'waits_for' | 'unreachable' | 'unreachable_since'>,
  now = Date.now() / 1000,
): { text: string; tone: MemberTone; detail?: string } | null {
  if (m.unreachable) {
    const secs = m.unreachable_since ? now - m.unreachable_since : 0;
    if (secs < STUCK_AFTER) return { text: 'Not answering over SSH yet', tone: 'busy', detail: m.unreachable };
    return { text: `Not answering over SSH for ${Math.floor(secs / 60)} min`, tone: 'problem', detail: m.unreachable };
  }
  switch (m.install) {
    case 'waiting':
      return { text: m.waits_for ? `Waiting for the ${m.waits_for.toLowerCase()}` : 'Waiting', tone: 'idle' };
    case 'installing':
      return { text: 'Installing', tone: 'busy' };
    case 'ready':
      return { text: 'Installed', tone: 'ready' };
  }
  return null;
}

// memberOutcome says where one machine of a failed service stood when its
// build stopped. Its VM may well be running, but that alone is not a green:
// without word from its install it reads "VM running" and no more.
export function memberOutcome(
  m: Pick<ServiceMember, 'install' | 'failure' | 'waits_for' | 'unreachable'> & { status?: string },
): { text: string; tone: MemberTone; detail?: string } | null {
  if (m.install === 'failed') return { text: 'Install failed', tone: 'problem', detail: m.failure && asSentence(m.failure) };
  if (m.unreachable) return { text: 'Did not answer over SSH', tone: 'problem', detail: m.unreachable };
  switch (m.install) {
    case 'ready':
      return { text: 'Installed', tone: 'ready' };
    case 'waiting':
      return { text: m.waits_for ? `Not installed: was waiting for the ${m.waits_for.toLowerCase()}` : 'Not installed', tone: 'idle' };
    case 'installing':
      return { text: 'Was still installing', tone: 'idle' };
  }
  return m.status === 'running' ? { text: 'VM running', tone: 'idle' } : null;
}

// asSentence capitalises a reason the daemon words to follow "on <vm>, ",
// and ends it with a full stop.
function asSentence(s: string): string {
  const t = s.trim();
  return t.charAt(0).toUpperCase() + t.slice(1) + (/[.!?]$/.test(t) ? '' : '.');
}

// stuckMember is a building service's machine that has not answered over
// SSH for a while, holding the build up, with its minutes; null when none.
export function stuckMember(members: ServiceMember[] | undefined, now = Date.now() / 1000): { vm: string; mins: number } | null {
  for (const m of members ?? []) {
    if (m.unreachable && m.unreachable_since && now - m.unreachable_since >= STUCK_AFTER) {
      return { vm: m.vm, mins: Math.floor((now - m.unreachable_since) / 60) };
    }
  }
  return null;
}

// buildProgress is the one line under a building service saying what it is
// doing now, or which machine holds it up.
export function buildProgress(svc: Pick<Service, 'state' | 'progress' | 'recipe_name'> & { members?: ServiceMember[] }): string | null {
  const stuck = building(svc.state) ? stuckMember(svc.members) : null;
  if (stuck) return `Waiting on ${stuck.vm}: not answering over SSH for ${stuck.mins} min`;
  switch (svc.state) {
    case 'pulling':
      return svc.progress ? `Downloading the base image · ${svc.progress}%` : 'Downloading the base image';
    case 'creating':
      return 'Creating its VM';
    case 'waiting-ip':
      return 'Booting · waiting for the network';
    case 'installing':
      return `Installing ${svc.recipe_name} · usually a few minutes`;
    case 'starting':
      return 'Starting · waiting for the network';
    default:
      return null;
  }
}

// sizeSummary reads a size the way people compare them.
export function sizeSummary(s: { vcpus: number; memory_mb: number; disk_gb: number }): string {
  const mem = s.memory_mb >= 1024 ? `${s.memory_mb / 1024} GB` : `${s.memory_mb} MB`;
  return `${s.vcpus} vCPU · ${mem} · ${s.disk_gb} GB disk`;
}

// suggestName offers the first free "<short>-<n>" for a recipe, free among
// both services and VMs because a service's VM takes its name.
const SHORT_NAMES: Record<string, string> = {
  kubernetes: 'k8s',
  kubernetes_cluster: 'cluster',
  postgres: 'pg',
  observability: 'obs',
};

export function suggestName(recipe: Pick<ServiceRecipe, 'id'>, taken: Set<string>): string {
  const short = SHORT_NAMES[recipe.id] ?? recipe.id.replace(/_/g, '-');
  for (let n = 1; ; n++) {
    const name = `${short}-${n}`;
    if (!taken.has(name)) return name;
  }
}

// The name rule, as internal/services checks it.
export const SERVICE_NAME_PATTERN = /^[a-z]([a-z0-9-]{0,38}[a-z0-9])?$/;

// machines says how many VMs a recipe makes with the given settings, role by
// role: one for a recipe without roles.
export function machines(
  recipe: Pick<ServiceRecipe, 'roles' | 'params'>,
  params: Record<string, string> = {},
): { label: string; count: number }[] {
  if (!recipe.roles?.length) return [{ label: '', count: 1 }];
  return recipe.roles.map(role => {
    let count = role.count ?? 0;
    if (role.count_param) {
      const raw = params[role.count_param] ?? recipe.params.find(p => p.id === role.count_param)?.default ?? '';
      count = Number.parseInt(raw, 10) || 0;
    }
    return { label: role.label, count };
  });
}

// machineSummary reads a cluster's shape: "1 control plane + 2 workers".
export function machineSummary(parts: { label: string; count: number }[]): string {
  return parts.map(p => `${p.count} ${p.label.toLowerCase()}${p.count === 1 || !p.label ? '' : 's'}`).join(' + ');
}

export function machineCount(parts: { count: number }[]): number {
  return parts.reduce((n, p) => n + p.count, 0);
}

// isLocalHostname reports whether a browser that reached nano-cloud at this
// hostname runs on the same machine, where localhost addresses reach the
// services.
export function isLocalHostname(hostname: string): boolean {
  const h = hostname.toLowerCase().replace(/^\[|\]$/g, '');
  return h === 'localhost' || h.endsWith('.localhost') || h === '::1' || /^127\./.test(h);
}
