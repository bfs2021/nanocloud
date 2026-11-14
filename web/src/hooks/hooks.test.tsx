import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api', () => ({ getSystemDeps: vi.fn() }));
import { getSystemDeps } from '../api';
import { useInterval } from './useInterval';
import { useReadiness } from './useHost';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('useInterval', () => {
  it('calls the latest callback, and stops while the delay is null', () => {
    const first = vi.fn();
    const second = vi.fn();
    const { rerender } = renderHook(({ fn, ms }) => useInterval(fn, ms), {
      initialProps: { fn: first, ms: 1000 as number | null },
    });
    act(() => vi.advanceTimersByTime(1000));
    rerender({ fn: second, ms: 1000 });
    act(() => vi.advanceTimersByTime(1000));
    expect(first).toHaveBeenCalledTimes(1);
    expect(second).toHaveBeenCalledTimes(1);

    rerender({ fn: second, ms: null });
    act(() => vi.advanceTimersByTime(5000));
    expect(second).toHaveBeenCalledTimes(1);
  });
});

describe('useReadiness', () => {
  it('rereads the checks, so a host fixed while the page is open clears', async () => {
    const failing = { checks: [{ name: 'libvirt', ok: false, required: true, detail: '' }] };
    vi.mocked(getSystemDeps).mockResolvedValue(failing as never);
    const { result } = renderHook(() => useReadiness());
    await act(async () => {});
    expect(result.current[0].ok).toBe(false);

    vi.mocked(getSystemDeps).mockResolvedValue({ checks: [{ ...failing.checks[0], ok: true }] } as never);
    await act(async () => {
      vi.advanceTimersByTime(30_000);
    });
    expect(result.current[0].ok).toBe(true);
  });
});
