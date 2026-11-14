import { useCallback, useEffect, useRef, useState } from 'react';
import { getServiceMonitoring } from '../api';
import type { Monitoring, Service, ServiceAlert } from '../types';
import { surfaceCard } from '../design';

// The service console's views of a monitoring service (Prometheus +
// Grafana): what it watches, a chart of the last hour, its dashboards and
// alert rules. And the banner any service shows while an alert about it
// fires.

const MONITORING_POLL_MS = 15000;

// useMonitoring reads a monitoring service's view while enabled, again
// every few seconds.
export function useMonitoring(name: string, enabled: boolean) {
  const [mon, setMon] = useState<Monitoring | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      setMon(await getServiceMonitoring(name));
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [name]);

  useEffect(() => {
    if (!enabled) return;
    refresh();
    const id = setInterval(refresh, MONITORING_POLL_MS);
    return () => clearInterval(id);
  }, [enabled, refresh]);

  return { mon, error };
}

const card: React.CSSProperties = {
  ...surfaceCard,
  padding: 20,
};
const cardTitle: React.CSSProperties = { margin: '0 0 14px', fontSize: 15, fontWeight: 600 };
const mono: React.CSSProperties = { fontFamily: 'var(--nd-font-mono)' };
const muted: React.CSSProperties = { fontSize: 12.5, color: 'var(--nd-muted)', lineHeight: 1.5 };

// formatBytes says a size the way people read it: 2.1 GB.
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 10 ? Math.round(v) : Math.round(v * 10) / 10} ${units[i]}`;
}

// retentionText says how long Prometheus keeps its data, from its own
// notation ("15d", "720h").
export function retentionText(r?: string): string | null {
  const m = /^(\d+)([smhdwy])$/.exec(r ?? '');
  if (!m) return null;
  let n = Number(m[1]);
  let unit = m[2];
  if (unit === 'h' && n % 24 === 0) {
    n /= 24;
    unit = 'd';
  }
  const word: Record<string, string> = { s: 'second', m: 'minute', h: 'hour', d: 'day', w: 'week', y: 'year' };
  return `${n} ${word[unit]}${n === 1 ? '' : 's'} kept`;
}

const JOB_LABEL: Record<string, string> = {
  node: 'node',
  postgres: 'PostgreSQL',
  mysql: 'MySQL',
  'kube-state-metrics': 'Kubernetes objects',
  prometheus: 'Prometheus',
};

// StatusMark pairs a status colour with its word, never the colour alone.
function StatusMark({ state }: { state: string }) {
  const meta: Record<string, { text: string; color: string }> = {
    ok: { text: 'OK', color: 'var(--nd-ready)' },
    pending: { text: 'Pending', color: 'var(--nd-warn)' },
    firing: { text: 'Firing', color: 'var(--nd-danger)' },
  };
  const m = meta[state] ?? { text: state, color: 'var(--nd-muted)' };
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 12.5, fontWeight: 600, color: m.color, flexShrink: 0 }}>
      <span aria-hidden="true" style={{ width: 7, height: 7, borderRadius: '50%', background: m.color }} />
      {m.text}
    </span>
  );
}

// AlertBanner lists the alerts firing about a service.
export function AlertBanner({ alerts }: { alerts: ServiceAlert[] }) {
  if (alerts.length === 0) return null;
  const critical = alerts.some(a => a.severity === 'critical');
  const color = critical ? 'var(--nd-danger)' : 'var(--nd-alert)';
  return (
    <section
      aria-label="Alerts firing"
      style={{
        ...card,
        marginBottom: 16,
        boxShadow: 'none',
        borderColor: critical ? 'var(--nd-danger-line)' : 'var(--nd-line)',
        background: critical ? 'var(--nd-danger-bg)' : 'var(--nd-surface)',
        display: 'flex',
        flexDirection: 'column',
        gap: 10,
      }}
    >
      <h2 style={{ margin: 0, fontSize: 15, fontWeight: 600, display: 'flex', alignItems: 'center', gap: 8 }}>
        <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
          <path d="M8 1.5 15 14H1L8 1.5Z" stroke={color} strokeWidth="1.5" strokeLinejoin="round" />
          <path d="M8 6v3.5M8 11.5v.5" stroke={color} strokeWidth="1.5" strokeLinecap="round" />
        </svg>
        {alerts.length === 1 ? '1 alert firing' : `${alerts.length} alerts firing`}
      </h2>
      <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 6 }}>
        {alerts.map(a => (
          <li key={`${a.name}:${a.vm}:${a.summary}`} style={{ fontSize: 13.5, lineHeight: 1.5 }}>
            <b style={{ fontWeight: 600 }}>{a.title}</b>
            {a.summary ? `: ${a.summary}` : ''}
            {a.since_unix ? (
              <span style={{ color: 'var(--nd-muted)' }}>
                {' '}
                · since {new Date(a.since_unix * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
              </span>
            ) : null}
          </li>
        ))}
      </ul>
      <span style={muted}>From the monitoring service watching it. It clears by itself once the cause is gone.</span>
    </section>
  );
}

type Series = { name: string; points: [number, number][] };

// niceMax rounds a chart's top up to a number its ticks read well at.
export function niceMax(v: number, percent: boolean): number {
  if (percent) {
    for (const m of [10, 25, 50, 75, 100]) if (v <= m) return m;
    return Math.ceil(v / 100) * 100;
  }
  if (v <= 0) return 1;
  const p = 10 ** Math.floor(Math.log10(v));
  for (const f of [1, 2, 5, 10]) if (v <= f * p) return f * p;
  return 10 * p;
}

function useWidth(fallback: number) {
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(fallback);
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver(entries => {
      const w = Math.round(entries[0].contentRect.width);
      if (w > 0) setWidth(w);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return { ref, width };
}

const MAX_SERIES = 8;

// LineChart draws series over time on one axis: 2px lines, a crosshair
// that snaps to the nearest minute with every series' value, a legend, and
// the same numbers as a table.
export function LineChart({ title, series, unit = '%' }: { title: string; series: Series[]; unit?: string }) {
  const { ref, width } = useWidth(720);
  const [at, setAt] = useState<number | null>(null);
  const shown = series.filter(s => s.points.length > 0).slice(0, MAX_SERIES);
  const hidden = series.filter(s => s.points.length > 0).length - shown.length;
  const H = 220;
  const pad = { l: 44, r: 14, t: 12, b: 26 };
  const times = [...new Set(shown.flatMap(s => s.points.map(p => p[0])))].sort((a, b) => a - b);
  const percent = unit === '%';

  if (times.length < 2) {
    return (
      <div ref={ref}>
        <p style={{ ...muted, margin: 0 }}>Not enough data yet: the chart fills in over the first minutes.</p>
      </div>
    );
  }

  const t0 = times[0];
  const t1 = times[times.length - 1];
  const yMax = niceMax(Math.max(...shown.flatMap(s => s.points.map(p => p[1]))), percent);
  const plotW = Math.max(120, width - pad.l - pad.r);
  const plotH = H - pad.t - pad.b;
  const x = (t: number) => pad.l + ((t - t0) / (t1 - t0)) * plotW;
  const y = (v: number) => pad.t + plotH - (Math.min(v, yMax) / yMax) * plotH;
  const ticks = [0, yMax / 2, yMax];
  const fmt = (v: number) => `${Math.round(v * 10) / 10}${unit}`;
  const color = (i: number) => `var(--nd-series-${i + 1})`;
  const valueAt = (s: Series, t: number) => s.points.find(p => p[0] === t)?.[1];

  function pick(clientX: number, rect: DOMRect) {
    const px = clientX - rect.left;
    const t = t0 + ((px - pad.l) / plotW) * (t1 - t0);
    let best = 0;
    for (let i = 1; i < times.length; i++) if (Math.abs(times[i] - t) < Math.abs(times[best] - t)) best = i;
    setAt(best);
  }

  const atT = at !== null ? times[at] : null;
  const minutesAgo = (t: number) => Math.round((t1 - t) / 60);
  const tipLeft = atT !== null ? Math.min(Math.max(x(atT) + 12, 0), width - 190) : 0;
  const summary = shown.map(s => {
    const vals = s.points.map(p => p[1]);
    return {
      name: s.name,
      now: vals[vals.length - 1],
      avg: vals.reduce((a, b) => a + b, 0) / vals.length,
      peak: Math.max(...vals),
    };
  });

  return (
    <div ref={ref} style={{ position: 'relative' }}>
      <svg
        role="img"
        aria-label={`${title}. ${summary.map(s => `${s.name} ${fmt(s.now)} now`).join(', ')}. Use the arrow keys to read each minute.`}
        width={width}
        height={H}
        tabIndex={0}
        onPointerMove={e => pick(e.clientX, e.currentTarget.getBoundingClientRect())}
        onPointerLeave={() => setAt(null)}
        onBlur={() => setAt(null)}
        onKeyDown={e => {
          if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
            e.preventDefault();
            const step = e.key === 'ArrowLeft' ? -1 : 1;
            setAt(a => Math.min(times.length - 1, Math.max(0, (a ?? times.length - 1) + (a === null ? 0 : step))));
          } else if (e.key === 'Escape') {
            setAt(null);
          }
        }}
        style={{ display: 'block', maxWidth: '100%', outline: 'none', touchAction: 'pan-y' }}
      >
        {ticks.map(t => (
          <g key={t}>
            <line
              x1={pad.l}
              x2={pad.l + plotW}
              y1={y(t)}
              y2={y(t)}
              stroke={t === 0 ? 'var(--nd-line)' : 'var(--nd-line-soft)'}
              strokeWidth={1}
            />
            <text
              x={pad.l - 8}
              y={y(t) + 4}
              textAnchor="end"
              fontSize={11}
              fill="var(--nd-muted)"
              style={{ fontVariantNumeric: 'tabular-nums' }}
            >
              {fmt(t)}
            </text>
          </g>
        ))}
        {[t0, t0 + (t1 - t0) / 2, t1].map((t, i) => (
          <text key={t} x={x(t)} y={H - 6} textAnchor={i === 0 ? 'start' : i === 2 ? 'end' : 'middle'} fontSize={11} fill="var(--nd-muted)">
            {i === 2 ? 'now' : `${minutesAgo(t)} min ago`}
          </text>
        ))}
        {shown.map((s, i) => (
          <polyline
            key={s.name}
            points={s.points.map(p => `${x(p[0])},${y(p[1])}`).join(' ')}
            fill="none"
            stroke={color(i)}
            strokeWidth={2}
            strokeLinejoin="round"
            strokeLinecap="round"
          />
        ))}
        {shown.map((s, i) => {
          const last = s.points[s.points.length - 1];
          return <circle key={s.name} cx={x(last[0])} cy={y(last[1])} r={4} fill={color(i)} stroke="var(--nd-surface)" strokeWidth={2} />;
        })}
        {atT !== null && (
          <g>
            <line x1={x(atT)} x2={x(atT)} y1={pad.t} y2={pad.t + plotH} stroke="var(--nd-muted)" strokeWidth={1} />
            {shown.map((s, i) => {
              const v = valueAt(s, atT);
              return v === undefined ? null : (
                <circle key={s.name} cx={x(atT)} cy={y(v)} r={4} fill={color(i)} stroke="var(--nd-surface)" strokeWidth={2} />
              );
            })}
          </g>
        )}
      </svg>

      {atT !== null && (
        <div
          role="status"
          style={{
            position: 'absolute',
            top: 8,
            left: tipLeft,
            width: 180,
            padding: '8px 10px',
            borderRadius: 9,
            border: '1px solid var(--nd-line)',
            background: 'var(--nd-bg)',
            boxShadow: 'var(--nd-shadow-lg)',
            pointerEvents: 'none',
            fontSize: 12,
          }}
        >
          <div style={{ color: 'var(--nd-muted)', marginBottom: 4 }}>{minutesAgo(atT) === 0 ? 'now' : `${minutesAgo(atT)} min ago`}</div>
          {shown.map((s, i) => {
            const v = valueAt(s, atT);
            return (
              <div key={s.name} style={{ display: 'flex', alignItems: 'center', gap: 8, lineHeight: 1.6 }}>
                <span aria-hidden="true" style={{ width: 12, height: 2, borderRadius: 1, background: color(i), flexShrink: 0 }} />
                <b style={{ fontWeight: 650, minWidth: 44, fontVariantNumeric: 'tabular-nums' }}>{v === undefined ? '—' : fmt(v)}</b>
                <span style={{ color: 'var(--nd-text-2)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {s.name}
                </span>
              </div>
            );
          })}
        </div>
      )}

      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: '6px 16px', marginTop: 10 }}>
        {shown.length > 1 &&
          shown.map((s, i) => (
            <span key={s.name} style={{ display: 'inline-flex', alignItems: 'center', gap: 7, fontSize: 12.5, color: 'var(--nd-text-2)' }}>
              <span aria-hidden="true" style={{ width: 14, height: 2, borderRadius: 1, background: color(i) }} />
              {s.name}
            </span>
          ))}
        {hidden > 0 && <span style={{ fontSize: 12.5, color: 'var(--nd-muted)' }}>and {hidden} more in Grafana</span>}
      </div>

      <details style={{ marginTop: 10 }}>
        <summary style={{ fontSize: 12.5, color: 'var(--nd-text-2)', cursor: 'pointer' }}>As a table</summary>
        <table style={{ marginTop: 8, borderCollapse: 'collapse', fontSize: 12.5, fontVariantNumeric: 'tabular-nums' }}>
          <thead>
            <tr>
              {['Service', 'Now', 'Average', 'Peak'].map(h => (
                <th
                  key={h}
                  style={{
                    textAlign: h === 'Service' ? 'left' : 'right',
                    padding: '4px 12px 4px 0',
                    color: 'var(--nd-muted)',
                    fontWeight: 600,
                  }}
                >
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {summary.map(s => (
              <tr key={s.name}>
                <td style={{ ...mono, padding: '4px 12px 4px 0' }}>{s.name}</td>
                <td style={{ textAlign: 'right', padding: '4px 12px 4px 0' }}>{fmt(s.now)}</td>
                <td style={{ textAlign: 'right', padding: '4px 12px 4px 0' }}>{fmt(s.avg)}</td>
                <td style={{ textAlign: 'right', padding: '4px 12px 4px 0' }}>{fmt(s.peak)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </details>
    </div>
  );
}

interface MonitoringOverviewProps {
  svc: Service;
  mon: Monitoring | null;
  error: string | null;
}

// MonitoringOverview is a monitoring service's console: the numbers, the
// last hour, its dashboards, what it watches and its alert rules.
export function MonitoringOverview({ svc, mon, error }: MonitoringOverviewProps) {
  if (!mon) {
    return (
      <section style={{ ...card, gridColumn: '1 / -1' }} aria-label="Monitoring">
        <p style={{ margin: 0, fontSize: 13.5, color: 'var(--nd-muted)' }}>{error ?? 'Reading Prometheus…'}</p>
      </section>
    );
  }

  const grafana = svc.endpoints.find(e => e.id === 'grafana' && e.address);
  const dashURL = (uid: string, v?: string, value?: string) =>
    grafana ? `http://${grafana.address}/d/${uid}${v && value ? `?var-${v}=${encodeURIComponent(value)}` : ''}` : undefined;
  const dashboardFor = (recipe?: string) => mon.dashboards.find(d => recipe && d.recipes?.includes(recipe));
  const setup = mon.setup ?? [];
  const kept = retentionText(mon.retention);
  const tiles: [string, string, string][] = [
    ['Targets up', `${mon.targets_up} / ${mon.targets_total}`, 'scraped every 15 s'],
    ['Watching', `${mon.vms} ${mon.vms === 1 ? 'VM' : 'VMs'}`, `in ${mon.services} ${mon.services === 1 ? 'service' : 'services'}`],
    ['Alerts firing', String(mon.firing), `${mon.rules.length} rules on`],
    ['Stored', formatBytes(mon.stored_bytes), kept ?? 'on its disk'],
  ];
  const linkStyle: React.CSSProperties = { fontSize: 13, textDecoration: 'none', color: 'var(--nd-accent)', whiteSpace: 'nowrap' };

  return (
    <>
      {error && (
        <p role="status" style={{ gridColumn: '1 / -1', margin: 0, fontSize: 12.5, color: 'var(--nd-warn)' }}>
          Could not read Prometheus just now ({error}); showing what it said last.
        </p>
      )}
      <section style={{ ...card, gridColumn: '1 / -1' }} aria-labelledby="mon-numbers">
        <h2 id="mon-numbers" style={cardTitle}>
          Monitoring
        </h2>
        <dl style={{ margin: 0, display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: 16 }}>
          {tiles.map(([k, v, sub]) => (
            <div key={k}>
              <dt style={{ fontSize: 12.5, color: 'var(--nd-muted)' }}>{k}</dt>
              <dd style={{ margin: '2px 0 0' }}>
                <span style={{ display: 'block', fontSize: 24, fontWeight: 650, letterSpacing: '-0.02em' }}>{v}</span>
                <span style={{ fontSize: 12, color: 'var(--nd-muted)' }}>{sub}</span>
              </dd>
            </div>
          ))}
        </dl>
        {!mon.provisioned && (
          <p role="status" style={{ ...muted, margin: '14px 0 0', color: 'var(--nd-warn)' }}>
            Setting up the dashboards and alert rules on {svc.name}. This takes a minute.
          </p>
        )}
      </section>

      <section style={{ ...card, gridColumn: '1 / -1' }} aria-labelledby="mon-cpu">
        <h2 id="mon-cpu" style={cardTitle}>
          CPU by service · last hour
        </h2>
        <LineChart title="CPU by service over the last hour" series={mon.cpu} />
      </section>

      <section style={card} aria-labelledby="mon-dashboards">
        <h2 id="mon-dashboards" style={cardTitle}>
          Dashboards, ready on first login
        </h2>
        <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 4 }}>
          {mon.dashboards.map(d => {
            const url = dashURL(d.uid);
            return (
              <li
                key={d.uid}
                style={{
                  display: 'flex',
                  alignItems: 'flex-start',
                  gap: 10,
                  padding: '8px 0',
                  borderBottom: '1px solid var(--nd-line-soft)',
                }}
              >
                <span style={{ flex: 1, minWidth: 0 }}>
                  <span style={{ fontSize: 13.5, fontWeight: 600 }}>{d.title}</span>
                  {d.home && (
                    <span
                      style={{
                        marginLeft: 8,
                        padding: '1px 6px',
                        borderRadius: 5,
                        background: 'var(--nd-accent-soft)',
                        fontSize: 10.5,
                        fontWeight: 650,
                        letterSpacing: '0.06em',
                      }}
                    >
                      HOME
                    </span>
                  )}
                  <span style={{ display: 'block', fontSize: 12.5, color: 'var(--nd-muted)', lineHeight: 1.45 }}>{d.about}</span>
                </span>
                {url && (
                  <a href={url} target="_blank" rel="noreferrer" style={linkStyle} aria-label={`Open ${d.title} in Grafana`}>
                    Open ↗
                  </a>
                )}
              </li>
            );
          })}
        </ul>
        {!grafana && <p style={{ ...muted, margin: '10px 0 0' }}>Grafana's address appears under Access once it is forwarded.</p>}
      </section>

      <section style={card} aria-labelledby="mon-watching">
        <h2 id="mon-watching" style={cardTitle}>
          What it watches
        </h2>
        {mon.watching.length === 0 ? (
          <p style={{ ...muted, margin: 0 }}>Nothing yet: nano-cloud is setting up the services' metrics.</p>
        ) : (
          <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 4 }}>
            {mon.watching.map(w => {
              const d = dashboardFor(w.recipe);
              const url = d ? dashURL(d.uid, d.var, w.service) : undefined;
              const healthy = w.up === w.total;
              return (
                <li
                  key={w.service}
                  style={{
                    display: 'flex',
                    flexWrap: 'wrap',
                    alignItems: 'center',
                    gap: '4px 10px',
                    padding: '8px 0',
                    borderBottom: '1px solid var(--nd-line-soft)',
                  }}
                >
                  <span style={{ flex: '1 1 200px', minWidth: 0 }}>
                    <span style={{ ...mono, fontSize: 13 }}>{w.service}</span>
                    <span style={{ display: 'block', fontSize: 12.5, color: 'var(--nd-muted)' }}>
                      {w.vms} {w.vms === 1 ? 'VM' : 'VMs'} · {w.jobs.map(j => JOB_LABEL[j] ?? j).join(', ')}
                    </span>
                  </span>
                  <span
                    style={{
                      display: 'inline-flex',
                      alignItems: 'center',
                      gap: 6,
                      fontSize: 12.5,
                      color: healthy ? 'var(--nd-text-2)' : 'var(--nd-danger)',
                    }}
                  >
                    <span
                      aria-hidden="true"
                      style={{ width: 7, height: 7, borderRadius: '50%', background: healthy ? 'var(--nd-ready)' : 'var(--nd-danger)' }}
                    />
                    {w.up} of {w.total} up
                  </span>
                  {url && (
                    <a href={url} target="_blank" rel="noreferrer" style={linkStyle} aria-label={`${d!.title} dashboard for ${w.service}`}>
                      {d!.title} ↗
                    </a>
                  )}
                </li>
              );
            })}
          </ul>
        )}
        {setup.length > 0 && (
          <ul
            aria-label="Being set up"
            style={{ listStyle: 'none', margin: '10px 0 0', padding: 0, display: 'flex', flexDirection: 'column', gap: 6 }}
          >
            {setup.map(s => (
              <li
                key={`${s.vm}:${s.what}`}
                style={{ fontSize: 12.5, lineHeight: 1.5, color: s.problem ? 'var(--nd-danger)' : 'var(--nd-text-2)' }}
              >
                {s.problem
                  ? `Could not set up ${s.what} on ${s.vm}: ${s.problem}. Trying again in a while.`
                  : s.running
                    ? `Setting up ${s.what} on ${s.vm}…`
                    : `${s.what} on ${s.vm}: next.`}
              </li>
            ))}
          </ul>
        )}
        <p style={{ ...muted, margin: '12px 0 0' }}>
          New services appear here by themselves: nano-cloud tells Prometheus about every VM it runs.
        </p>
      </section>

      <section style={{ ...card, gridColumn: '1 / -1' }} aria-labelledby="mon-rules">
        <h2 id="mon-rules" style={cardTitle}>
          Alert rules
        </h2>
        <ul
          style={{
            listStyle: 'none',
            margin: 0,
            padding: 0,
            display: 'grid',
            gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 300px), 1fr))',
            gap: '4px 24px',
          }}
        >
          {mon.rules.map(r => (
            <li
              key={r.name}
              style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '8px 0', borderBottom: '1px solid var(--nd-line-soft)' }}
            >
              <span style={{ flex: 1, minWidth: 0, fontSize: 13.5 }}>
                <b style={{ fontWeight: 600 }}>{r.name}</b>
                {r.when && <span style={{ color: 'var(--nd-muted)' }}> · {r.when}</span>}
              </span>
              <StatusMark state={r.state} />
            </li>
          ))}
        </ul>
        <p style={{ ...muted, margin: '12px 0 0' }}>A firing alert shows in nano-cloud's activity feed and on the service it is about.</p>
      </section>
    </>
  );
}
