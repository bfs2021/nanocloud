import { useId } from 'react';

// The Nanodot mark: a cloud drawn from dots, in the aurora gradient. The same
// dots make the app icon (web/public/icon.svg).
const DOTS: [number, number, number][] = [
  [24, 4, 1.85],
  [19.5, 8, 1.93],
  [24.5, 8.5, 2.44],
  [29.5, 9, 1.93],
  [11, 12.5, 1.76],
  [15.5, 12.5, 2.1],
  [20.5, 13, 2.35],
  [26, 13.5, 2.52],
  [31.5, 13.5, 2.1],
  [6, 18, 1.76],
  [10.5, 18, 2.18],
  [15.5, 18.3, 2.35],
  [21, 18.5, 2.52],
  [26.5, 18.5, 2.44],
  [32, 18.3, 2.27],
  [36, 18, 1.68],
  [8, 23.2, 1.6],
  [12.5, 23.4, 1.76],
  [17, 23.5, 1.85],
  [21.5, 23.5, 1.85],
  [26, 23.5, 1.85],
  [30.5, 23.4, 1.76],
  [34.5, 23.2, 1.51],
];

export function BrandMark({ size = 30, label }: { size?: number; label?: string }) {
  const id = useId();
  return (
    <svg
      width={size}
      height={(size * 24) / 36}
      viewBox="3 1.5 36 24"
      {...(label ? { role: 'img', 'aria-label': label } : { 'aria-hidden': true })}
    >
      <defs>
        <linearGradient id={id} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#22d3ee" />
          <stop offset="1" stopColor="#a78bfa" />
        </linearGradient>
      </defs>
      <g fill={`url(#${id})`}>
        {DOTS.map(([cx, cy, r]) => (
          <circle key={`${cx}-${cy}`} cx={cx} cy={cy} r={r} />
        ))}
      </g>
    </svg>
  );
}
