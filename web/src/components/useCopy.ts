import { useEffect, useRef, useState } from 'react';

// useCopy copies text to the clipboard and reports "copied" for a moment
// afterwards, so a button can say so.
export function useCopy(resetMs = 1500) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>();
  useEffect(() => () => clearTimeout(timer.current), []);

  function copy(text: string) {
    if (!text) return;
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true);
      clearTimeout(timer.current);
      timer.current = setTimeout(() => setCopied(false), resetMs);
    });
  }
  return { copied, copy };
}
