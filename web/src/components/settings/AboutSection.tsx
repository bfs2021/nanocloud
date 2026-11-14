import { useEffect, useState } from 'react';
import { getUpdateStatus } from '../../api';
import { backendLabel, useCapabilities } from '../../capabilities';
import type { FileSettings } from '../../types';
import { FactList, SettingsCard } from './common';

const REPO = 'https://github.com/nanocloud-io/nanocloud';

export function AboutSection({ file }: { file?: FileSettings }) {
  const caps = useCapabilities();
  const [daemon, setDaemon] = useState<string | null>(null);

  useEffect(() => {
    getUpdateStatus().then(
      s => setDaemon(s.current),
      () => setDaemon(null),
    );
  }, []);

  const mono = (v?: string) => (v ? <span className="nd-mono">{v}</span> : '—');

  return (
    <>
      <SettingsCard title="Nano Cloud">
        <FactList
          facts={[
            ['Daemon', mono(daemon ?? undefined)],
            ['Web app', mono(`v${__APP_VERSION__}`)],
            ['Hypervisor', backendLabel(caps)],
            ['Licence', 'Apache-2.0'],
            [
              'Source',
              <a href={REPO} target="_blank" rel="noreferrer" style={{ color: 'var(--nd-accent)' }}>
                {REPO.replace('https://', '')}
              </a>,
            ],
          ]}
        />
      </SettingsCard>
      {file && (
        <SettingsCard title="Set in config.yaml">
          <p className="nd-card-text">
            These change only by editing <span className="nd-mono">{file.config_path}</span> and restarting the daemon.
          </p>
          <FactList
            facts={[
              ['Listens on', mono(file.listen)],
              ...(file.libvirt_uri ? [['libvirt', mono(file.libvirt_uri)] as [string, React.ReactNode]] : []),
              ...(file.vz_network ? [['VM network', mono(file.vz_network)] as [string, React.ReactNode]] : []),
              ['Image cache', mono(file.image_cache_dir)],
              ['VM specs', mono(file.metadata_path)],
            ]}
          />
        </SettingsCard>
      )}
    </>
  );
}
