import type { ReactNode } from 'react';
import type { HealthCheck } from '../../types';
import { BrandMark } from '../../ui/BrandMark';
import { Button } from '../../ui/Button';

interface FirstRunChecklistProps {
  readinessChecks: HealthCheck[];
  cachedImageCount: number;
  onOpenHost: () => void;
  onOpenImages: () => void;
  onNewVM: () => void;
}

// FirstRunChecklist is the VM list's empty state: the three things between a
// fresh install and a running VM, each ticked off from live state rather
// than remembered, so it is right after a reinstall too.
export function FirstRunChecklist({ readinessChecks, cachedImageCount, onOpenHost, onOpenImages, onNewVM }: FirstRunChecklistProps) {
  const failing = readinessChecks.filter(c => c.required && !c.ok).length;
  const hostReady = failing === 0;

  return (
    <section
      aria-labelledby="first-run-title"
      style={{
        background: 'var(--nd-surface)',
        border: '1px dashed var(--nd-line)',
        borderRadius: 16,
        padding: '40px clamp(16px, 4vw, 48px)',
        display: 'grid',
        gap: 'var(--nd-space-5)',
        justifyItems: 'start',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--nd-space-4)' }}>
        <BrandMark size={56} />
        <div>
          <h2 id="first-run-title" style={{ fontSize: 'var(--nd-text-xl)', fontWeight: 650 }}>
            No virtual machines yet
          </h2>
          <p className="nd-card-text">Three steps to the first one.</p>
        </div>
      </div>
      <ol style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: 'var(--nd-space-4)', width: '100%', maxWidth: 620 }}>
        <Step n={1} done={hostReady} title="This host is ready">
          {hostReady ? (
            'The hypervisor and the tools nano-cloud needs are in place.'
          ) : (
            <>
              {failing === 1 ? 'One requirement needs' : `${failing} requirements need`} attention first.{' '}
              <Button size="sm" variant="quiet" onClick={onOpenHost} style={{ color: 'var(--nd-accent)' }}>
                See what is missing
              </Button>
            </>
          )}
        </Step>
        <Step n={2} done={cachedImageCount > 0} title="Download a cloud image">
          {cachedImageCount > 0 ? (
            `${cachedImageCount} downloaded.`
          ) : (
            <>
              Or skip this: the wizard downloads the one you pick.{' '}
              <Button size="sm" variant="quiet" onClick={onOpenImages} style={{ color: 'var(--nd-accent)' }}>
                Browse images
              </Button>
            </>
          )}
        </Step>
        <Step n={3} done={false} title="Create your first VM">
          <Button variant="primary" onClick={onNewVM} disabled={!hostReady} style={{ marginTop: 'var(--nd-space-2)' }}>
            + New VM
          </Button>
        </Step>
      </ol>
    </section>
  );
}

function Step({ n, done, title, children }: { n: number; done: boolean; title: string; children: ReactNode }) {
  return (
    <li style={{ display: 'grid', gridTemplateColumns: '28px 1fr', gap: 'var(--nd-space-3)', alignItems: 'start' }}>
      <span
        aria-hidden="true"
        style={{
          width: 28,
          height: 28,
          borderRadius: '50%',
          display: 'grid',
          placeItems: 'center',
          fontSize: 'var(--nd-text-sm)',
          fontWeight: 600,
          border: `1px solid ${done ? 'var(--nd-ready-line)' : 'var(--nd-line)'}`,
          background: done ? 'var(--nd-ready-bg)' : 'transparent',
          color: done ? 'var(--nd-ready)' : 'var(--nd-muted)',
        }}
      >
        {done ? '✓' : n}
      </span>
      <div>
        <div style={{ fontWeight: 600, fontSize: 'var(--nd-text-md)' }}>
          {title}
          {done && <span className="nd-sr-only"> (done)</span>}
        </div>
        <div className="nd-card-text">{children}</div>
      </div>
    </li>
  );
}
