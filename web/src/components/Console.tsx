import { useEffect, useRef, useState } from 'react';
import RFB from '@novnc/novnc';
import { consoleWebSocketURL } from '../api';
import { Spinner } from '../ui/Spinner';

interface ConsoleProps {
  vmId: string;
}

type ConsoleState = 'connecting' | 'connected' | 'disconnected';

// Console renders an in-browser VNC console for a running VM, connecting
// noVNC's RFB client directly to the backend's WebSocket proxy
// (GET /vms/{id}/console). One RFB instance per mount; torn down on
// unmount or when vmId changes.
export function Console({ vmId }: ConsoleProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [state, setState] = useState<ConsoleState>('connecting');
  const [error, setError] = useState('');

  useEffect(() => {
    if (!containerRef.current) return;
    setState('connecting');
    setError('');

    const rfb = new RFB(containerRef.current, consoleWebSocketURL(vmId));
    // Scale the guest's native framebuffer down to fit our container, preserving
    // aspect ratio (noVNC letterboxes the difference against the dark background).
    rfb.scaleViewport = true;
    // Keep false. Asking the guest to resize breaks QEMU's text-mode boot
    // console, which can't reflow and ends up crammed into a corner.
    rfb.resizeSession = false;

    const onConnect = () => setState('connected');
    const onDisconnect = (e: CustomEvent<{ clean: boolean }>) => {
      setState('disconnected');
      if (!e.detail.clean) setError('Console connection lost.');
    };
    const onCredentials = () => setError('This VM requires console credentials, which nano-cloud does not support yet.');

    rfb.addEventListener('connect', onConnect);
    rfb.addEventListener('disconnect', onDisconnect as EventListener);
    rfb.addEventListener('credentialsrequired', onCredentials);

    return () => {
      rfb.removeEventListener('connect', onConnect);
      rfb.removeEventListener('disconnect', onDisconnect as EventListener);
      rfb.removeEventListener('credentialsrequired', onCredentials);
      rfb.disconnect();
    };
  }, [vmId]);

  return (
    <div
      style={{
        position: 'relative',
        background: 'var(--nd-screen)',
        border: '1px solid var(--nd-line)',
        borderRadius: 14,
        overflow: 'hidden',
        aspectRatio: '16 / 10',
        width: '100%',
      }}
    >
      <div ref={containerRef} data-console style={{ width: '100%', height: '100%' }} />

      {state !== 'connected' && (
        <div
          style={{
            position: 'absolute',
            inset: 0,
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            justifyContent: 'center',
            gap: 10,
            color: 'var(--nd-screen-text)',
            fontSize: 13,
            background: 'color-mix(in srgb, var(--nd-screen) 88%, transparent)',
          }}
        >
          {state === 'connecting' && !error && (
            <>
              <Spinner size={18} track="rgba(255, 255, 255, 0.15)" head="var(--nd-accent)" />
              Connecting to console…
            </>
          )}
          {state === 'disconnected' && <span>{error || 'Console disconnected.'}</span>}
          {error && state === 'connecting' && <span>{error}</span>}
        </div>
      )}
    </div>
  );
}
