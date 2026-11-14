import { useEffect, useRef } from 'react';

// useInterval calls fn every ms milliseconds, or not at all while ms is null.
// It always calls the latest fn, so a caller's closure never reads stale state
// and a new fn each render does not restart the timer.
export function useInterval(fn: () => void, ms: number | null) {
  const latest = useRef(fn);
  useEffect(() => {
    latest.current = fn;
  });
  useEffect(() => {
    if (ms === null) return;
    const id = setInterval(() => latest.current(), ms);
    return () => clearInterval(id);
  }, [ms]);
}
