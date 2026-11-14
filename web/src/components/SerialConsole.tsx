import { useEffect, useRef, useState } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';
import { consoleWebSocketURL } from '../api';

interface SerialConsoleProps {
  vmId: string;
}

type ConsoleState = 'connecting' | 'connected' | 'disconnected';

// xterm draws on a canvas, which cannot resolve CSS variables, so the theme's
// terminal tokens are read out and handed over as plain colours.
function terminalLook() {
  const css = getComputedStyle(document.documentElement);
  const v = (name: string) => css.getPropertyValue(name).trim() || undefined;
  return {
    fontFamily: v('--nd-font-mono') ?? 'monospace',
    theme: {
      background: v('--nd-term'),
      foreground: v('--nd-term-text'),
      cursor: v('--nd-accent'),
      selectionBackground: v('--nd-selection'),
    },
  };
}

// SerialConsole is the console for backends whose guests have a serial port
// rather than a screen — Apple Virtualization on macOS. It is a terminal on
// the same WebSocket route the VNC console uses (GET /vms/{id}/console), which
// carries the guest's /dev/hvc0 byte stream instead of RFB. The daemon replays
// recent output on connect, so a reopened tab shows the login prompt.
export function SerialConsole({ vmId }: SerialConsoleProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [state, setState] = useState<ConsoleState>('connecting');

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    setState('connecting');

    const term = new Terminal({ cursorBlink: true, fontSize: 13, scrollback: 5000, ...terminalLook() });
    // Follow a theme switch made while the console is open.
    const retheme = new MutationObserver(() => (term.options.theme = terminalLook().theme));
    retheme.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    fit.fit();

    const ws = new WebSocket(consoleWebSocketURL(vmId));
    ws.binaryType = 'arraybuffer';
    const encoder = new TextEncoder();

    ws.onopen = () => {
      setState('connected');
      term.focus();
    };
    ws.onmessage = ev => {
      term.write(typeof ev.data === 'string' ? ev.data : new Uint8Array(ev.data as ArrayBuffer));
    };
    ws.onclose = () => setState('disconnected');
    const typed = term.onData(data => {
      // Binary frames: the daemon's proxy reads binary messages only.
      if (ws.readyState === WebSocket.OPEN) ws.send(encoder.encode(data));
    });

    // A serial line has no window size to negotiate, so fitting only changes
    // how much the terminal shows, never what the guest prints.
    const resize = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(() => fit.fit());
    resize?.observe(el);

    return () => {
      resize?.disconnect();
      retheme.disconnect();
      typed.dispose();
      ws.onopen = ws.onmessage = ws.onclose = null;
      ws.close();
      term.dispose();
    };
  }, [vmId]);

  return (
    <div
      style={{
        position: 'relative',
        background: 'var(--nd-term)',
        border: '1px solid var(--nd-line)',
        borderRadius: 14,
        overflow: 'hidden',
        padding: 10,
        height: 460,
        width: '100%',
        boxSizing: 'border-box',
      }}
    >
      <div ref={containerRef} data-testid="serial-console" data-console style={{ width: '100%', height: '100%' }} />
      {state !== 'connected' && (
        <div
          style={{
            position: 'absolute',
            inset: 0,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            color: 'var(--nd-term-text)',
            fontSize: 13,
            background: 'color-mix(in srgb, var(--nd-term) 88%, transparent)',
          }}
        >
          {state === 'connecting' ? 'Connecting to serial console…' : 'Serial console disconnected.'}
        </div>
      )}
    </div>
  );
}
