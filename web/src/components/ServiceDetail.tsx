import { useState } from 'react';
import type { NanoEvent, Service, ServiceEndpoint, ServiceMember } from '../types';
import { serviceFileURL } from '../api';
import { useCapabilities } from '../capabilities';
import { building, buildProgress, memberDoing, memberOutcome, serviceRail, sizeSummary, type MemberTone } from '../services';
import { BootRail } from '../ui/BootRail';
import { CopyField } from '../ui/CopyField';
import { ServiceBadge } from '../ui/ServiceBadge';
import { ServiceGlyph } from '../ui/ServiceGlyph';
import { ClusterNodes, ClusterSummary, ClusterWorkloads, useCluster } from './ClusterViews';
import { AlertBanner, MonitoringOverview, useMonitoring } from './MonitoringViews';
import { RestartVM } from './RestartVM';
import { ServiceLogView } from './ServiceLogView';
import { surfaceCard } from '../design';

interface ServiceDetailProps {
  svc: Service;
  // The activity feed; the service's own entries are shown.
  events?: NanoEvent[];
  busy: boolean;
  // The host nano-cloud runs on, and whether this browser runs there too.
  hostname: string;
  localViewer: boolean;
  onShare: (shared: boolean) => void;
  onRetry: () => void;
  onBack: () => void;
  onStart: () => void;
  onStop: () => void;
  onDelete: () => void;
  onOpenVM: (name: string) => void;
  // A Kubernetes service's nodes and published NodePorts.
  onAddWorker?: () => void;
  onRemoveWorker?: (vm: string) => void;
  onPublish?: (nodePort: number, label: string, scheme?: string) => void;
  onUnpublish?: (nodePort: number) => void;
}

const card: React.CSSProperties = {
  ...surfaceCard,
  padding: 20,
};

const cardTitle: React.CSSProperties = { margin: '0 0 14px', fontSize: 15, fontWeight: 600 };

function actionButton(kind: 'plain' | 'danger'): React.CSSProperties {
  return {
    minHeight: 38,
    padding: '0 16px',
    borderRadius: 9,
    fontSize: 13.5,
    fontWeight: 500,
    cursor: 'pointer',
    border: `1px solid ${kind === 'danger' ? 'var(--nd-danger-line)' : 'var(--nd-line)'}`,
    background: kind === 'danger' ? 'var(--nd-danger-bg)' : 'var(--nd-surface)',
    color: kind === 'danger' ? 'var(--nd-danger)' : 'var(--nd-text)',
  };
}

// Addresses lists a service's endpoints as nano-cloud forwards them, and
// whether other machines may connect.
function Addresses({
  svc,
  busy,
  hostname,
  localViewer,
  onMac,
  onShare,
}: {
  svc: Service;
  busy: boolean;
  hostname: string;
  localViewer: boolean;
  onMac: boolean;
  onShare: (shared: boolean) => void;
}) {
  const canOpen = (e: ServiceEndpoint) => e.scheme === 'http' && !!e.address && svc.state === 'ready';
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      {svc.endpoints.map(e => (
        <div key={e.id} style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 10, minHeight: 32 }}>
            <span style={{ fontSize: 13, color: 'var(--nd-text-2)', minWidth: 110 }}>{e.label}</span>
            {e.address ? (
              <span style={{ fontFamily: 'var(--nd-font-mono)', fontSize: 13 }}>{e.address}</span>
            ) : (
              <span style={{ fontSize: 12.5, color: e.problem ? 'var(--nd-danger)' : 'var(--nd-muted)' }}>
                {e.problem ?? 'Not forwarded yet'}
              </span>
            )}
            {canOpen(e) && (
              <a href={`http://${e.address}`} target="_blank" rel="noreferrer" style={{ fontSize: 13, textDecoration: 'none' }}>
                Open ↗
              </a>
            )}
          </div>
          {e.direct && (
            <span style={{ fontSize: 12, color: 'var(--nd-muted)' }}>
              On the VM network: <span style={{ fontFamily: 'var(--nd-font-mono)' }}>{e.direct}</span>
            </span>
          )}
        </div>
      ))}

      <div
        style={{
          display: 'flex',
          alignItems: 'flex-start',
          gap: 12,
          padding: 12,
          marginTop: 2,
          borderRadius: 10,
          background: 'var(--nd-surface-2)',
        }}
      >
        <button
          type="button"
          role="switch"
          aria-checked={svc.shared}
          aria-labelledby={`${svc.name}-share`}
          onClick={() => onShare(!svc.shared)}
          disabled={busy}
          style={{
            width: 44,
            height: 26,
            flexShrink: 0,
            borderRadius: 999,
            border: 'none',
            padding: 3,
            display: 'flex',
            justifyContent: svc.shared ? 'flex-end' : 'flex-start',
            background: svc.shared ? 'var(--nd-accent)' : 'var(--nd-track)',
            cursor: busy ? 'not-allowed' : 'pointer',
          }}
        >
          <span style={{ width: 20, height: 20, borderRadius: '50%', background: '#fff', boxShadow: '0 1px 2px rgba(0,0,0,.25)' }} />
        </button>
        <span style={{ fontSize: 13 }}>
          <span id={`${svc.name}-share`} style={{ fontWeight: 600 }}>
            Reachable from other machines
          </span>
          <br />
          <span style={{ color: 'var(--nd-muted)' }}>
            {svc.shared ? `On: anyone who can reach ${hostname} on your network can connect.` : `Off: only ${hostname} itself can connect.`}
          </span>
        </span>
      </div>

      {!localViewer && !svc.shared && (
        <p role="note" style={{ margin: 0, fontSize: 12.5, lineHeight: 1.5, color: 'var(--nd-warn)' }}>
          You are using nano-cloud from another computer. These addresses work on {hostname} itself; turn on Reachable from other machines
          to connect from here.
        </p>
      )}
      {onMac && svc.endpoints.some(e => e.direct) && (
        <p style={{ margin: 0, fontSize: 12, lineHeight: 1.5, color: 'var(--nd-muted)' }}>
          The VM network addresses need your browser allowed under System Settings → Privacy &amp; Security → Local Network. The ones above
          do not.
        </p>
      )}
    </div>
  );
}

type Tab = 'overview' | 'nodes' | 'workloads' | 'access' | 'activity';

const TABS: { id: Tab; label: string; kube?: boolean }[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'nodes', label: 'Nodes', kube: true },
  { id: 'workloads', label: 'Workloads', kube: true },
  { id: 'access', label: 'Access' },
  { id: 'activity', label: 'Activity' },
];

const noop = () => {};

// What Retry does, by the step the service failed in.
const RETRY_NOTE: Record<string, string> = {
  pulling: 'Retry downloads the image again.',
  creating: 'Retry creates the VMs that are missing.',
  'waiting-ip': 'Retry waits for the addresses again. If a VM has no network, restart it from its page first.',
  installing: 'Retry runs the install again on each VM where it failed; the others stay as they are.',
};

function members(svc: Service): ServiceMember[] {
  return svc.members ?? svc.vms.map((vm): ServiceMember => ({ role: '', role_label: '', vm, status: '' }));
}

// eventsOf picks a service's own entries from the feed, newest first.
export function eventsOf(svc: Service, events: NanoEvent[]): NanoEvent[] {
  const own = new Set([svc.name, ...svc.vms]);
  return events.filter(e => e.vm && own.has(e.vm)).reverse();
}

function eventTime(at: string): string {
  const d = new Date(at);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

// clusterWait says why a Kubernetes service's cluster cannot be read yet.
function clusterWait(state: string): string {
  if (state === 'stopped') return 'Start the service to see its cluster.';
  if (state === 'missing') return 'Its VMs no longer exist.';
  if (state === 'failed') return 'The cluster did not come up. Retry it, or see Activity for why.';
  return 'The cluster shows here once its control plane is up.';
}

// What a machine's part in its service reads in: the dot, and its words.
const TONE_DOT: Record<MemberTone, string> = {
  ready: 'var(--nd-ready)',
  busy: 'var(--nd-warn)',
  idle: 'var(--nd-muted)',
  problem: 'var(--nd-danger)',
};
const TONE_TEXT: Record<MemberTone, string> = {
  ready: 'var(--nd-ready)',
  busy: 'var(--nd-warn)',
  idle: 'var(--nd-text-2)',
  problem: 'var(--nd-danger)',
};

const LEVEL_DOT: Record<string, string> = {
  info: 'var(--nd-accent)',
  warn: 'var(--nd-warn)',
  error: 'var(--nd-danger)',
};

export function ServiceDetail({
  svc,
  events = [],
  busy,
  hostname,
  localViewer,
  onShare,
  onRetry,
  onBack,
  onStart,
  onStop,
  onDelete,
  onOpenVM,
  onAddWorker = noop,
  onRemoveWorker = noop,
  onPublish = noop,
  onUnpublish = noop,
}: ServiceDetailProps) {
  const [tab, setTab] = useState<Tab>('overview');
  const kube = svc.console === 'kubernetes';
  const tabs = TABS.filter(t => kube || !t.kube);
  // The cluster can be read once the control plane has come up, and not
  // while the service is stopped.
  const clusterUp = kube && (svc.state === 'ready' || !!svc.ready_at_unix) && svc.state !== 'stopped' && svc.state !== 'missing';
  const {
    cluster,
    error: clusterError,
    refresh,
  } = useCluster(svc.name, clusterUp, tab === 'overview' || tab === 'nodes' || tab === 'workloads');
  // A monitoring service shows what it watches once it is ready.
  const watching = svc.console === 'monitoring' && svc.state === 'ready';
  const { mon, error: monError } = useMonitoring(svc.name, watching && tab === 'overview');
  const rail = serviceRail(svc);
  const progress = buildProgress(svc);
  const up = svc.state === 'ready' || svc.state === 'starting' || building(svc.state);
  const onMac = useCapabilities().backend === 'vz';
  const machines = members(svc);
  const many = machines.length > 1;
  const unreachable = machines.filter(m => m.unreachable).map(m => m.vm);
  const own = eventsOf(svc, events);
  // A VM's log can be read once it has an address: from the install on.
  const canLog = svc.state !== 'missing' && !['pulling', 'creating', 'waiting-ip'].includes(svc.state);

  return (
    <div style={{ padding: '22px clamp(16px, 3vw, 40px) 60px', maxWidth: 1080, width: '100%' }}>
      <button
        onClick={onBack}
        style={{
          background: 'none',
          border: 'none',
          color: 'var(--nd-text-2)',
          fontSize: 13,
          cursor: 'pointer',
          padding: 0,
          marginBottom: 18,
        }}
      >
        ← Services
      </button>

      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 14, marginBottom: 20 }}>
        <ServiceGlyph recipe={svc.recipe} size={52} label={svc.recipe_name} />
        <div style={{ flex: '1 1 260px', minWidth: 0 }}>
          <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 11 }}>
            <h1 style={{ margin: 0, fontSize: 26, fontWeight: 650, letterSpacing: '-0.025em' }}>{svc.name}</h1>
            <ServiceBadge state={svc.state} />
          </div>
          <div style={{ fontFamily: 'var(--nd-font-mono)', fontSize: 12.5, color: 'var(--nd-muted)', marginTop: 3 }}>
            {svc.recipe_name} · {svc.size.label} · {many ? `${machines.length} × ` : ''}
            {sizeSummary(svc.size)}
            {svc.ip ? ` · ${svc.ip}` : ''}
          </div>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          {svc.state !== 'missing' &&
            !building(svc.state) &&
            svc.state !== 'failed' &&
            (up ? (
              <button style={actionButton('plain')} onClick={onStop} disabled={busy}>
                Stop
              </button>
            ) : (
              <button style={actionButton('plain')} onClick={onStart} disabled={busy}>
                Start
              </button>
            ))}
          <button style={actionButton('danger')} onClick={onDelete} disabled={busy}>
            {busy ? 'Working…' : 'Delete'}
          </button>
        </div>
      </div>

      {rail && svc.state !== 'ready' && (
        <div style={{ ...card, marginBottom: 16 }}>
          <BootRail steps={rail} />
          {progress && (
            <div role="status" style={{ marginTop: 12, fontFamily: 'var(--nd-font-mono)', fontSize: 12.5, color: 'var(--nd-warn)' }}>
              {progress}
            </div>
          )}
        </div>
      )}

      {svc.state === 'failed' && (
        <div
          role="alert"
          style={{ ...card, marginBottom: 16, borderColor: 'var(--nd-danger-line)', background: 'var(--nd-danger-bg)', boxShadow: 'none' }}
        >
          <h2 style={{ ...cardTitle, color: 'var(--nd-danger)' }}>{svc.recipe_name} did not come up</h2>
          <pre
            style={{
              margin: 0,
              whiteSpace: 'pre-wrap',
              overflowWrap: 'anywhere',
              fontFamily: 'var(--nd-font-mono)',
              fontSize: 12.5,
              color: 'var(--nd-text)',
            }}
          >
            {svc.reason}
          </pre>
          {svc.failed_in && RETRY_NOTE[svc.failed_in] ? (
            <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 12, marginTop: 14 }}>
              <button
                onClick={onRetry}
                disabled={busy}
                style={{
                  ...actionButton('plain'),
                  border: 'none',
                  background: 'var(--nd-accent)',
                  color: 'var(--nd-on-accent)',
                  fontWeight: 600,
                }}
              >
                Retry
              </button>
              <span style={{ flex: '1 1 260px', fontSize: 13, color: 'var(--nd-text-2)' }}>{RETRY_NOTE[svc.failed_in]}</span>
            </div>
          ) : (
            <p style={{ margin: '12px 0 0', fontSize: 13, color: 'var(--nd-text-2)' }}>
              Its VMs are kept so you can look: open one and use the console, or delete the service to start again.
            </p>
          )}
        </div>
      )}

      {svc.alerts && svc.alerts.length > 0 && <AlertBanner alerts={svc.alerts} />}

      <div
        role="tablist"
        aria-label={svc.name}
        style={{ display: 'flex', gap: 4, borderBottom: '1px solid var(--nd-line)', marginBottom: 18, overflowX: 'auto' }}
      >
        {tabs.map(t => {
          const on = t.id === tab;
          return (
            <button
              key={t.id}
              role="tab"
              id={`${svc.name}-tab-${t.id}`}
              aria-selected={on}
              aria-controls={`${svc.name}-panel`}
              onClick={() => setTab(t.id)}
              style={{
                minHeight: 44,
                padding: '0 14px',
                marginBottom: -1,
                border: 'none',
                borderBottom: `2px solid ${on ? 'var(--nd-accent)' : 'transparent'}`,
                background: 'none',
                color: on ? 'var(--nd-text)' : 'var(--nd-muted)',
                fontSize: 14,
                fontWeight: on ? 600 : 500,
                cursor: 'pointer',
                whiteSpace: 'nowrap',
              }}
            >
              {t.label}
            </button>
          );
        })}
      </div>

      <div role="tabpanel" id={`${svc.name}-panel`} aria-labelledby={`${svc.name}-tab-${tab}`}>
        {tab === 'overview' && (
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 340px), 1fr))',
              gap: 16,
              alignItems: 'start',
            }}
          >
            <section style={card} aria-labelledby="connect">
              <h2 id="connect" style={cardTitle}>
                Connect
              </h2>
              {svc.outputs.length > 0 ? (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
                  {svc.outputs.map(o => (
                    <CopyField key={o.id} label={o.label} value={o.value} secret={o.secret} />
                  ))}
                  {svc.files.length > 0 && (
                    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
                      {svc.files.map(f => (
                        <a
                          key={f.id}
                          href={serviceFileURL(svc.name, f.id)}
                          download
                          style={{
                            display: 'inline-flex',
                            alignItems: 'center',
                            gap: 7,
                            minHeight: 36,
                            padding: '0 14px',
                            borderRadius: 9,
                            background: 'var(--nd-accent)',
                            color: 'var(--nd-on-accent)',
                            fontSize: 13.5,
                            fontWeight: 600,
                            textDecoration: 'none',
                          }}
                        >
                          <svg width="13" height="13" viewBox="0 0 12 12" fill="none" aria-hidden="true">
                            <path
                              d="M6 1v6m0 0L3.5 4.5M6 7l2.5-2.5M2 10h8"
                              stroke="currentColor"
                              strokeWidth="1.4"
                              strokeLinecap="round"
                              strokeLinejoin="round"
                            />
                          </svg>
                          Download {f.label}
                        </a>
                      ))}
                    </div>
                  )}
                </div>
              ) : (
                <p style={{ margin: 0, fontSize: 13.5, color: 'var(--nd-muted)', lineHeight: 1.5 }}>
                  {svc.state === 'stopped'
                    ? 'Start the service to connect to it.'
                    : svc.state === 'failed' || svc.state === 'missing'
                      ? 'Nothing to connect to.'
                      : 'The address and credentials appear here once it is ready.'}
                </p>
              )}
              <button
                onClick={() => setTab('access')}
                style={{
                  marginTop: 14,
                  padding: 0,
                  border: 'none',
                  background: 'none',
                  color: 'var(--nd-accent)',
                  fontSize: 13,
                  fontWeight: 500,
                  cursor: 'pointer',
                }}
              >
                All addresses and logins →
              </button>
            </section>

            <section style={card} aria-labelledby="machine">
              <h2 id="machine" style={cardTitle}>
                {many ? `Machines (${machines.length})` : 'Machine'}
              </h2>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                {machines.map(m => {
                  const gone = m.status === 'missing';
                  // A failed service's machines say how far each got: a running
                  // VM is not a green while the service around it is broken.
                  const doing = building(svc.state) ? memberDoing(m) : svc.state === 'failed' ? memberOutcome(m) : null;
                  const dot = doing
                    ? TONE_DOT[doing.tone]
                    : m.phase === 'ready'
                      ? 'var(--nd-ready)'
                      : m.status === 'running'
                        ? 'var(--nd-warn)'
                        : gone
                          ? 'var(--nd-alert)'
                          : 'var(--nd-muted)';
                  const about = [many ? m.role_label : '', m.ip ?? ''].filter(Boolean).join(' · ');
                  const statusId = `${svc.name}-${m.vm}-doing`;
                  // Up but silent: the one problem a restart may cure. A failed
                  // install is not, and says so in its own words.
                  const canRestart = !!m.unreachable && m.install !== 'failed' && doing?.tone === 'problem' && !gone;
                  return (
                    <div
                      key={m.vm}
                      style={{
                        borderRadius: 9,
                        border: `1px solid ${doing?.tone === 'problem' ? 'var(--nd-danger-line)' : 'var(--nd-line)'}`,
                        background: 'var(--nd-surface-2)',
                      }}
                    >
                      <button
                        onClick={() => onOpenVM(m.vm)}
                        disabled={gone}
                        aria-label={`Open VM ${m.vm}`}
                        aria-describedby={doing ? statusId : undefined}
                        style={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: 10,
                          width: '100%',
                          minHeight: 44,
                          padding: '6px 12px',
                          border: 'none',
                          borderRadius: 9,
                          background: 'none',
                          color: 'var(--nd-text)',
                          cursor: gone ? 'default' : 'pointer',
                          textAlign: 'left',
                        }}
                      >
                        <span
                          aria-hidden="true"
                          style={{
                            width: 7,
                            height: 7,
                            borderRadius: '50%',
                            flexShrink: 0,
                            alignSelf: doing?.detail && doing.tone === 'problem' ? 'flex-start' : 'center',
                            marginTop: doing?.detail && doing.tone === 'problem' ? 6 : 0,
                            background: dot,
                            animation: doing?.tone === 'busy' ? 'ncpulse 1.6s ease-in-out infinite' : 'none',
                          }}
                        />
                        <span style={{ flex: 1, minWidth: 0 }}>
                          <span style={{ display: 'block', fontFamily: 'var(--nd-font-mono)', fontSize: 13 }}>{m.vm}</span>
                          {(about || (doing && doing.tone !== 'problem')) && (
                            <span style={{ display: 'block', fontSize: 11.5, color: 'var(--nd-muted)' }}>
                              {about}
                              {about && doing && doing.tone !== 'problem' ? ' · ' : ''}
                              {doing && doing.tone !== 'problem' && (
                                <span id={statusId} style={{ color: TONE_TEXT[doing.tone] }}>
                                  {doing.text}
                                </span>
                              )}
                            </span>
                          )}
                          {/* A machine holding the build up gets its own lines:
                              what is wrong, why, and what to try. */}
                          {doing?.tone === 'problem' && (
                            <span style={{ display: 'block', marginTop: 4, fontSize: 11.5, lineHeight: 1.45, overflowWrap: 'anywhere' }}>
                              <span id={statusId} style={{ display: 'block', color: TONE_TEXT.problem, fontWeight: 600 }}>
                                {doing.text}
                              </span>
                              {doing.detail && <span style={{ display: 'block', color: 'var(--nd-text-2)' }}>{doing.detail}</span>}
                            </span>
                          )}
                        </span>
                        <span style={{ fontSize: 12, color: 'var(--nd-muted)', flexShrink: 0 }}>{gone ? 'deleted' : 'Open VM →'}</span>
                      </button>
                      {canRestart && (
                        <div style={{ padding: '0 12px 10px 29px' }}>
                          <RestartVM vm={m.vm} />
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            </section>

            {watching && <MonitoringOverview svc={svc} mon={mon} error={monError} />}

            {kube && clusterUp && (
              <div style={{ gridColumn: '1 / -1' }}>
                <ClusterSummary cluster={cluster} error={clusterError} onManage={() => setTab('nodes')} />
              </div>
            )}

            {building(svc.state) && canLog && (
              <section style={{ ...card, gridColumn: '1 / -1' }} aria-labelledby="live-log">
                <h2 id="live-log" style={cardTitle}>
                  Live log
                </h2>
                <ServiceLogView service={svc.name} vms={machines.map(m => m.vm)} unreachable={unreachable} follow />
              </section>
            )}
          </div>
        )}

        {tab === 'nodes' &&
          (clusterUp ? (
            <ClusterNodes
              svc={svc}
              cluster={cluster}
              error={clusterError}
              busy={busy}
              onAddWorker={onAddWorker}
              onRemoveWorker={onRemoveWorker}
              onOpenVM={onOpenVM}
            />
          ) : (
            <p style={{ margin: 0, fontSize: 13.5, color: 'var(--nd-muted)' }}>{clusterWait(svc.state)}</p>
          ))}

        {tab === 'workloads' &&
          (clusterUp ? (
            <ClusterWorkloads
              svc={svc}
              cluster={cluster}
              error={clusterError}
              busy={busy}
              onRefresh={refresh}
              onPublish={onPublish}
              onUnpublish={onUnpublish}
            />
          ) : (
            <p style={{ margin: 0, fontSize: 13.5, color: 'var(--nd-muted)' }}>{clusterWait(svc.state)}</p>
          ))}

        {tab === 'access' && (
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 380px), 1fr))',
              gap: 16,
              alignItems: 'start',
            }}
          >
            <section style={card} aria-labelledby="addresses">
              <h2 id="addresses" style={cardTitle}>
                Addresses
              </h2>
              {svc.endpoints.length > 0 && svc.state !== 'missing' ? (
                <Addresses svc={svc} busy={busy} hostname={hostname} localViewer={localViewer} onMac={onMac} onShare={onShare} />
              ) : (
                <p style={{ margin: 0, fontSize: 13.5, color: 'var(--nd-muted)' }}>Nothing to connect to.</p>
              )}
            </section>

            <section style={card} aria-labelledby="logins">
              <h2 id="logins" style={cardTitle}>
                Log in to the {many ? 'machines' : 'machine'}
              </h2>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
                <CopyField label="Console login" value={svc.vm_user} />
                <CopyField label="Console password" value={svc.vm_password} secret />
                {machines
                  .filter(m => m.ip || (!many && svc.ip))
                  .map(m => (
                    <CopyField
                      key={m.vm}
                      label={many ? `SSH to ${m.vm}, with your key` : 'SSH, with your key'}
                      value={`ssh ${svc.vm_user}@${m.ip ?? svc.ip}`}
                    />
                  ))}
                <p style={{ margin: 0, fontSize: 12.5, color: 'var(--nd-muted)', lineHeight: 1.5 }}>
                  SSH takes the keys you gave when launching it. Every machine's console is on its VM page.
                </p>
              </div>
            </section>
          </div>
        )}

        {tab === 'activity' && (
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 420px), 1fr))',
              gap: 16,
              alignItems: 'start',
            }}
          >
            <section style={card} aria-labelledby="events">
              <h2 id="events" style={cardTitle}>
                Events
              </h2>
              {own.length === 0 ? (
                <p style={{ margin: 0, fontSize: 13.5, color: 'var(--nd-muted)' }}>
                  Nothing yet since nano-cloud started. Builds, retries and failures show here.
                </p>
              ) : (
                <ol style={{ listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column' }}>
                  {own.map(e => (
                    <li key={e.seq} style={{ display: 'flex', gap: 12, padding: '8px 0', borderBottom: '1px solid var(--nd-line-soft)' }}>
                      <span style={{ fontFamily: 'var(--nd-font-mono)', fontSize: 12, color: 'var(--nd-muted)', width: 70, flexShrink: 0 }}>
                        {eventTime(e.at)}
                      </span>
                      <span
                        aria-hidden="true"
                        style={{
                          width: 8,
                          height: 8,
                          marginTop: 6,
                          borderRadius: '50%',
                          flexShrink: 0,
                          background: LEVEL_DOT[e.level] ?? 'var(--nd-muted)',
                        }}
                      />
                      <span style={{ flex: 1, minWidth: 0, fontSize: 13.5 }}>
                        {e.message}
                        {e.detail && (
                          <span
                            style={{
                              display: 'block',
                              marginTop: 2,
                              fontSize: 12.5,
                              color: 'var(--nd-text-2)',
                              whiteSpace: 'pre-wrap',
                              overflowWrap: 'anywhere',
                            }}
                          >
                            {e.detail}
                          </span>
                        )}
                      </span>
                    </li>
                  ))}
                </ol>
              )}
            </section>

            <section style={card} aria-labelledby="install-log">
              <h2 id="install-log" style={cardTitle}>
                Install log
              </h2>
              {canLog ? (
                <ServiceLogView service={svc.name} vms={machines.map(m => m.vm)} unreachable={unreachable} follow={building(svc.state)} />
              ) : (
                <p style={{ margin: 0, fontSize: 13.5, color: 'var(--nd-muted)' }}>
                  {svc.state === 'missing' ? 'Its VM no longer exists.' : 'The log can be read once the VMs have their addresses.'}
                </p>
              )}
            </section>
          </div>
        )}
      </div>
    </div>
  );
}
