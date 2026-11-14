import { patchSettings } from '../../api';
import type { Settings } from '../../types';
import { SettingsCard, SwitchRow, type Apply } from './common';

export function SelfHealingSection({ settings, apply }: { settings: Settings; apply: Apply }) {
  return (
    <SettingsCard title="Recovery">
      <SwitchRow
        title="Keep VMs the way you left them"
        description="Every 10 seconds nano-cloud compares each VM with what you last asked for: one that should be running but is not gets started again, with back-off, and one whose definition is gone is rebuilt from its disk. A VM you stopped stays stopped."
        checked={settings.self_healing}
        onChange={on => apply(() => patchSettings({ self_healing: on }))}
      />
      {!settings.self_healing && (
        <p className="nd-card-text">
          While this is off, a VM that crashes or does not come back after the host restarts stays down until you start it.
        </p>
      )}
    </SettingsCard>
  );
}
