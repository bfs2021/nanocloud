import { getOS } from '../design';
import { OS_LOGOS } from '../osLogos';

interface OsChipProps {
  os?: string;
  size?: number;
}

// OsChip is a distribution's logo on its brand colour. An unknown one gets
// the Linux penguin on a neutral ground.
export function OsChip({ os, size = 36 }: OsChipProps) {
  const meta = getOS(os);
  const logo = OS_LOGOS[(os || '').toLowerCase()] ?? OS_LOGOS.linux;

  return (
    <div
      role="img"
      aria-label={meta.label}
      title={meta.label}
      style={{
        width: size,
        height: size,
        borderRadius: Math.round(size * 0.28),
        background: meta.color,
        // AlmaLinux's colour is black, which would vanish on the dark theme.
        boxShadow: 'inset 0 0 0 1px rgb(255 255 255 / 0.12)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        flexShrink: 0,
      }}
    >
      <svg viewBox="0 0 24 24" width={Math.round(size * 0.56)} height={Math.round(size * 0.56)} aria-hidden="true">
        <path d={logo} fill="#fff" />
      </svg>
    </div>
  );
}
