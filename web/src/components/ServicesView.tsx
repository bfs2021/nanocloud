import type { Service, ServiceRecipe } from '../types';
import { buildProgress, machineCount, machines, machineSummary, serviceRail } from '../services';
import { BootRail } from '../ui/BootRail';
import { ServiceBadge } from '../ui/ServiceBadge';
import { ServiceGlyph } from '../ui/ServiceGlyph';
import { surfaceCard } from '../design';

interface ServicesViewProps {
  catalog: ServiceRecipe[];
  services: Service[];
  // Set when GET /services failed: shown instead of an empty list, which
  // would claim there are none.
  error?: string | null;
  hostname: string;
  onLaunch: (recipe: ServiceRecipe) => void;
  onOpen: (name: string) => void;
}

const CATEGORY_LABEL: Record<string, string> = {
  containers: 'Containers',
  database: 'Database',
  storage: 'Storage',
  monitoring: 'Monitoring',
};

function smallestSize(r: ServiceRecipe) {
  return r.sizes.reduce((a, b) => (b.memory_mb < a.memory_mb ? b : a), r.sizes[0]);
}

function LaunchTile({ recipe, onLaunch }: { recipe: ServiceRecipe; onLaunch: () => void }) {
  const min = smallestSize(recipe);
  const parts = machines(recipe);
  const count = machineCount(parts);
  return (
    <div
      className="nd-lift"
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 12,
        padding: 18,
        ...surfaceCard,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <ServiceGlyph recipe={recipe.id} size={44} />
        <div style={{ minWidth: 0 }}>
          <h3 style={{ margin: 0, fontSize: 17, fontWeight: 650, letterSpacing: '-0.015em' }}>{recipe.name}</h3>
          <div style={{ fontSize: 12, color: 'var(--nd-muted)' }}>{CATEGORY_LABEL[recipe.category] ?? recipe.category}</div>
        </div>
      </div>
      <p style={{ margin: 0, fontSize: 13.5, lineHeight: 1.5, color: 'var(--nd-text-2)', flex: 1 }}>{recipe.description}</p>
      {count > 1 && <div style={{ fontSize: 12.5, color: 'var(--nd-text-2)' }}>{machineSummary(parts)}, by default</div>}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10 }}>
        <span style={{ fontFamily: 'var(--nd-font-mono)', fontSize: 11.5, color: 'var(--nd-muted)' }}>
          {count > 1 ? `${count} VMs · ` : ''}from {min.vcpus} vCPU · {min.memory_mb / 1024} GB{count > 1 ? ' each' : ''}
        </span>
        <button
          onClick={onLaunch}
          aria-label={`Launch ${recipe.name}`}
          style={{
            minHeight: 36,
            padding: '0 16px',
            borderRadius: 9,
            border: 'none',
            background: 'var(--nd-accent)',
            color: 'var(--nd-on-accent)',
            fontSize: 13.5,
            fontWeight: 600,
            cursor: 'pointer',
          }}
        >
          Launch
        </button>
      </div>
    </div>
  );
}

function ServiceCard({ svc, onOpen }: { svc: Service; onOpen: () => void }) {
  const rail = serviceRail(svc);
  const progress = buildProgress(svc);
  // The first output that is safe to show on a card: an address, never a key.
  const headline = svc.outputs.find(o => !o.secret);
  return (
    <button
      onClick={onOpen}
      aria-label={`Open ${svc.name}`}
      className="nd-lift"
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 12,
        padding: 16,
        textAlign: 'left',
        borderRadius: 14,
        border: '1px solid var(--nd-line)',
        background: 'var(--nd-surface)',
        color: 'var(--nd-text)',
        cursor: 'pointer',
        width: '100%',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, width: '100%' }}>
        <ServiceGlyph recipe={svc.recipe} size={36} />
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontSize: 15, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {svc.name}
          </div>
          <div style={{ fontSize: 12, color: 'var(--nd-muted)' }}>
            {svc.recipe_name} · {svc.size.label}
            {svc.vms.length > 1 ? ` · ${svc.vms.length} VMs` : ''}
          </div>
        </div>
        <ServiceBadge state={svc.state} />
      </div>
      {rail && svc.state !== 'ready' && (
        <div style={{ width: '100%' }}>
          <BootRail steps={rail} labels={false} />
        </div>
      )}
      {svc.state === 'failed' && svc.reason && (
        <div
          style={{
            fontSize: 12.5,
            color: 'var(--nd-danger)',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
            width: '100%',
          }}
        >
          {svc.reason.split('\n')[0]}
        </div>
      )}
      {progress && (
        <div role="status" style={{ fontFamily: 'var(--nd-font-mono)', fontSize: 12, color: 'var(--nd-warn)' }}>
          {progress}
        </div>
      )}
      {headline && (
        <div style={{ fontFamily: 'var(--nd-font-mono)', fontSize: 12.5, color: 'var(--nd-text-2)', overflowWrap: 'anywhere' }}>
          {headline.value}
        </div>
      )}
    </button>
  );
}

export function ServicesView({ catalog, services, error, hostname, onLaunch, onOpen }: ServicesViewProps) {
  return (
    <div style={{ padding: '28px clamp(16px, 3vw, 40px) 60px', maxWidth: 1320, width: '100%' }}>
      <h1 style={{ margin: '0 0 4px', fontSize: 28, fontWeight: 650, letterSpacing: '-0.025em' }}>Services</h1>
      <p style={{ margin: '0 0 24px', color: 'var(--nd-muted)', fontSize: 14 }}>
        Kubernetes, databases, storage and monitoring, each on its own VMs on{' '}
        <span style={{ fontFamily: 'var(--nd-font-mono)' }}>{hostname}</span>
      </p>

      {error && (
        <div
          role="alert"
          style={{
            marginBottom: 20,
            padding: '12px 14px',
            borderRadius: 10,
            border: '1px solid var(--nd-danger-line)',
            background: 'var(--nd-danger-bg)',
            color: 'var(--nd-danger)',
            fontSize: 13.5,
          }}
        >
          Couldn't load services: {error}
        </div>
      )}

      {services.length > 0 && (
        <section aria-labelledby="your-services" style={{ marginBottom: 32 }}>
          <h2
            id="your-services"
            style={{
              fontSize: 13,
              fontWeight: 600,
              color: 'var(--nd-muted)',
              textTransform: 'uppercase',
              letterSpacing: '0.06em',
              margin: '0 0 12px',
            }}
          >
            Your services
          </h2>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(min(100%, 320px), 1fr))', gap: 14 }}>
            {services.map(svc => (
              <ServiceCard key={svc.name} svc={svc} onOpen={() => onOpen(svc.name)} />
            ))}
          </div>
        </section>
      )}

      <section aria-labelledby="launch">
        <h2
          id="launch"
          style={{
            fontSize: 13,
            fontWeight: 600,
            color: 'var(--nd-muted)',
            textTransform: 'uppercase',
            letterSpacing: '0.06em',
            margin: '0 0 12px',
          }}
        >
          Launch a service
        </h2>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(min(100%, 300px), 1fr))', gap: 14 }}>
          {catalog.map(r => (
            <LaunchTile key={r.id} recipe={r} onLaunch={() => onLaunch(r)} />
          ))}
        </div>
        {catalog.length > 0 && (
          <p style={{ margin: '14px 0 0', fontSize: 12.5, color: 'var(--nd-muted)' }}>
            Each service runs on its own VM, which you can still open, stop and snapshot like any other.
          </p>
        )}
      </section>
    </div>
  );
}
