import { afterEach, describe, expect, it, vi } from 'vitest';
import { MOCK_SNAPSHOTS, MOCK_VMS, mockCreateVM, mockDeleteVM, mockSetPower, mockSnapshotChange } from './mock';

// The public demo has no daemon, so these are what make its actions stick.
describe('mock VM actions', () => {
  afterEach(() => vi.useRealTimers());

  it('lists a created VM, booting until it has an address', () => {
    vi.useFakeTimers();
    mockCreateVM({
      name: 'new-one',
      image_id: 'c1',
      vcpus: 2,
      memory_mb: 2048,
      disk_gb: 20,
      pool_name: 'local-ssd',
      network_mode: 'nat',
      username: 'me',
    });
    const vm = MOCK_VMS.find(v => v.name === 'new-one')!;
    expect(vm).toMatchObject({ status: 'running', phase: 'booting', os: 'ubuntu', pool_path: '/var/lib/nano/images' });
    vi.runAllTimers();
    expect(vm.phase).toBe('ready');
    expect(vm.ip).toMatch(/^192\.168\.122\./);
    expect(() =>
      mockCreateVM({ ...vm, image_id: 'c1', vcpus: 2, disk_gb: 20, pool_name: 'local-ssd', network_mode: 'nat', username: 'me' }),
    ).toThrow(/already exists/);
  });

  it('keeps a stopped VM stopped, and a deleted one gone', () => {
    mockSetPower('vm1', false);
    expect(MOCK_VMS.find(v => v.id === 'vm1')).toMatchObject({ status: 'stopped', phase: 'stopped' });
    mockDeleteVM('vm1');
    expect(MOCK_VMS.some(v => v.id === 'vm1')).toBe(false);
  });

  it('a VM that stops while booting does not come up ready', () => {
    vi.useFakeTimers();
    mockSetPower('vm3', true);
    mockSetPower('vm3', false);
    vi.runAllTimers();
    expect(MOCK_VMS.find(v => v.id === 'vm3')!.phase).toBe('stopped');
  });

  it('adds and removes snapshots', () => {
    mockSnapshotChange('vm4', 'create', 'before-upgrade');
    expect(MOCK_SNAPSHOTS.vm4[0].name).toBe('before-upgrade');
    mockSnapshotChange('vm4', 'delete', 'before-upgrade');
    expect(MOCK_SNAPSHOTS.vm4.map(s => s.name)).toEqual(['base']);
  });
});
