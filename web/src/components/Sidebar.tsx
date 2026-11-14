import type { View, StatsSample, Service } from '../types';
import { building, serviceStateMeta } from '../services';
import type { Theme } from '../theme';
import { useCapabilities } from '../capabilities';
import { BrandMark } from '../ui/BrandMark';
import { StatsChart } from './StatsChart';

interface SidebarProps {
  view: View;
  onNavigate: (v: View) => void;
  // The update badge's destination; Settings' top otherwise.
  onOpenUpdates?: () => void;
  onNewVM: () => void;
  cpuPercent: number;
  memUsedGb: number;
  memTotalGb: number;
  hostname: string;
  // Recent host samples for the trend line under the CPU meter. Optional —
  // an empty/missing series just renders the "gathering data" placeholder.
  cpuHistory?: StatsSample[];
  // Set when GET /system failed, so the footer doesn't present stale numbers.
  systemError?: string | null;
  // The colour theme and its switch, in the footer. Optional so callers that
  // do not offer a switch (tests) still render.
  theme?: Theme;
  onToggleTheme?: () => void;
  // The services, listed under the navigation for a quick jump; the open
  // one is marked.
  services?: Service[];
  currentService?: string | null;
  onOpenService?: (name: string) => void;
  // A newer release's tag, when there is one; it links to Settings.
  updateTag?: string;
}

// ServiceLinks lists the services with how each is doing: a glowing dot when
// ready, a pulsing one while it builds, and a word when it needs a look.
function ServiceLinks({ services, current, onOpen }: { services: Service[]; current?: string | null; onOpen: (name: string) => void }) {
  return (
    <nav aria-label="Your services" className="nd-sidebar-services" style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <div
        style={{
          padding: '0 12px 6px',
          fontSize: 11.5,
          fontWeight: 600,
          letterSpacing: '0.06em',
          textTransform: 'uppercase',
          color: 'var(--nd-muted)',
        }}
      >
        Your services
      </div>
      {services.map(s => {
        const meta = serviceStateMeta(s.state);
        const on = s.name === current;
        let word = building(s.state) || s.state === 'failed' || s.state === 'missing' ? meta.label : null;
        let dot = meta.dot;
        let wordColor = meta.color;
        // An alert firing about a ready service is worth a look from anywhere.
        if (!word && s.alerts?.length) {
          const critical = s.alerts.some(a => a.severity === 'critical');
          word = s.alerts.length === 1 ? 'Alert' : `${s.alerts.length} alerts`;
          dot = wordColor = critical ? 'var(--nd-danger)' : 'var(--nd-alert)';
        }
        return (
          <button
            key={s.name}
            onClick={() => onOpen(s.name)}
            aria-current={on ? 'page' : undefined}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 10,
              minHeight: 36,
              padding: '0 12px',
              borderRadius: 10,
              border: 'none',
              cursor: 'pointer',
              textAlign: 'left',
              background: on ? 'var(--nd-surface-2)' : 'transparent',
              color: on ? 'var(--nd-text)' : 'var(--nd-text-2)',
            }}
          >
            <span
              aria-hidden="true"
              style={{
                width: 7,
                height: 7,
                borderRadius: '50%',
                flexShrink: 0,
                background: dot,
                boxShadow: meta.glow && dot === meta.dot ? 'var(--nd-ready-glow)' : 'none',
                animation: meta.pulse ? 'ncpulse 1.6s ease-in-out infinite' : 'none',
              }}
            />
            <span
              style={{
                flex: 1,
                minWidth: 0,
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
                fontFamily: 'var(--nd-font-mono)',
                fontSize: 13,
              }}
            >
              {s.name}
            </span>
            {word && <span style={{ fontSize: 11.5, color: wordColor, flexShrink: 0 }}>{word.toLowerCase()}</span>}
          </button>
        );
      })}
    </nav>
  );
}

// short is the label on a phone, where the four links share one row.
function NavLink({
  active,
  onClick,
  icon,
  label,
  short,
}: {
  active: boolean;
  onClick: () => void;
  icon: React.ReactNode;
  label: string;
  short?: string;
}) {
  return (
    <button
      onClick={onClick}
      aria-current={active ? 'page' : undefined}
      aria-label={short ? label : undefined}
      className="nd-nav-link"
      style={{
        flexShrink: 0,
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        minHeight: 40,
        padding: '0 12px',
        borderRadius: 10,
        border: 'none',
        cursor: 'pointer',
        background: active ? 'var(--nd-surface-2)' : 'transparent',
        boxShadow: active ? 'inset 2px 0 0 var(--nd-accent)' : 'none',
        color: active ? 'var(--nd-text)' : 'var(--nd-muted)',
        fontSize: 14,
        fontWeight: active ? 550 : 400,
        textAlign: 'left',
        transition: 'background 0.15s, color 0.15s',
      }}
    >
      {icon}
      {short ? (
        <>
          <span className="nd-nav-full">{label}</span>
          <span className="nd-nav-short">{short}</span>
        </>
      ) : (
        label
      )}
    </button>
  );
}

function Meter({ label, value, pct }: { label: string; value: string; pct: number }) {
  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12, marginBottom: 5 }}>
        <span style={{ color: 'var(--nd-muted)' }}>{label}</span>
        <span style={{ fontFamily: 'var(--nd-font-mono)', color: 'var(--nd-text-2)' }}>{value}</span>
      </div>
      <div style={{ height: 4, background: 'var(--nd-track)', borderRadius: 2, overflow: 'hidden' }}>
        <div
          style={{
            width: `${Math.min(100, Math.max(0, pct))}%`,
            height: '100%',
            background: 'var(--nd-accent)',
            borderRadius: 2,
            transition: 'width 0.5s ease',
          }}
        />
      </div>
    </div>
  );
}

export function Sidebar({
  view,
  onNavigate,
  onOpenUpdates,
  onNewVM,
  cpuPercent,
  memUsedGb,
  memTotalGb,
  hostname,
  cpuHistory,
  systemError,
  theme,
  onToggleTheme,
  services,
  currentService,
  onOpenService,
  updateTag,
}: SidebarProps) {
  const caps = useCapabilities();
  return (
    <aside className="nd-sidebar" style={{ background: 'var(--nd-side)' }}>
      {/* Brand */}
      <div className="nd-sidebar-brand" style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '2px 6px' }}>
        <BrandMark size={34} label="Nano Cloud" />
        <div style={{ lineHeight: 1.15 }}>
          <div aria-hidden="true" style={{ fontWeight: 650, fontSize: 17, letterSpacing: '-0.02em' }}>
            nano<span style={{ color: 'var(--nd-accent)' }}>·</span>cloud
          </div>
          <div style={{ fontFamily: 'var(--nd-font-mono)', fontSize: 11, color: 'var(--nd-muted)' }}>v{__APP_VERSION__}</div>
          {updateTag && (
            <button
              type="button"
              onClick={onOpenUpdates ?? (() => onNavigate('settings'))}
              style={{
                marginTop: 3,
                padding: '1px 7px',
                borderRadius: 999,
                border: '1px solid var(--nd-accent-line)',
                background: 'var(--nd-accent-soft)',
                color: 'var(--nd-accent)',
                fontSize: 11,
                cursor: 'pointer',
              }}
            >
              {updateTag} available
            </button>
          )}
        </div>
      </div>

      <button
        onClick={onNewVM}
        style={{
          minHeight: 42,
          padding: '0 18px',
          background: 'var(--nd-accent)',
          color: 'var(--nd-on-accent)',
          border: 'none',
          borderRadius: 10,
          fontSize: 14,
          fontWeight: 600,
          cursor: 'pointer',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          gap: 7,
        }}
      >
        <span style={{ fontSize: 17, lineHeight: 1, marginTop: -1 }}>+</span>
        New VM
      </button>

      <nav aria-label="Main" className="nd-sidebar-nav">
        <NavLink
          active={view === 'services'}
          onClick={() => onNavigate('services')}
          label="Services"
          icon={
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <rect x="1.5" y="1.5" width="5.5" height="5.5" rx="1.5" stroke="currentColor" strokeWidth="1.4" />
              <rect x="9" y="1.5" width="5.5" height="5.5" rx="1.5" stroke="currentColor" strokeWidth="1.4" />
              <rect x="1.5" y="9" width="5.5" height="5.5" rx="1.5" stroke="currentColor" strokeWidth="1.4" />
              <path d="M11.75 9v5.5M9 11.75h5.5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
            </svg>
          }
        />
        <NavLink
          active={view === 'dashboard'}
          onClick={() => onNavigate('dashboard')}
          label="Virtual machines"
          short="VMs"
          icon={
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <rect x="1.5" y="2.5" width="13" height="8" rx="2" stroke="currentColor" strokeWidth="1.4" />
              <path d="M5 13.5h6M8 10.5v3" stroke="currentColor" strokeWidth="1.4" />
            </svg>
          }
        />
        <NavLink
          active={view === 'images'}
          onClick={() => onNavigate('images')}
          label="Images"
          icon={
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <ellipse cx="8" cy="3.4" rx="5.7" ry="2" stroke="currentColor" strokeWidth="1.4" />
              <path d="M2.3 3.4v4.4c0 1.1 2.55 2 5.7 2s5.7-.9 5.7-2V3.4" stroke="currentColor" strokeWidth="1.4" />
              <path d="M2.3 7.8v4.4c0 1.1 2.55 2 5.7 2s5.7-.9 5.7-2V7.8" stroke="currentColor" strokeWidth="1.4" />
            </svg>
          }
        />
        <NavLink
          active={view === 'settings'}
          onClick={() => onNavigate('settings')}
          label="Settings"
          icon={
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <circle cx="8" cy="8" r="2.2" stroke="currentColor" strokeWidth="1.4" />
              <path
                d="M8 1.5v1.3M8 13.2v1.3M1.5 8h1.3M13.2 8h1.3M3.4 3.4l.92.92M11.68 11.68l.92.92M3.4 12.6l.92-.92M11.68 4.32l.92-.92"
                stroke="currentColor"
                strokeWidth="1.4"
                strokeLinecap="round"
              />
            </svg>
          }
        />
      </nav>

      {services && services.length > 0 && onOpenService && (
        <ServiceLinks services={services} current={currentService} onOpen={onOpenService} />
      )}

      {/* Host card */}
      <div
        className="nd-sidebar-host"
        style={{
          marginTop: 'auto',
          display: 'flex',
          flexDirection: 'column',
          gap: 12,
          padding: 14,
          border: '1px solid var(--nd-line)',
          borderRadius: 12,
          background: 'var(--nd-surface)',
        }}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 8 }}>
          <span
            style={{
              fontFamily: 'var(--nd-font-mono)',
              fontSize: 12,
              color: 'var(--nd-text-2)',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
          >
            {hostname}
          </span>
          <span style={{ fontSize: 11.5, color: 'var(--nd-muted)', flexShrink: 0 }}>
            {caps.backend === 'vz' ? 'Apple Virtualization' : 'QEMU/KVM'}
          </span>
        </div>
        {systemError ? (
          <div
            role="alert"
            style={{
              background: 'var(--nd-danger-bg)',
              border: '1px solid var(--nd-danger-line)',
              borderRadius: 8,
              padding: '9px 11px',
              fontSize: 11.5,
              color: 'var(--nd-danger)',
              wordBreak: 'break-word',
            }}
          >
            Host stats unavailable: {systemError}
          </div>
        ) : (
          <>
            <Meter label="CPU" value={`${Math.round(cpuPercent)}%`} pct={cpuPercent} />
            <div className="nd-sidebar-trend">
              <StatsChart
                points={(cpuHistory ?? []).map(s => ({ at: s.at, value: s.cpu_percent }))}
                label="Host CPU"
                color="var(--nd-accent)"
                max={100}
                height={26}
              />
            </div>
            <Meter
              label="Memory"
              value={`${memUsedGb.toFixed(0)} / ${memTotalGb.toFixed(0)} GB`}
              pct={memTotalGb > 0 ? (memUsedGb / memTotalGb) * 100 : 0}
            />
          </>
        )}
        {onToggleTheme && (
          <button
            onClick={onToggleTheme}
            className="nd-sidebar-theme"
            style={{
              minHeight: 34,
              borderRadius: 8,
              border: '1px solid var(--nd-line)',
              background: 'transparent',
              color: 'var(--nd-muted)',
              fontSize: 12.5,
              cursor: 'pointer',
            }}
          >
            {theme === 'light' ? 'Switch to dark' : 'Switch to light'}
          </button>
        )}
      </div>
    </aside>
  );
}
