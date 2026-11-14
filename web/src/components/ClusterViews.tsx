import { useCallback, useEffect, useId, useState } from 'react';
import { applyManifest, getServiceCluster } from '../api';
import type { ApplyResult, Cluster, ClusterNode, Service } from '../types';
import { surfaceCard } from '../design';

// The service console's views of a Kubernetes service: a summary on its
// Overview, its nodes, and its workloads. The cluster is read through
// nano-cloud (kubectl on the control plane), every few seconds while shown.

const CLUSTER_POLL_MS = 5000;

// useCluster reads a Kubernetes service's cluster while enabled, again every
// few seconds while polling.
export function useCluster(name: string, enabled: boolean, polling: boolean) {
  const [cluster, setCluster] = useState<Cluster | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      setCluster(await getServiceCluster(name));
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [name]);

  useEffect(() => {
    if (enabled) refresh();
  }, [enabled, refresh]);

  useEffect(() => {
    if (!enabled || !polling) return;
    const id = setInterval(refresh, CLUSTER_POLL_MS);
    return () => clearInterval(id);
  }, [enabled, polling, refresh]);

  return { cluster, error, refresh };
}

const card: React.CSSProperties = {
  ...surfaceCard,
  padding: 20,
};
const cardTitle: React.CSSProperties = { margin: 0, fontSize: 15, fontWeight: 600 };
const mono: React.CSSProperties = { fontFamily: 'var(--nd-font-mono)' };
const th: React.CSSProperties = {
  textAlign: 'left',
  padding: '10px 12px',
  fontSize: 11.5,
  fontWeight: 600,
  color: 'var(--nd-muted)',
  textTransform: 'uppercase',
  letterSpacing: '0.05em',
  borderBottom: '1px solid var(--nd-line)',
  whiteSpace: 'nowrap',
};
const td: React.CSSProperties = {
  padding: '10px 12px',
  fontSize: 13,
  borderBottom: '1px solid var(--nd-line-soft)',
  verticalAlign: 'middle',
};

function button(kind: 'primary' | 'plain' | 'danger' | 'quiet'): React.CSSProperties {
  const base: React.CSSProperties = {
    minHeight: 36,
    padding: '0 14px',
    borderRadius: 9,
    fontSize: 13,
    fontWeight: 500,
    cursor: 'pointer',
    whiteSpace: 'nowrap',
  };
  switch (kind) {
    case 'primary':
      return { ...base, border: 'none', background: 'var(--nd-accent)', color: 'var(--nd-on-accent)', fontWeight: 600 };
    case 'danger':
      return { ...base, border: '1px solid var(--nd-danger-line)', background: 'var(--nd-danger-bg)', color: 'var(--nd-danger)' };
    case 'quiet':
      return { ...base, border: 'none', background: 'none', color: 'var(--nd-accent)', padding: '0 4px' };
    default:
      return { ...base, border: '1px solid var(--nd-line)', background: 'var(--nd-surface)', color: 'var(--nd-text)' };
  }
}

function Bar({ pct, label }: { pct?: number; label: string }) {
  if (pct === undefined) return <span style={{ color: 'var(--nd-muted)' }}>—</span>;
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8, minWidth: 96 }}>
      <span
        role="meter"
        aria-label={label}
        aria-valuenow={pct}
        aria-valuemin={0}
        aria-valuemax={100}
        style={{ width: 52, height: 5, borderRadius: 3, background: 'var(--nd-track)', overflow: 'hidden' }}
      >
        <span
          style={{
            display: 'block',
            height: '100%',
            width: `${Math.min(100, pct)}%`,
            background: pct > 85 ? 'var(--nd-warn)' : 'var(--nd-accent)',
          }}
        />
      </span>
      <span style={{ ...mono, fontSize: 12 }}>{pct}%</span>
    </span>
  );
}

function Problem({ text }: { text: string }) {
  return <p style={{ margin: 0, fontSize: 13.5, color: 'var(--nd-muted)' }}>{text}</p>;
}

function age(unix?: number): string {
  if (!unix) return '—';
  const s = Math.max(0, Date.now() / 1000 - unix);
  if (s < 60) return `${Math.floor(s)}s`;
  if (s < 3600) return `${Math.max(1, Math.round(s / 60))}m`;
  if (s < 86400) return `${Math.round(s / 3600)}h`;
  return `${Math.round(s / 86400)}d`;
}

// ClusterSummary is the cluster at a glance, on the service's Overview.
export function ClusterSummary({ cluster, error, onManage }: { cluster: Cluster | null; error: string | null; onManage: () => void }) {
  const ready = cluster?.nodes.filter(n => n.ready).length ?? 0;
  return (
    <section style={card} aria-labelledby="cluster-summary">
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10, marginBottom: 14 }}>
        <h2 id="cluster-summary" style={cardTitle}>
          Cluster
        </h2>
        <button onClick={onManage} style={button('quiet')}>
          Manage nodes →
        </button>
      </div>
      {!cluster ? (
        <Problem text={error ?? 'Reading the cluster…'} />
      ) : (
        <dl style={{ margin: 0, display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))', gap: 14 }}>
          {[
            ['Nodes ready', `${ready} / ${cluster.nodes.length}`],
            [
              'Pods running',
              `${cluster.pods.running}${cluster.pods.pending ? ` · ${cluster.pods.pending} pending` : ''}${cluster.pods.failed ? ` · ${cluster.pods.failed} failed` : ''}`,
            ],
            ['Kubernetes', cluster.version || '—'],
          ].map(([k, v]) => (
            <div key={k}>
              <dt style={{ fontSize: 12.5, color: 'var(--nd-muted)' }}>{k}</dt>
              <dd style={{ margin: '2px 0 0', fontSize: 18, fontWeight: 650, letterSpacing: '-0.01em', overflowWrap: 'anywhere' }}>{v}</dd>
            </div>
          ))}
        </dl>
      )}
    </section>
  );
}

type NodeRow = { name: string; node?: ClusterNode; role: string; removable: boolean; joining: boolean; leaving: boolean };

function nodeStatus(r: NodeRow): { text: string; color: string } {
  if (r.leaving) return { text: 'Leaving · moving its pods', color: 'var(--nd-warn)' };
  if (r.joining) return { text: 'Joining', color: 'var(--nd-warn)' };
  if (!r.node) return { text: 'Not in the cluster', color: 'var(--nd-muted)' };
  if (!r.node.ready) return { text: 'Not ready', color: 'var(--nd-danger)' };
  if (r.node.unschedulable) return { text: 'Cordoned', color: 'var(--nd-warn)' };
  return { text: 'Ready', color: 'var(--nd-ready)' };
}

interface ClusterNodesProps {
  svc: Service;
  cluster: Cluster | null;
  error: string | null;
  busy: boolean;
  onAddWorker: () => void;
  onRemoveWorker: (vm: string) => void;
  onOpenVM: (vm: string) => void;
}

// ClusterNodes lists the cluster's nodes, with adding and removing workers.
export function ClusterNodes({ svc, cluster, error, busy, onAddWorker, onRemoveWorker, onOpenVM }: ClusterNodesProps) {
  const [confirm, setConfirm] = useState<string | null>(null);
  const scale = svc.scale;
  const changing = (svc.adding?.length ?? 0) > 0 || (svc.removing?.length ?? 0) > 0;
  const canChange = !busy && !changing && svc.state === 'ready';

  // Every machine of the service, matched with its node when it has one.
  const rows: NodeRow[] = (svc.members ?? []).map(m => ({
    name: m.vm,
    node: cluster?.nodes.find(n => n.name === m.vm),
    role: m.role_label,
    removable: !!scale && m.role === scale.role && scale.count > 1,
    joining: !!svc.adding?.includes(m.vm),
    leaving: !!svc.removing?.includes(m.vm),
  }));
  for (const n of cluster?.nodes ?? []) {
    if (!rows.some(r => r.name === n.name))
      rows.push({
        name: n.name,
        node: n,
        role: n.roles.includes('control-plane') ? 'Control plane' : 'Worker',
        removable: false,
        joining: false,
        leaving: false,
      });
  }
  const workers = scale ? rows.filter(r => r.role === scale.label).length : 0;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 12 }}>
        <div style={{ flex: '1 1 260px' }}>
          <div style={{ fontWeight: 600, fontSize: 15 }}>
            {rows.length} {rows.length === 1 ? 'node' : 'nodes'}
            {scale
              ? ` · ${rows.length - workers} control plane, ${workers} ${workers === 1 ? scale.label.toLowerCase() : `${scale.label.toLowerCase()}s`}`
              : ''}
          </div>
          <div style={{ fontSize: 12.5, color: 'var(--nd-muted)' }}>
            {cluster ? `Read ${age(cluster.read_at_unix)} ago from the control plane.` : (error ?? 'Reading the cluster…')}
          </div>
        </div>
        {scale && (
          <button
            onClick={onAddWorker}
            disabled={!canChange}
            title={canChange ? undefined : `Wait until ${svc.name} is ready`}
            style={{ ...button('primary'), opacity: canChange ? 1 : 0.55, cursor: canChange ? 'pointer' : 'not-allowed' }}
          >
            + Add {scale.label.toLowerCase()}
          </button>
        )}
      </div>

      <div style={{ overflowX: 'auto', border: '1px solid var(--nd-line)', borderRadius: 14, background: 'var(--nd-surface)' }}>
        <table style={{ width: '100%', minWidth: 860, borderCollapse: 'collapse' }}>
          <thead>
            <tr>
              {['Node', 'Role', 'Status', 'Kubernetes', 'Address', 'CPU', 'Memory', 'Pods', ''].map(h => (
                <th key={h} style={th}>
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map(r => {
              const st = nodeStatus(r);
              return (
                <tr key={r.name}>
                  <td style={{ ...td, ...mono, whiteSpace: 'nowrap' }}>{r.name}</td>
                  <td style={{ ...td, color: 'var(--nd-text-2)', whiteSpace: 'nowrap' }}>{r.role}</td>
                  <td style={{ ...td, whiteSpace: 'nowrap' }}>
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7, color: st.color }}>
                      <span aria-hidden="true" style={{ width: 7, height: 7, borderRadius: '50%', background: st.color }} />
                      {st.text}
                    </span>
                  </td>
                  <td style={{ ...td, ...mono, fontSize: 12, color: 'var(--nd-text-2)' }}>{r.node?.version ?? '—'}</td>
                  <td style={{ ...td, ...mono, fontSize: 12, color: 'var(--nd-text-2)' }}>{r.node?.ip ?? '—'}</td>
                  <td style={td}>
                    <Bar pct={r.node?.cpu_percent} label={`CPU of ${r.name}`} />
                  </td>
                  <td style={td}>
                    <Bar pct={r.node?.memory_percent} label={`Memory of ${r.name}`} />
                  </td>
                  <td style={td}>{r.node ? r.node.pods : '—'}</td>
                  <td style={{ ...td, textAlign: 'right', whiteSpace: 'nowrap' }}>
                    <button onClick={() => onOpenVM(r.name)} style={button('plain')} aria-label={`Open VM ${r.name}`}>
                      VM
                    </button>{' '}
                    {r.removable && (
                      <button
                        onClick={() => setConfirm(r.name)}
                        disabled={!canChange}
                        style={{ ...button('danger'), opacity: canChange ? 1 : 0.55 }}
                        aria-label={`Remove ${r.name}`}
                      >
                        Remove
                      </button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {confirm && (
        <div
          role="alertdialog"
          aria-label={`Remove ${confirm}?`}
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: 10,
            padding: '16px 18px',
            borderRadius: 14,
            border: '1px solid var(--nd-danger-line)',
            background: 'var(--nd-danger-bg)',
          }}
        >
          <strong style={{ fontSize: 14.5 }}>
            Remove {confirm} from {svc.name}?
          </strong>
          <span style={{ fontSize: 13.5, color: 'var(--nd-text-2)', lineHeight: 1.5 }}>
            Its pods move to the other nodes first (kubectl drain). Then it leaves the cluster and its VM is deleted. The cluster keeps
            serving throughout; a minute or two.
          </span>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
            <button
              onClick={() => {
                onRemoveWorker(confirm);
                setConfirm(null);
              }}
              style={{ ...button('danger'), fontWeight: 600 }}
            >
              Drain and remove
            </button>
            <button onClick={() => setConfirm(null)} style={button('plain')}>
              Cancel
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

// schemeOf guesses whether a NodePort serves a web page from its service
// port, so a published one gets an Open link.
export function schemeOf(port: number, name?: string): string | undefined {
  if (port === 443 || port === 8443 || name === 'https') return 'https';
  if ([80, 3000, 5000, 8000, 8080, 8888].includes(port) || name === 'http' || name === 'web') return 'http';
  return undefined;
}

interface ClusterWorkloadsProps {
  svc: Service;
  cluster: Cluster | null;
  error: string | null;
  busy: boolean;
  onRefresh: () => void;
  onPublish: (nodePort: number, label: string, scheme?: string) => void;
  onUnpublish: (nodePort: number) => void;
}

// ClusterWorkloads lists deployments, applies manifests, and publishes the
// cluster's NodePorts through nano-cloud.
export function ClusterWorkloads({ svc, cluster, error, busy, onRefresh, onPublish, onUnpublish }: ClusterWorkloadsProps) {
  const [ns, setNs] = useState('all');
  const [applyOpen, setApplyOpen] = useState(false);
  const [manifest, setManifest] = useState('');
  const [applying, setApplying] = useState(false);
  const [result, setResult] = useState<ApplyResult | null>(null);
  const manifestId = useId();

  if (!cluster) return <Problem text={error ?? 'Reading the cluster…'} />;

  const namespaces = [...new Set(cluster.deployments.map(d => d.namespace))].sort();
  const shown = cluster.deployments.filter(d => ns === 'all' || d.namespace === ns);

  // Every NodePort in the cluster, then any published one whose Service has
  // since gone, so it can still be unpublished.
  const rows: { nodePort: number; label: string; detail: string; scheme?: string }[] = cluster.exposed.flatMap(e =>
    e.ports
      .filter(p => p.node_port)
      .map(p => ({
        nodePort: p.node_port!,
        label: `${e.namespace}/${e.name}`,
        detail: `${e.type} · ${p.port} → ${p.node_port}`,
        scheme: schemeOf(p.port, p.name),
      })),
  );
  for (const ep of svc.endpoints) {
    if (ep.published && !rows.some(r => `np${r.nodePort}` === ep.id)) {
      rows.push({ nodePort: ep.port, label: ep.label, detail: 'no longer in the cluster' });
    }
  }

  async function apply() {
    setApplying(true);
    setResult(null);
    try {
      setResult(await applyManifest(svc.name, manifest));
      onRefresh();
    } catch (e) {
      setResult({ ok: false, output: e instanceof Error ? e.message : String(e) });
    } finally {
      setApplying(false);
    }
  }

  const chip = (on: boolean): React.CSSProperties => ({
    minHeight: 34,
    padding: '0 13px',
    borderRadius: 999,
    border: `1px solid ${on ? 'var(--nd-accent-line)' : 'var(--nd-line)'}`,
    background: on ? 'var(--nd-accent-soft)' : 'var(--nd-surface)',
    color: 'var(--nd-text)',
    fontSize: 13,
    cursor: 'pointer',
  });

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8 }}>
        <div role="group" aria-label="Namespace" style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
          {['all', ...namespaces].map(n => (
            <button key={n} aria-pressed={ns === n} onClick={() => setNs(n)} style={chip(ns === n)}>
              {n === 'all' ? 'All namespaces' : n}
            </button>
          ))}
        </div>
        <span style={{ flex: 1 }} />
        <button onClick={() => setApplyOpen(o => !o)} aria-expanded={applyOpen} style={button('plain')}>
          Apply a manifest
        </button>
      </div>

      {applyOpen && (
        <section
          style={{ ...card, borderColor: 'var(--nd-accent-line)', display: 'flex', flexDirection: 'column', gap: 10 }}
          aria-label="Apply a manifest"
        >
          <label htmlFor={manifestId} style={{ fontSize: 13, fontWeight: 500 }}>
            Manifest (YAML)
          </label>
          <textarea
            id={manifestId}
            value={manifest}
            onChange={e => setManifest(e.target.value)}
            rows={10}
            spellCheck={false}
            placeholder={'apiVersion: apps/v1\nkind: Deployment\n…'}
            style={{
              width: '100%',
              boxSizing: 'border-box',
              padding: '10px 12px',
              borderRadius: 9,
              border: '1px solid var(--nd-line)',
              background: 'var(--nd-term)',
              color: 'var(--nd-term-text)',
              ...mono,
              fontSize: 12.5,
              resize: 'vertical',
            }}
          />
          <span style={{ fontSize: 12.5, color: 'var(--nd-muted)' }}>Runs kubectl apply on the control plane. Up to 64 KB.</span>
          <div style={{ display: 'flex', gap: 8 }}>
            <button
              onClick={apply}
              disabled={applying || !manifest.trim()}
              style={{ ...button('primary'), opacity: applying || !manifest.trim() ? 0.55 : 1 }}
            >
              {applying ? 'Applying…' : 'Apply'}
            </button>
            <button onClick={() => setApplyOpen(false)} style={button('plain')}>
              Close
            </button>
          </div>
          {result && (
            <pre
              role="status"
              style={{
                margin: 0,
                padding: '10px 12px',
                borderRadius: 9,
                border: `1px solid ${result.ok ? 'var(--nd-ready-line)' : 'var(--nd-danger-line)'}`,
                background: result.ok ? 'var(--nd-ready-bg)' : 'var(--nd-danger-bg)',
                color: result.ok ? 'var(--nd-text)' : 'var(--nd-danger)',
                ...mono,
                fontSize: 12,
                whiteSpace: 'pre-wrap',
                overflowWrap: 'anywhere',
              }}
            >
              {result.output || (result.ok ? 'Applied.' : 'kubectl refused the manifest.')}
            </pre>
          )}
        </section>
      )}

      <div style={{ overflowX: 'auto', border: '1px solid var(--nd-line)', borderRadius: 14, background: 'var(--nd-surface)' }}>
        <table style={{ width: '100%', minWidth: 720, borderCollapse: 'collapse' }} aria-label="Deployments">
          <thead>
            <tr>
              {['Deployment', 'Namespace', 'Ready', 'Image', 'Age'].map(h => (
                <th key={h} style={th}>
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {shown.length === 0 && (
              <tr>
                <td colSpan={5} style={{ ...td, color: 'var(--nd-muted)' }}>
                  No deployments{ns === 'all' ? '' : ` in ${ns}`} yet. Apply a manifest to add one.
                </td>
              </tr>
            )}
            {shown.map(d => {
              const ok = d.ready >= d.desired;
              return (
                <tr key={`${d.namespace}/${d.name}`}>
                  <td style={{ ...td, ...mono, whiteSpace: 'nowrap' }}>{d.name}</td>
                  <td style={{ ...td, color: 'var(--nd-text-2)' }}>{d.namespace}</td>
                  <td style={td}>
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}>
                      <span
                        aria-hidden="true"
                        style={{ width: 7, height: 7, borderRadius: '50%', background: ok ? 'var(--nd-ready)' : 'var(--nd-warn)' }}
                      />
                      {d.ready}/{d.desired}
                    </span>
                  </td>
                  <td style={{ ...td, ...mono, fontSize: 12, color: 'var(--nd-text-2)', overflowWrap: 'anywhere' }}>
                    {d.images.join(', ')}
                  </td>
                  <td style={{ ...td, color: 'var(--nd-muted)' }}>{age(d.created_unix)}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      <section style={card} aria-labelledby="exposed">
        <h2 id="exposed" style={{ ...cardTitle, marginBottom: 6 }}>
          Reachable from this computer
        </h2>
        <p style={{ margin: '0 0 12px', fontSize: 12.5, color: 'var(--nd-muted)' }}>
          The cluster's NodePort and LoadBalancer services. Publishing one gives it a stable address through nano-cloud.
        </p>
        {rows.length === 0 ? (
          <Problem text="None yet. A Service of type NodePort shows here." />
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            {rows.map(r => {
              const ep = svc.endpoints.find(x => x.id === `np${r.nodePort}`);
              return (
                <div
                  key={`${r.label}:${r.nodePort}`}
                  style={{
                    display: 'flex',
                    flexWrap: 'wrap',
                    alignItems: 'center',
                    gap: 10,
                    minHeight: 48,
                    padding: '6px 12px',
                    borderRadius: 10,
                    background: 'var(--nd-surface-2)',
                  }}
                >
                  <span style={{ flex: '1 1 220px', minWidth: 0 }}>
                    <span style={{ ...mono, fontSize: 13 }}>{r.label}</span>
                    <span style={{ fontSize: 12, color: 'var(--nd-muted)' }}> · {r.detail}</span>
                  </span>
                  {ep ? (
                    <>
                      {ep.address ? (
                        <span style={{ ...mono, fontSize: 13 }}>{ep.address}</span>
                      ) : (
                        <span style={{ fontSize: 12.5, color: ep.problem ? 'var(--nd-danger)' : 'var(--nd-muted)' }}>
                          {ep.problem ?? 'Not forwarded yet'}
                        </span>
                      )}
                      {ep.address && (ep.scheme === 'http' || ep.scheme === 'https') && (
                        <a
                          href={`${ep.scheme}://${ep.address}`}
                          target="_blank"
                          rel="noreferrer"
                          style={{ fontSize: 13, textDecoration: 'none' }}
                        >
                          Open ↗
                        </a>
                      )}
                      <button
                        onClick={() => onUnpublish(r.nodePort)}
                        disabled={busy}
                        style={button('plain')}
                        aria-label={`Unpublish ${r.label}`}
                      >
                        Unpublish
                      </button>
                    </>
                  ) : (
                    <button
                      onClick={() => onPublish(r.nodePort, r.label, r.scheme)}
                      disabled={busy}
                      style={button('plain')}
                      aria-label={`Publish ${r.label}`}
                    >
                      Publish
                    </button>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </section>
    </div>
  );
}
