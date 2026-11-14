import { describe, it, expect } from 'vitest';
import { bootProgress, formatClock } from './design';
import type { VM } from './types';

type Booting = Pick<VM, 'status' | 'phase' | 'ip' | 'uptime_seconds'>;

describe('bootProgress', () => {
  it.each<[string, Booting, string | null]>([
    ['waiting for network', { status: 'running', phase: 'booting', uptime_seconds: 42 }, 'Starting · waiting for network · 0:42'],
    [
      'finishing boot once it has an address',
      { status: 'running', phase: 'booting', ip: '192.168.64.7', uptime_seconds: 75 },
      'Network up · finishing boot · 1:15',
    ],
    [
      'no network',
      { status: 'running', phase: 'no-network', uptime_seconds: 200 },
      'No network after 3 minutes · open the console to see why',
    ],
    ['nothing once ready', { status: 'running', phase: 'ready', ip: '192.168.64.7', uptime_seconds: 90 }, null],
    ['nothing while stopped', { status: 'stopped', phase: 'stopped', uptime_seconds: 0 }, null],
  ])('%s', (_, vm, want) => {
    expect(bootProgress(vm)).toBe(want);
  });
});

describe('formatClock', () => {
  it.each([
    [0, '0:00'],
    [9, '0:09'],
    [61, '1:01'],
    [600, '10:00'],
  ])('%i seconds is %s', (s, want) => {
    expect(formatClock(s)).toBe(want);
  });
});
