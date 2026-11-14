import { useId } from 'react';
import { useThemeChoice, type ThemeChoice } from '../../theme';
import { SettingsCard } from './common';

const CHOICES: [ThemeChoice, string][] = [
  ['dark', 'Dark'],
  ['light', 'Light'],
  ['system', 'Match system'],
];

export function GeneralSection() {
  const [choice, choose] = useThemeChoice();
  const id = useId();
  return (
    <SettingsCard title="Appearance">
      <div className="nd-setting-row" style={{ alignItems: 'center', flexWrap: 'wrap' }}>
        <div>
          <div id={id} style={{ fontWeight: 600, fontSize: 'var(--nd-text-md)' }}>
            Theme
          </div>
          <div className="nd-card-text">Remembered by this browser only.</div>
        </div>
        <div role="radiogroup" aria-labelledby={id} className="nd-segmented">
          {CHOICES.map(([value, label]) => (
            <label key={value}>
              <input type="radio" name={`${id}-theme`} value={value} checked={choice === value} onChange={() => choose(value)} />
              {label}
            </label>
          ))}
        </div>
      </div>
    </SettingsCard>
  );
}
