import { useEffect, useState } from 'react';
import type { HostBridge, VM, VMNetwork, VMSpec } from '../../types';
import { getVMSpec, patchVMSpec } from '../../api';
import { Card } from '../../ui/Card';
import { NetworkPicker } from '../CreateVMModal';
import { ApplyRow, Banner } from '../HardwareTab';
import { FactRow } from './FactRow';

interface NetworkTabProps {
  vm: VM;
  ip: string;
  bridges: HostBridge[];
  // Empty means no VLAN can be offered on this host.
  uplink: string;
  onError?: (fallback: string, err: unknown) => void;
  onRefreshVMs: () => void;
}

export function NetworkTab({ vm, ip, bridges, uplink, onError, onRefreshVMs }: NetworkTabProps) {
  const [spec, setSpec] = useState<VMSpec | null>(null);
  // The edit in progress, seeded from the spec and compared against it to
  // decide whether there is anything to apply.
  const [net, setNet] = useState<VMNetwork | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    // Swallowed on failure: a VM defined by hand with virsh has no spec, and
    // the observed facts below are worth showing regardless.
    getVMSpec(vm.id)
      .then(s => {
        setSpec(s);
        setNet(s.network);
      })
      .catch(() => setNet(null));
  }, [vm.id]);

  const dirty = net !== null && spec !== null && JSON.stringify(net) !== JSON.stringify(spec.network);
  const bridged = vm.network_mode === 'bridge';
  const modeLabel = bridged ? 'Bridge' : 'NAT';
  const badge = bridged
    ? { bg: 'var(--nd-accent-soft)', color: 'var(--nd-accent)' }
    : { bg: 'var(--nd-ready-bg)', color: 'var(--nd-ready)' };

  async function save() {
    if (!net) return;
    setSaving(true);
    try {
      const next = await patchVMSpec(vm.id, { network: net });
      setSpec(next);
      setNet(next.network);
      // The badge and the facts below come from GET /vms, not the spec, so
      // refresh now rather than wait for the next poll.
      onRefreshVMs();
    } catch (e) {
      onError?.('Could not change this VM’s network', e);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
      <Card as="div" style={{ padding: 0, overflow: 'hidden' }}>
        <div
          style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '18px 22px', borderBottom: '1px solid var(--nd-line-soft)' }}
        >
          <div
            style={{
              width: 36,
              height: 36,
              borderRadius: 10,
              background: 'var(--nd-surface-2)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              color: 'var(--nd-text-2)',
              flexShrink: 0,
            }}
          >
            <svg width="18" height="18" viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <circle cx="8" cy="8" r="6.3" stroke="currentColor" strokeWidth="1.4" />
              <path
                d="M8 1.7c1.8 1.7 2.8 4 2.8 6.3S9.8 12.6 8 14.3M8 1.7C6.2 3.4 5.2 5.7 5.2 8s1 4.6 2.8 6.3M1.7 8h12.6"
                stroke="currentColor"
                strokeWidth="1.4"
              />
            </svg>
          </div>
          <div>
            <div style={{ fontWeight: 600, fontSize: 14 }}>{bridged ? vm.bridge || 'Bridge' : 'NAT'}</div>
            <div style={{ fontSize: 12, color: 'var(--nd-muted)' }}>{bridged ? 'Host bridge' : 'libvirt default network'}</div>
          </div>
          <span
            style={{
              marginLeft: 'auto',
              background: badge.bg,
              color: badge.color,
              fontSize: 12,
              fontWeight: 600,
              padding: '4px 10px',
              borderRadius: 20,
            }}
          >
            {modeLabel}
          </span>
        </div>
        <div style={{ padding: '6px 22px' }}>
          <FactRow k="IPv4" v={ip} />
          {/* The MAC libvirt assigned at define time, from the spec: a bridge VM has
              no lease to read it from. */}
          <FactRow k="MAC" v={vm.mac || '—'} />
          {vm.vlan_id ? <FactRow k="VLAN" v={String(vm.vlan_id)} /> : null}
          <FactRow k="Mode" v={modeLabel} />
        </div>
      </Card>

      <Card as="div" style={{ padding: '18px 20px', display: 'flex', flexDirection: 'column', gap: 14 }}>
        {spec?.restart_required && (
          <Banner tone="warn">
            <strong>Restart to apply.</strong> This VM’s recorded network differs from the machine that is running. A network change
            rebuilds the VM’s definition, which the guest only picks up when it next boots — stop it and start it again.
          </Banner>
        )}

        <div>
          <h3 style={{ fontSize: 14, fontWeight: 600, color: 'var(--nd-text)', margin: 0 }}>Change network</h3>
          <p style={{ fontSize: 12.5, color: 'var(--nd-muted)', margin: '4px 0 0', lineHeight: 1.5 }}>
            Where this VM attaches to the host. The address above is what the guest actually holds — a tag says which VLAN it was asked for,
            never which one it reached.
          </p>
        </div>

        {net && <NetworkPicker value={net} bridges={bridges} uplink={uplink} disabled={saving} onChange={setNet} />}

        <ApplyRow dirty={dirty} saving={saving} onApply={save} onDiscard={() => setNet(spec?.network ?? null)} />
      </Card>
    </div>
  );
}
