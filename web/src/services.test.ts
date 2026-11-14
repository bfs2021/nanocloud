import { describe, it, expect, vi } from 'vitest';
import {
  buildProgress,
  memberDoing,
  memberOutcome,
  stuckMember,
  isLocalHostname,
  machineCount,
  machines,
  machineSummary,
  serviceRail,
  sizeSummary,
  suggestName,
  SERVICE_NAME_PATTERN,
} from './services';
import { MOCK_SERVICE_CATALOG } from './mock';

describe('serviceRail', () => {
  it.each([
    { state: 'pulling', want: ['active', 'todo', 'todo', 'todo', 'todo'] },
    { state: 'creating', want: ['done', 'active', 'todo', 'todo', 'todo'] },
    { state: 'waiting-ip', want: ['done', 'done', 'active', 'todo', 'todo'] },
    { state: 'installing', want: ['done', 'done', 'done', 'active', 'todo'] },
    { state: 'ready', want: ['done', 'done', 'done', 'done', 'done'] },
    { state: 'stopped', want: null },
    { state: 'starting', want: null },
    { state: 'missing', want: null },
  ] as const)('$state', ({ state, want }) => {
    expect(serviceRail({ state })).toEqual(want);
  });

  it('marks the step a failed service failed in', () => {
    expect(serviceRail({ state: 'failed', failed_in: 'installing' })).toEqual(['done', 'done', 'done', 'failed', 'todo']);
    expect(serviceRail({ state: 'failed', failed_in: 'pulling' })).toEqual(['failed', 'todo', 'todo', 'todo', 'todo']);
  });

  it('shows no rail for a failure it cannot place', () => {
    expect(serviceRail({ state: 'failed' })).toBeNull();
  });
});

describe('buildProgress', () => {
  it('says what a build is doing now', () => {
    expect(buildProgress({ state: 'pulling', progress: 40, recipe_name: 'PostgreSQL' })).toBe('Downloading the base image · 40%');
    expect(buildProgress({ state: 'installing', recipe_name: 'PostgreSQL' })).toBe('Installing PostgreSQL · usually a few minutes');
    expect(buildProgress({ state: 'ready', recipe_name: 'PostgreSQL' })).toBeNull();
  });

  it('names the machine holding a build up once it has not answered for a while', () => {
    const now = 1_800_000_000;
    const members = [
      {
        role: 'server',
        role_label: 'Control plane',
        vm: 'lab-server',
        status: 'running',
        unreachable: 'SSH does not answer at its address (i/o timeout)',
        unreachable_since: now - 360,
      },
      { role: 'agent', role_label: 'Worker', vm: 'lab-agent-1', status: 'running', install: 'waiting', waits_for: 'Control plane' },
    ];
    expect(stuckMember(members, now)).toEqual({ vm: 'lab-server', mins: 6 });
    expect(stuckMember([{ ...members[0], unreachable_since: now - 30 }], now)).toBeNull();
    vi.useFakeTimers();
    vi.setSystemTime(now * 1000);
    try {
      expect(buildProgress({ state: 'installing', recipe_name: 'Kubernetes cluster', members })).toBe(
        'Waiting on lab-server: not answering over SSH for 6 min',
      );
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('memberDoing', () => {
  const now = 1_800_000_000;
  it('says what a building machine is doing', () => {
    expect(memberDoing({ install: 'waiting', waits_for: 'Control plane' }, now)).toEqual({
      text: 'Waiting for the control plane',
      tone: 'idle',
    });
    expect(memberDoing({ install: 'installing' }, now)).toEqual({ text: 'Installing', tone: 'busy' });
    expect(memberDoing({ install: 'ready' }, now)).toEqual({ text: 'Installed', tone: 'ready' });
    expect(memberDoing({}, now)).toBeNull();
  });

  it('waits a little before calling a machine that does not answer a problem', () => {
    const why = 'SSH does not answer at its address (i/o timeout)';
    expect(memberDoing({ unreachable: why, unreachable_since: now - 20 }, now)).toEqual({
      text: 'Not answering over SSH yet',
      tone: 'busy',
      detail: why,
    });
    expect(memberDoing({ unreachable: why, unreachable_since: now - 300 }, now)).toEqual({
      text: 'Not answering over SSH for 5 min',
      tone: 'problem',
      detail: why,
    });
  });
});

describe('memberOutcome', () => {
  it('says where each machine of a failed service stood', () => {
    expect(memberOutcome({ install: 'failed', failure: 'k3s did not start' })).toEqual({
      text: 'Install failed',
      tone: 'problem',
      detail: 'K3s did not start.',
    });
    expect(memberOutcome({ install: 'ready' })).toEqual({ text: 'Installed', tone: 'ready' });
    expect(memberOutcome({ install: 'waiting', waits_for: 'Control plane' })).toEqual({
      text: 'Not installed: was waiting for the control plane',
      tone: 'idle',
    });
    expect(memberOutcome({ install: 'installing' })).toEqual({ text: 'Was still installing', tone: 'idle' });
    expect(memberOutcome({ unreachable: 'nothing listens on its SSH port' })).toEqual({
      text: 'Did not answer over SSH',
      tone: 'problem',
      detail: 'nothing listens on its SSH port',
    });
  });

  it('never calls a machine green for its VM alone', () => {
    expect(memberOutcome({ status: 'running' })).toEqual({ text: 'VM running', tone: 'idle' });
    expect(memberOutcome({ status: 'stopped' })).toBeNull();
  });
});

describe('suggestName', () => {
  it('takes the first free short name', () => {
    expect(suggestName({ id: 'kubernetes' }, new Set())).toBe('k8s-1');
    expect(suggestName({ id: 'postgres' }, new Set(['pg-1', 'pg-2']))).toBe('pg-3');
    expect(suggestName({ id: 's3' }, new Set(['pg-1']))).toBe('s3-1');
  });

  it('suggests only names the daemon accepts', () => {
    for (const id of ['kubernetes', 'postgres', 's3']) {
      expect(SERVICE_NAME_PATTERN.test(suggestName({ id }, new Set()))).toBe(true);
    }
  });
});

describe('sizeSummary', () => {
  it('reads a size the way people compare them', () => {
    expect(sizeSummary({ vcpus: 2, memory_mb: 4096, disk_gb: 30 })).toBe('2 vCPU · 4 GB · 30 GB disk');
    expect(sizeSummary({ vcpus: 1, memory_mb: 512, disk_gb: 5 })).toBe('1 vCPU · 512 MB · 5 GB disk');
  });
});

describe('machines', () => {
  const cluster = MOCK_SERVICE_CATALOG.find(r => r.id === 'kubernetes_cluster')!;
  const single = MOCK_SERVICE_CATALOG.find(r => r.id === 'postgres')!;

  it('counts one VM for a recipe without roles', () => {
    expect(machineCount(machines(single))).toBe(1);
  });

  it('reads a role count from its setting, falling back to the default', () => {
    expect(machineSummary(machines(cluster))).toBe('1 control plane + 2 workers');
    expect(machineCount(machines(cluster, { workers: '4' }))).toBe(5);
    expect(machineSummary(machines(cluster, { workers: '1' }))).toBe('1 control plane + 1 worker');
  });

  it('suggests a short name for every recipe', () => {
    expect(suggestName(cluster, new Set())).toBe('cluster-1');
    expect(suggestName({ id: 'observability' }, new Set())).toBe('obs-1');
    expect(suggestName({ id: 'mysql' }, new Set())).toBe('mysql-1');
  });
});

describe('isLocalHostname', () => {
  it('knows a browser on the same machine', () => {
    for (const h of ['localhost', 'LOCALHOST', 'app.localhost', '127.0.0.1', '127.1.2.3', '::1', '[::1]']) {
      expect(isLocalHostname(h)).toBe(true);
    }
    for (const h of ['mac-studio.local', '192.168.1.20', 'fd00::7', 'localhost.example.com']) {
      expect(isLocalHostname(h)).toBe(false);
    }
  });
});
