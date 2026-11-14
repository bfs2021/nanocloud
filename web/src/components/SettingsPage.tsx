import { useEffect, useRef, type ReactNode } from 'react';
import { formatRoute } from '../route';
import type { Settings } from '../types';
import { Spinner } from '../ui/Spinner';
import { AboutSection } from './settings/AboutSection';
import { useSettingsData, type Apply } from './settings/common';
import { GeneralSection } from './settings/GeneralSection';
import { HostSection } from './settings/HostSection';
import { SecuritySection } from './settings/SecuritySection';
import { SelfHealingSection } from './settings/SelfHealingSection';
import { StorageSection } from './settings/StorageSection';
import { UpdatesSection } from './settings/UpdatesSection';

interface Section {
  id: string;
  label: string;
  lede: string;
  // needs is set when the section edits GET /settings and cannot show
  // anything useful without it.
  render: (s: Settings | null, apply: Apply) => ReactNode;
  needs?: true;
}

const SECTIONS: Section[] = [
  { id: 'general', label: 'General', lede: 'How the app looks in this browser.', render: () => <GeneralSection /> },
  {
    id: 'security',
    label: 'Sign-in & security',
    lede: 'The password, tokens for scripts, and the certificate this page is served with.',
    render: s => <SecuritySection file={s?.file} />,
  },
  {
    id: 'updates',
    label: 'Updates',
    lede: 'Whether a newer release exists. nano-cloud never replaces itself; you run the update on the host.',
    render: (s, apply) => <UpdatesSection settings={s!} apply={apply} />,
    needs: true,
  },
  {
    id: 'storage',
    label: 'Storage',
    lede: 'Where VM disks and downloaded images are kept.',
    render: (s, apply) => <StorageSection settings={s!} apply={apply} />,
    needs: true,
  },
  {
    id: 'self-healing',
    label: 'Self-healing',
    lede: 'What nano-cloud does when a VM is not in the state you left it in.',
    render: (s, apply) => <SelfHealingSection settings={s!} apply={apply} />,
    needs: true,
  },
  {
    id: 'host',
    label: 'Host',
    lede: 'This machine: what it is ready for, its devices, and saved VM templates.',
    render: () => <HostSection />,
  },
  {
    id: 'about',
    label: 'About',
    lede: 'Versions, licence, and the values only config.yaml sets.',
    render: s => <AboutSection file={s?.file} />,
  },
];

export function SettingsPage({ section }: { section?: string }) {
  const { settings, error, apply } = useSettingsData();
  const current = SECTIONS.find(s => s.id === section) ?? SECTIONS[0];
  const nav = useRef<HTMLElement>(null);

  // On a phone the list is a row wider than the screen; keep the open
  // section in view.
  useEffect(() => {
    nav.current?.querySelector('[aria-current]')?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' });
  }, [current]);

  let body: ReactNode = current.render(settings, apply);
  if (current.needs && !settings) {
    body = error ? (
      <p role="alert" className="nd-field-error">
        Could not load settings: {error}
      </p>
    ) : (
      <div className="nd-card-text" style={{ display: 'flex', alignItems: 'center', gap: 9 }}>
        <Spinner size={15} shrink />
        Loading settings…
      </div>
    );
  }

  return (
    <div className="nd-settings">
      <nav ref={nav} aria-label="Settings sections" className="nd-settings-nav">
        {SECTIONS.map(s => (
          <a key={s.id} href={formatRoute({ view: 'settings', section: s.id })} aria-current={s === current ? 'page' : undefined}>
            {s.label}
          </a>
        ))}
      </nav>
      <div className="nd-settings-body">
        <header>
          <h1 className="nd-settings-title">{current.label}</h1>
          <p className="nd-settings-lede">{current.lede}</p>
        </header>
        {body}
      </div>
    </div>
  );
}
