import { useCallback, useEffect, useRef, useState } from 'react';
import { cancelPull, getImageStatus, listCachedImages, listCatalog, pullImage } from '../api';
import type { CachedImage, CatalogImage } from '../types';
import { useInterval } from './useInterval';

function without<T>(rec: Record<string, T>, key: string): Record<string, T> {
  const next = { ...rec };
  delete next[key];
  return next;
}

function messageOf(e: unknown, fallback: string) {
  return e instanceof Error ? e.message : fallback;
}

// useImages holds the image lists and every download in progress. It lives in
// App because ImagesView and the wizard both unmount while the daemon keeps
// downloading.
export function useImages(onError: (title: string, e: unknown) => void) {
  const [cached, setCached] = useState<CachedImage[]>([]);
  const [catalog, setCatalog] = useState<CatalogImage[]>([]);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [pullProgress, setPullProgress] = useState<Record<string, number>>({});
  const [pullErrors, setPullErrors] = useState<Record<string, string>>({});
  const timers = useRef<Record<string, ReturnType<typeof setInterval>>>({});

  const stopPolling = useCallback((id: string) => {
    clearInterval(timers.current[id]);
    delete timers.current[id];
  }, []);

  // refresh and track call each other: a finished pull rereads the lists, and
  // the lists name pulls this tab is not yet tracking. The ref breaks the cycle.
  const refreshRef = useRef<() => Promise<void>>(async () => {});

  const track = useCallback(
    (id: string) => {
      if (timers.current[id]) return;
      timers.current[id] = setInterval(async () => {
        try {
          const status = await getImageStatus(id);
          if (status.state === 'cached') {
            stopPolling(id);
            setPullProgress(prev => without(prev, id));
            void refreshRef.current();
          } else if (status.progress !== undefined) {
            setPullProgress(prev => ({ ...prev, [id]: status.progress! }));
          }
        } catch (e) {
          stopPolling(id);
          setPullProgress(prev => without(prev, id));
          setPullErrors(prev => ({ ...prev, [id]: messageOf(e, 'Download failed') }));
        }
      }, 2000);
    },
    [stopPolling],
  );

  const refresh = useCallback(async () => {
    try {
      const [c, cat] = await Promise.all([listCachedImages(), listCatalog()]);
      setCached(c);
      setCatalog(cat);
      setLoadError(null);
      // Pick up a pull already running server-side: after a reload, or one
      // started from a view that has since unmounted.
      for (const img of cat) if (img.state === 'pulling') track(img.id);
    } catch (e) {
      setLoadError(messageOf(e, String(e)));
    }
  }, [track]);
  useEffect(() => {
    refreshRef.current = refresh;
  }, [refresh]);

  useEffect(() => {
    void refresh();
  }, [refresh]);
  useInterval(refresh, 5000);

  useEffect(() => {
    const all = timers.current;
    return () => Object.values(all).forEach(clearInterval);
  }, []);

  const pull = useCallback(
    async (id: string) => {
      setPullErrors(prev => without(prev, id));
      setPullProgress(prev => ({ ...prev, [id]: 0 }));
      try {
        await pullImage(id);
        track(id);
        void refresh();
      } catch (e) {
        setPullProgress(prev => without(prev, id));
        setPullErrors(prev => ({ ...prev, [id]: messageOf(e, 'Download failed') }));
      }
    },
    [track, refresh],
  );

  const cancel = useCallback(
    async (id: string) => {
      stopPolling(id);
      setPullProgress(prev => without(prev, id));
      setPullErrors(prev => without(prev, id));
      try {
        await cancelPull(id);
      } catch (e) {
        onError(`Failed to cancel the download of ${id}`, e);
      }
      void refresh();
    },
    [stopPolling, refresh, onError],
  );

  return { cached, catalog, loadError, pullProgress, pullErrors, refresh, pull, cancel };
}
