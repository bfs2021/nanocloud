import { patchSettings } from '../../api';
import type { Settings } from '../../types';
import { UpdatesCard } from '../UpdatesCard';
import { SettingsCard, SwitchRow, type Apply } from './common';

export function UpdatesSection({ settings, apply }: { settings: Settings; apply: Apply }) {
  return (
    <>
      {/* Keyed on the switch, so turning checks on asks again straight away. */}
      <UpdatesCard key={String(settings.update_check)} />
      <SettingsCard title="Checking">
        <SwitchRow
          title="Check for new releases"
          description="Asks GitHub for the latest release at most every 12 hours. It is the only request nano-cloud makes on its own; nothing about this host is sent."
          checked={settings.update_check}
          onChange={on => apply(() => patchSettings({ update_check: on }))}
        />
      </SettingsCard>
    </>
  );
}
