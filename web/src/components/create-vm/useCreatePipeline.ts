import { useEffect, useRef, useState } from 'react';
import type { CatalogImage, ImageState } from '../../types';
import { pullImage, getImageStatus, cancelPull } from '../../api';

export type CreatePhase = 'idle' | 'downloading' | 'creating';

// useCreatePipeline owns everything between pressing Create VM and the VM
// existing: downloading the image if it is not cached, then the create call.
// Selecting an image never waits on a download — Create does — so the request
// can never reach the backend for an image that is not on disk.
export function useCreatePipeline({
  catalog,
  pullProgress,
  pullErrors,
}: {
  catalog: CatalogImage[];
  // Shared with ImagesView and owned by App: progress of the step-1 standalone
  // Download button, which survives this modal closing.
  pullProgress: Record<string, number>;
  pullErrors: Record<string, string>;
}) {
  const [phase, setPhase] = useState<CreatePhase>('idle');
  const [progress, setProgress] = useState(0);
  const [error, setError] = useState('');

  // Per-image overlay for pulls started from Create VM, keyed by image id.
  // Takes precedence over the catalog prop, which only refreshes on the
  // dashboard's slow poll.
  const [imgState, setImgState] = useState<Record<string, ImageState>>({});
  const [imgProgress, setImgProgress] = useState<Record<string, number>>({});
  const [imgError, setImgError] = useState<Record<string, string>>({});
  const pullTimers = useRef<Record<string, ReturnType<typeof setInterval>>>({});
  // When true, the polling loop rejects early and the pipeline aborts cleanly.
  const cancelledRef = useRef(false);

  // The pipeline's own overlay wins (it is mid-flight), then the shared
  // Download button's progress, then the catalog prop.
  function effState(img: CatalogImage): ImageState {
    if (imgState[img.id]) return imgState[img.id];
    if (pullProgress[img.id] !== undefined) return 'pulling';
    return img.state;
  }

  function progressOf(img: CatalogImage): number {
    return imgProgress[img.id] ?? pullProgress[img.id] ?? 0;
  }

  function errorOf(img: CatalogImage): string {
    return imgError[img.id] || pullErrors[img.id] || '';
  }

  useEffect(() => {
    const timers = pullTimers.current;
    return () => {
      Object.values(timers).forEach(clearInterval);
    };
  }, []);

  function stopTimer(imageId: string) {
    if (pullTimers.current[imageId]) {
      clearInterval(pullTimers.current[imageId]);
      delete pullTimers.current[imageId];
    }
  }

  // Aborts the download-then-create pipeline. The Download button on an image
  // card uses the shared onPullImage/onCancelPullImage props instead.
  async function cancel(imageId: string) {
    stopTimer(imageId);
    setImgState(prev => ({ ...prev, [imageId]: 'idle' }));
    setImgProgress(prev => ({ ...prev, [imageId]: 0 }));
    setImgError(prev => ({ ...prev, [imageId]: '' }));

    cancelledRef.current = true;
    setPhase('idle');
    setProgress(0);
    setError('');

    try {
      await cancelPull(imageId);
    } catch (e) {
      // Non-fatal: polling is already stopped and the card is back to idle.
      console.error('Cancel pull failed', e);
    }
  }

  // Resolves once the image is cached; immediately if it already is.
  async function ensureImageCached(imageId: string): Promise<void> {
    const currentState = effState(catalog.find(i => i.id === imageId)!);
    if (currentState === 'cached') return;

    if (currentState !== 'pulling') {
      setImgState(prev => ({ ...prev, [imageId]: 'pulling' }));
      setImgProgress(prev => ({ ...prev, [imageId]: 0 }));
      setImgError(prev => ({ ...prev, [imageId]: '' }));
      await pullImage(imageId);
    }

    return new Promise((resolve, reject) => {
      stopTimer(imageId);
      const abort = (timer: ReturnType<typeof setInterval>) => {
        clearInterval(timer);
        delete pullTimers.current[imageId];
        reject(new Error('cancelled'));
      };
      const timer = setInterval(async () => {
        // cancel() sets this flag and clears the interval externally; this
        // guard covers a tick that fires between the two.
        if (cancelledRef.current) return abort(timer);
        try {
          const status = await getImageStatus(imageId);
          if (cancelledRef.current) return abort(timer);
          if (status.state === 'cached') {
            clearInterval(timer);
            delete pullTimers.current[imageId];
            setImgState(prev => ({ ...prev, [imageId]: 'cached' }));
            setImgProgress(prev => ({ ...prev, [imageId]: 100 }));
            setProgress(100);
            resolve();
          } else if (status.progress !== undefined) {
            setImgProgress(prev => ({ ...prev, [imageId]: status.progress! }));
            setProgress(status.progress!);
          }
        } catch (e) {
          clearInterval(timer);
          delete pullTimers.current[imageId];
          setImgState(prev => ({ ...prev, [imageId]: 'idle' }));
          const msg = e instanceof Error ? e.message : 'Download failed';
          setImgError(prev => ({ ...prev, [imageId]: msg }));
          reject(new Error(msg));
        }
      }, 2000);
      pullTimers.current[imageId] = timer;
    });
  }

  // run downloads the image if needed, then calls submit. A second call while
  // one is in flight is ignored, so a double click cannot race the shared
  // cancelledRef (a second run would reset the flag mid-flight).
  async function run(imageId: string, submit: () => Promise<void>) {
    if (!imageId || phase !== 'idle') return;
    setError('');
    cancelledRef.current = false;

    const img = catalog.find(i => i.id === imageId);
    const needsDownload = img && effState(img) !== 'cached';

    try {
      if (needsDownload) {
        setPhase('downloading');
        setProgress(0);
        await ensureImageCached(imageId);
      }
      if (cancelledRef.current) return;

      setPhase('creating');
      await submit();
    } catch (e) {
      // 'cancelled' is a clean, user-initiated abort: nothing to surface.
      if (e instanceof Error && e.message === 'cancelled') return;
      setError(e instanceof Error ? e.message : 'Failed to create VM');
      setPhase('idle');
    }
  }

  return { phase, progress, error, busy: phase !== 'idle', effState, progressOf, errorOf, run, cancel };
}
