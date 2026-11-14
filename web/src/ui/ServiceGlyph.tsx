import { serviceColor } from '../services';

// A service's chip: its glyph, white on its colour, the way an OS chip marks
// a VM. Drawn inline so it follows currentColor and needs no asset.

function Glyph({ recipe }: { recipe: string }) {
  const common = {
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 1.6,
    strokeLinecap: 'round' as const,
    strokeLinejoin: 'round' as const,
  };
  switch (recipe) {
    case 'kubernetes':
    case 'kubernetes_cluster':
      // A helm wheel: a heptagon around a hub, seven spokes.
      return (
        <svg viewBox="0 0 24 24" width="62%" height="62%" aria-hidden="true">
          <path d="M12 2.6 19.3 6.1 21.1 14 16.1 20.3H7.9L2.9 14l1.8-7.9Z" {...common} />
          <circle cx="12" cy="12.2" r="2.2" {...common} />
          {[0, 1, 2, 3, 4, 5, 6].map(i => {
            const a = (i / 7) * Math.PI * 2 - Math.PI / 2;
            return (
              <path
                key={i}
                d={`M${12 + Math.cos(a) * 2.6} ${12.2 + Math.sin(a) * 2.6}L${12 + Math.cos(a) * 6.4} ${12.2 + Math.sin(a) * 6.4}`}
                {...common}
              />
            );
          })}
        </svg>
      );
    case 'postgres':
    case 'mysql':
      // A database: three stacked discs.
      return (
        <svg viewBox="0 0 24 24" width="62%" height="62%" aria-hidden="true">
          <ellipse cx="12" cy="5.5" rx="7.5" ry="2.8" {...common} />
          <path d="M4.5 5.5v6.5c0 1.5 3.4 2.8 7.5 2.8s7.5-1.3 7.5-2.8V5.5" {...common} />
          <path d="M4.5 12v6.5c0 1.5 3.4 2.8 7.5 2.8s7.5-1.3 7.5-2.8V12" {...common} />
        </svg>
      );
    case 's3':
      // A bucket.
      return (
        <svg viewBox="0 0 24 24" width="62%" height="62%" aria-hidden="true">
          <ellipse cx="12" cy="6" rx="8" ry="2.6" {...common} />
          <path d="M4 6l2.2 13c.3 1.4 2.8 2.4 5.8 2.4s5.5-1 5.8-2.4L20 6" {...common} />
          <path d="M5.2 11.5c1.4.9 4 1.5 6.8 1.5s5.4-.6 6.8-1.5" {...common} />
        </svg>
      );
    case 'observability':
      // A chart: axes and a rising line.
      return (
        <svg viewBox="0 0 24 24" width="62%" height="62%" aria-hidden="true">
          <path d="M3.5 3.5v17h17" {...common} />
          <path d="M7 15.5l3.5-4 3 2.5 5.5-7" {...common} />
          <circle cx="19" cy="7" r="1.2" {...common} />
        </svg>
      );
    default:
      return (
        <svg viewBox="0 0 24 24" width="62%" height="62%" aria-hidden="true">
          <path d="M12 2.5 20.5 7v10L12 21.5 3.5 17V7Z M3.5 7 12 11.5 20.5 7 M12 11.5v10" {...common} />
        </svg>
      );
  }
}

export function ServiceGlyph({ recipe, size = 40, label }: { recipe: string; size?: number; label?: string }) {
  return (
    <span
      role={label ? 'img' : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      title={label}
      style={{
        width: size,
        height: size,
        borderRadius: Math.round(size * 0.28),
        background: serviceColor(recipe),
        color: '#fff',
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        flexShrink: 0,
      }}
    >
      <Glyph recipe={recipe} />
    </span>
  );
}
