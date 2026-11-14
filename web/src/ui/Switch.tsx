interface SwitchProps {
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
  // The switch is named by the visible text beside it (aria-labelledby) or,
  // failing that, by label.
  'aria-labelledby'?: string;
  'aria-describedby'?: string;
  label?: string;
}

// Switch is an on/off setting that applies as soon as it is flipped, so it
// needs no Save button.
export function Switch({ checked, onChange, disabled, label, ...aria }: SwitchProps) {
  return (
    <button
      type="button"
      role="switch"
      className="nd-switch"
      aria-checked={checked}
      aria-label={aria['aria-labelledby'] ? undefined : label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      {...aria}
    />
  );
}
