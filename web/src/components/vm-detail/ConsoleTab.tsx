import { lazy, Suspense } from 'react';
import { DEMO } from '../../demo';
import { Console } from '../Console';

// Loaded on demand: only a macOS daemon's VMs have a serial console, so Linux
// users never download the terminal emulator.
const SerialConsole = lazy(() => import('../SerialConsole').then(m => ({ default: m.SerialConsole })));

interface ConsoleTabProps {
  vmId: string;
  name: string;
  running: boolean;
  // Serial instead of VNC, for a backend that has no VNC console.
  serial: boolean;
}

export function ConsoleTab({ vmId, name, running, serial }: ConsoleTabProps) {
  if (!running) {
    return (
      <div
        style={{
          background: 'var(--nd-surface)',
          border: '1px dashed var(--nd-line)',
          borderRadius: 14,
          padding: '48px 24px',
          textAlign: 'center',
          color: 'var(--nd-text-2)',
          fontSize: 13.5,
        }}
      >
        Start the VM to open its console.
      </div>
    );
  }
  if (DEMO) return <DemoScreen name={name} />;
  if (serial) {
    return (
      <Suspense fallback={null}>
        <SerialConsole vmId={vmId} />
      </Suspense>
    );
  }
  return <Console vmId={vmId} />;
}

// DemoScreen stands in for the console in the public demo, where there is no
// VM to connect to: a still of the login prompt the guest would show.
function DemoScreen({ name }: { name: string }) {
  return (
    <figure style={{ margin: 0 }}>
      <pre
        style={{
          margin: 0,
          background: 'var(--nd-screen)',
          color: 'var(--nd-screen-text)',
          border: '1px solid var(--nd-line)',
          borderRadius: 14,
          aspectRatio: '16 / 10',
          padding: '20px 24px',
          fontFamily: 'var(--nd-font-mono)',
          fontSize: 13,
          lineHeight: 1.6,
          overflow: 'hidden',
        }}
      >
        {`Ubuntu 24.04.1 LTS ${name} tty1\n\n${name} login: `}
        <span className="nd-demo-cursor">▌</span>
      </pre>
      <figcaption style={{ marginTop: 10, fontSize: 13, color: 'var(--nd-text-2)' }}>
        This is a demo with no VM behind it. On your own host, this is the VM's screen, live in the browser.
      </figcaption>
    </figure>
  );
}
