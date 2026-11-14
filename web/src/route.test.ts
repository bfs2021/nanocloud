import { beforeEach, describe, expect, it } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { formatRoute, parseHash, useRoute, type Route } from './route';

describe('parseHash and formatRoute', () => {
  const routes: Route[] = [
    { view: 'dashboard' },
    { view: 'detail', vm: 'web-1' },
    { view: 'services' },
    { view: 'service', service: 'k8s' },
    { view: 'images' },
    { view: 'settings' },
    { view: 'settings', section: 'updates' },
  ];

  it.each(routes)('round-trips %o', r => {
    expect(parseHash(formatRoute(r))).toEqual(r);
  });

  it('opens the VM list for an empty, unknown or malformed hash', () => {
    for (const hash of ['', '#', '#/', '#/nope', '#/vms/%E0%A4%A']) {
      expect(parseHash(hash)).toEqual({ view: 'dashboard' });
    }
  });

  it('keeps a name that needs escaping intact', () => {
    const r: Route = { view: 'detail', vm: 'a b/c' };
    expect(formatRoute(r)).toBe('#/vms/a%20b%2Fc');
    expect(parseHash(formatRoute(r))).toEqual(r);
  });
});

describe('useRoute', () => {
  beforeEach(() => window.history.replaceState(null, '', '#/vms'));

  it('starts from the hash, follows Back, and titles the page', () => {
    window.history.replaceState(null, '', '#/images');
    const { result } = renderHook(() => useRoute());
    expect(result.current[0]).toEqual({ view: 'images' });
    expect(document.title).toBe('Images · nano-cloud');

    act(() => result.current[1]({ view: 'settings', section: 'updates' }));
    expect(window.location.hash).toBe('#/settings/updates');
    expect(result.current[0]).toEqual({ view: 'settings', section: 'updates' });

    // Back: the browser restores the hash and fires hashchange.
    act(() => {
      window.history.replaceState(null, '', '#/images');
      window.dispatchEvent(new HashChangeEvent('hashchange'));
    });
    expect(result.current[0]).toEqual({ view: 'images' });
  });

  it('replace leaves no history entry to go back to', () => {
    const { result } = renderHook(() => useRoute());
    const before = window.history.length;
    act(() => result.current[1]({ view: 'detail', vm: 'gone' }, { replace: true }));
    expect(window.history.length).toBe(before);
    expect(window.location.hash).toBe('#/vms/gone');
  });
});
