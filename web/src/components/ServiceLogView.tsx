import { useCallback, useEffect, useRef, useState } from 'react';
import { getServiceLog } from '../api';
import type { ServiceLog } from '../types';

interface ServiceLogViewProps {
  service: string;
  vms: string[];
  // Read again every few seconds, while a build is under way.
  follow: boolean;
  // Machines the service already knows it cannot reach, marked on their chips.
  unreachable?: string[];
}

// How often a followed log is read again.
const FOLLOW_MS = 3000;
// After a failed read the next one comes sooner than a person would think to
// press Refresh, then backs off so a machine that stays down is not hammered.
const RETRY_MS = 3000;
const RETRY_MAX_MS = 15000;

// What the view knows of the machine it shows. A failed read keeps log and
// readAt; reads counts finished reads (failed or not) since the machine was
// picked, so a timer can be started after each.
interface ReadState {
  log: ServiceLog | null;
  readAt: Date | null;
  error: string | null;
  failures: number;
  reads: number;
}

const FRESH: ReadState = { log: null, readAt: null, error: null, failures: 0, reads: 0 };

export function retryDelay(failures: number): number {
  return Math.min(RETRY_MS * 2 ** Math.max(0, failures - 1), RETRY_MAX_MS);
}

// shortName drops the service's own name from a VM's: "agent-1" for
// cluster-1-agent-1. A single-VM service's VM keeps its whole name.
function shortName(service: string, vm: string): string {
  return vm.startsWith(service + '-') ? vm.slice(service.length + 1) : vm;
}

function stateColor(state: string): string {
  if (state.startsWith('failed')) return 'var(--nd-danger)';
  if (state === 'ready') return 'var(--nd-ready)';
  return 'var(--nd-warn)';
}

// ServiceLogView shows the end of one of a service's VMs' install log, read
// over SSH by nano-cloud — how a node was set up, during the build and after.
export function ServiceLogView({ service, vms, follow, unreachable = [] }: ServiceLogViewProps) {
  const [vm, setVM] = useState(vms[0] ?? '');
  const [read, setRead] = useState<ReadState>(FRESH);
  const { log, readAt, error, failures, reads } = read;
  const [copied, setCopied] = useState(false);
  const pre = useRef<HTMLPreElement>(null);
  // The VM a read was made for; an answer for another one is dropped.
  const current = useRef(vm);
  current.current = vm;

  const load = useCallback(async () => {
    if (!vm) return;
    try {
      const l = await getServiceLog(service, vm);
      if (current.current !== vm) return;
      setRead(r => ({ log: l, readAt: new Date(), error: null, failures: 0, reads: r.reads + 1 }));
    } catch (e) {
      if (current.current !== vm) return;
      // The last good log stays on screen; only the status around it changes.
      const why = e instanceof Error ? e.message : String(e);
      setRead(r => ({ ...r, error: why, failures: r.failures + 1, reads: r.reads + 1 }));
    }
  }, [service, vm]);

  useEffect(() => {
    setRead(FRESH);
    load();
  }, [load]);

  // One timer, started when a read finishes: soon after a failure, at the
  // follow interval otherwise. A read that finishes starts the next timer, so
  // the two schedules cannot double up.
  useEffect(() => {
    if (reads === 0) return;
    const wait = failures > 0 ? retryDelay(failures) : follow ? FOLLOW_MS : 0;
    if (!wait) return;
    const id = setTimeout(load, wait);
    return () => clearTimeout(id);
  }, [reads, failures, follow, load]);

  // A followed log stays scrolled to its newest line.
  useEffect(() => {
    if (follow && pre.current) pre.current.scrollTop = pre.current.scrollHeight;
  }, [follow, log]);

  async function copy() {
    if (!log) return;
    try {
      await navigator.clipboard.writeText(log.log);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // No clipboard (insecure context): the log is selectable instead.
    }
  }

  const chip = (on: boolean): React.CSSProperties => ({
    minHeight: 32,
    padding: '0 12px',
    borderRadius: 999,
    border: `1px solid ${on ? 'var(--nd-accent-line)' : 'var(--nd-line)'}`,
    background: on ? 'var(--nd-accent-soft)' : 'var(--nd-surface)',
    color: 'var(--nd-text)',
    fontFamily: 'var(--nd-font-mono)',
    fontSize: 12,
    cursor: 'pointer',
  });

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8 }}>
        {vms.length > 1 && (
          <div role="group" aria-label="Machine" style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
            {vms.map(v => (
              <button key={v} type="button" aria-pressed={v === vm} onClick={() => setVM(v)} style={chip(v === vm)}>
                {(unreachable.includes(v) || (v === vm && error)) && (
                  <span
                    role="img"
                    aria-label="not answering"
                    style={{
                      display: 'inline-block',
                      width: 6,
                      height: 6,
                      marginRight: 6,
                      borderRadius: '50%',
                      background: 'var(--nd-warn)',
                      verticalAlign: 'middle',
                    }}
                  />
                )}
                {shortName(service, v)}
              </button>
            ))}
          </div>
        )}
        {log && (
          <span style={{ fontSize: 12.5, color: stateColor(log.state) }}>{log.state.startsWith('failed') ? 'failed' : log.state}</span>
        )}
        <span style={{ flex: 1 }} />
        <button
          type="button"
          onClick={load}
          style={{
            minHeight: 32,
            padding: '0 12px',
            borderRadius: 8,
            border: '1px solid var(--nd-line)',
            background: 'var(--nd-surface)',
            color: 'var(--nd-text)',
            fontSize: 12.5,
            cursor: 'pointer',
          }}
        >
          Refresh
        </button>
        <button
          type="button"
          onClick={copy}
          disabled={!log}
          style={{
            minHeight: 32,
            padding: '0 12px',
            borderRadius: 8,
            border: '1px solid var(--nd-line)',
            background: 'var(--nd-surface)',
            color: 'var(--nd-text)',
            fontSize: 12.5,
            cursor: log ? 'pointer' : 'default',
          }}
        >
          {copied ? 'Copied' : 'Copy log'}
        </button>
      </div>
      <div role="status" style={{ minHeight: 20, fontSize: 12, color: 'var(--nd-muted)' }}>
        {error ? (
          <>
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8, color: 'var(--nd-warn)' }}>
              <span
                aria-hidden="true"
                style={{
                  width: 7,
                  height: 7,
                  borderRadius: '50%',
                  background: 'var(--nd-warn)',
                  animation: 'ncpulse 1.6s ease-in-out infinite',
                }}
              />
              {/ssh/i.test(error)
                ? `${shortName(service, vm)} isn't answering over SSH — trying again…`
                : `Can't read ${shortName(service, vm)}'s log right now — trying again…`}
            </span>
            {readAt && <span> Showing what was read at {readAt.toLocaleTimeString()}.</span>}
            <details style={{ display: 'inline', marginLeft: 8 }}>
              <summary style={{ display: 'inline', cursor: 'pointer' }}>Details</summary>
              <div style={{ marginTop: 4, overflowWrap: 'anywhere' }}>{error}</div>
            </details>
          </>
        ) : (
          <>
            /var/log/nanocloud-service.log on {vm}
            {follow ? `, read again every ${FOLLOW_MS / 1000} s.` : '.'}
          </>
        )}
      </div>
      <pre
        ref={pre}
        aria-label={`Install log of ${vm}`}
        aria-live={follow ? 'polite' : undefined}
        style={{
          margin: 0,
          padding: '12px 14px',
          minHeight: 160,
          maxHeight: 360,
          overflow: 'auto',
          borderRadius: 10,
          border: '1px solid var(--nd-line)',
          background: 'var(--nd-term)',
          color: log ? 'var(--nd-term-text)' : 'var(--nd-muted)',
          fontFamily: 'var(--nd-font-mono)',
          fontSize: 12,
          lineHeight: 1.6,
          whiteSpace: 'pre-wrap',
          overflowWrap: 'anywhere',
        }}
      >
        {log
          ? log.log || 'The log is empty so far.'
          : error
            ? `Waiting for ${shortName(service, vm)} to answer. Its log appears here as soon as it does.`
            : 'Reading the log…'}
      </pre>
    </div>
  );
}
