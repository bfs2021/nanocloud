import { useCallback, useEffect, useState } from 'react';
import type { View } from './types';

// The page the app shows, kept in the URL's hash so Back, a reload and a
// pasted link all land where they should. The hash rather than the path
// because the daemon and GitHub Pages both serve one index.html for everything.
export type Route =
  | { view: 'dashboard' }
  | { view: 'services' }
  | { view: 'images' }
  | { view: 'settings'; section?: string }
  | { view: 'detail'; vm: string }
  | { view: 'service'; service: string };

const DASHBOARD: Route = { view: 'dashboard' };

export function parseHash(hash: string): Route {
  const parts = hash.replace(/^#\/?/, '').split('/').filter(Boolean);
  let name: string | undefined;
  try {
    name = parts[1] === undefined ? undefined : decodeURIComponent(parts[1]);
  } catch {
    return DASHBOARD; // a malformed escape: not a page we can show
  }
  switch (parts[0]) {
    case undefined:
    case 'vms':
      return name ? { view: 'detail', vm: name } : DASHBOARD;
    case 'services':
      return name ? { view: 'service', service: name } : { view: 'services' };
    case 'images':
      return { view: 'images' };
    case 'settings':
      return name ? { view: 'settings', section: name } : { view: 'settings' };
    default:
      return DASHBOARD;
  }
}

export function formatRoute(r: Route): string {
  switch (r.view) {
    case 'dashboard':
      return '#/vms';
    case 'detail':
      return `#/vms/${encodeURIComponent(r.vm)}`;
    case 'services':
      return '#/services';
    case 'service':
      return `#/services/${encodeURIComponent(r.service)}`;
    case 'images':
      return '#/images';
    case 'settings':
      return r.section ? `#/settings/${encodeURIComponent(r.section)}` : '#/settings';
  }
}

// routeForView is the top-level page a sidebar item names.
export function routeForView(v: View): Route {
  switch (v) {
    case 'services':
    case 'images':
    case 'settings':
      return { view: v };
    default:
      return DASHBOARD;
  }
}

export function routeTitle(r: Route): string {
  switch (r.view) {
    case 'dashboard':
      return 'Virtual machines';
    case 'detail':
      return r.vm;
    case 'services':
      return 'Services';
    case 'service':
      return r.service;
    case 'images':
      return 'Images';
    case 'settings':
      return 'Settings';
  }
}

export type Navigate = (r: Route, opts?: { replace?: boolean }) => void;

// useRoute follows the hash. navigate adds a history entry, so Back returns
// to the previous page; replace is for leaving a page that no longer exists,
// such as a VM just deleted, which Back should not return to.
export function useRoute(): [Route, Navigate] {
  const [route, setRoute] = useState<Route>(() => parseHash(window.location.hash));

  useEffect(() => {
    const onChange = () => setRoute(parseHash(window.location.hash));
    window.addEventListener('hashchange', onChange);
    return () => window.removeEventListener('hashchange', onChange);
  }, []);

  useEffect(() => {
    document.title = `${routeTitle(route)} · nano-cloud`;
  }, [route]);

  const navigate = useCallback<Navigate>((r, opts) => {
    const hash = formatRoute(r);
    if (opts?.replace) {
      window.history.replaceState(null, '', hash);
    } else if (window.location.hash !== hash) {
      window.history.pushState(null, '', hash);
    }
    // Neither call fires hashchange, so the state follows by hand.
    setRoute(parseHash(hash));
  }, []);

  return [route, navigate];
}
