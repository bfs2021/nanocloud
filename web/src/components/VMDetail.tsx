import { useState } from 'react';
import type { HostBridge, NetworkLease, Service, VM } from '../types';
import { getOS, bootProgress } from '../design';
import { OsChip } from './OsChip';
import { StatusBadge } from './StatusBadge';
import { BootProgressLine } from './BootProgressLine';
import { BootRail, railSteps } from '../ui/BootRail';
import { Tabs, type TabItem } from '../ui/Tabs';
import { useCapabilities } from '../capabilities';
import { HardwareTab } from './HardwareTab';
import { CloneVMModal, SaveTemplateModal } from './VMCopyDialogs';
import { ConsoleTab } from './vm-detail/ConsoleTab';
import { NetworkTab } from './vm-detail/NetworkTab';
import { OverviewTab } from './vm-detail/OverviewTab';
import { ServiceBanner } from './vm-detail/ServiceBanner';
import { StorageTab } from './vm-detail/StorageTab';
import { VMActions } from './vm-detail/VMActions';
import { useVMStats } from './vm-detail/useVMStats';

interface VMDetailProps {
  vm: VM;
  leases: NetworkLease[];
  // The host's bridges, for the Network tab's picker.
  bridges: HostBridge[];
  // The host's default-route interface, for the same picker's VLAN field —
  // empty means no VLAN can be offered on this host.
  uplink: string;
  onBack: () => void;
  onStart: (id: string) => void;
  onStop: (id: string) => void;
  // Force-off, offered once a graceful shutdown has gone unanswered long
  // enough that the guest is not going to honour it (no ACPI daemon, hung
  // kernel, sitting at a bootloader).
  onForceStop?: (id: string) => void;
  onDelete?: (id: string) => void;
  canForceStop?: boolean;
  // Reports a failed action to App's toast stack.
  onError?: (fallback: string, err: unknown) => void;
  // Re-fetches the VM list. Called after a snapshot restore or network change,
  // since App owns the `vm` prop.
  onRefreshVMs: () => void;
  // Reports a successful clone or template save to App's toast stack.
  onNotice?: (message: string) => void;
  // Opens the service this VM belongs to, when it belongs to one.
  onOpenService?: (name: string) => void;
  // That service, for the banner saying what the VM is to it.
  service?: Service;
}

type Tab = 'overview' | 'storage' | 'network' | 'hardware' | 'console';

export function VMDetail({
  vm,
  leases,
  bridges,
  uplink,
  onBack,
  onStart,
  onStop,
  onForceStop,
  onDelete,
  canForceStop,
  onError,
  onRefreshVMs,
  onNotice,
  onOpenService,
  service,
}: VMDetailProps) {
  const caps = useCapabilities();
  // Hardware is passthrough and CPU tuning; a backend with neither has nothing
  // to show there.
  const hasHardwareTab = caps.passthrough || caps.cpu_tuning;
  const [tab, setTab] = useState<Tab>('overview');
  const [copyDialog, setCopyDialog] = useState<'clone' | 'template' | null>(null);

  const running = vm.status === 'running';
  // App overlays this phase while a graceful shutdown it issued is still
  // outstanding; libvirt itself has no such state to report.
  const stopping = vm.phase === 'stopping';
  const lease = leases.find(l => l.vm_name === vm.name);
  // vm.ip wins. The backend resolves it ARP → guest agent → lease, and only the
  // first two prove the guest answered; a lease survives a host power cut. The
  // lease here is only a fallback for a VM libvirt has no live answer for.
  const ip = vm.ip || lease?.ip || '—';
  const progress = bootProgress(vm);
  const rail = railSteps(vm);
  const { stats, history } = useVMStats(vm.id, running, tab === 'overview');

  const tabs: TabItem<Tab>[] = [
    { id: 'overview', label: 'Overview' },
    { id: 'storage', label: 'Storage' },
    { id: 'network', label: 'Network' },
    ...(hasHardwareTab ? [{ id: 'hardware' as const, label: 'Hardware' }] : []),
    { id: 'console', label: 'Console' },
  ];

  return (
    <div style={{ padding: '22px clamp(16px, 3vw, 40px) 60px', maxWidth: 1180, width: '100%' }}>
      <button
        onClick={onBack}
        style={{
          background: 'none',
          border: 'none',
          color: 'var(--nd-text-2)',
          fontSize: 13,
          cursor: 'pointer',
          padding: 0,
          display: 'flex',
          alignItems: 'center',
          gap: 6,
          marginBottom: 18,
        }}
      >
        ← Virtual machines
      </button>

      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 14, marginBottom: 24 }}>
        <OsChip os={vm.os} size={48} />
        <div style={{ flex: '1 1 260px', minWidth: 0 }}>
          <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 11 }}>
            <h1 style={{ margin: 0, fontSize: 26, fontWeight: 650, letterSpacing: '-0.025em' }}>{vm.name}</h1>
            <StatusBadge status={vm.status} phase={vm.phase} />
          </div>
          <div style={{ fontFamily: 'var(--nd-font-mono)', fontSize: 12.5, color: 'var(--nd-muted)', marginTop: 3 }}>
            {getOS(vm.os).label} · {ip}
          </div>

          {(rail || progress) && (
            <div style={{ marginTop: 12, maxWidth: 420, display: 'flex', flexDirection: 'column', gap: 8 }}>
              {rail && <BootRail steps={rail} />}
              {progress && <BootProgressLine phase={vm.phase} text={progress} />}
            </div>
          )}
        </div>
        <VMActions
          vm={vm}
          stopping={stopping}
          canForceStop={canForceStop}
          onStart={onStart}
          onStop={onStop}
          onForceStop={onForceStop}
          onDelete={onDelete}
          onClone={() => setCopyDialog('clone')}
          onSaveTemplate={() => setCopyDialog('template')}
        />
      </div>

      {copyDialog === 'clone' && (
        <CloneVMModal
          vmId={vm.id}
          onClose={() => setCopyDialog(null)}
          onCloned={(name, warnings) => {
            setCopyDialog(null);
            onRefreshVMs();
            onNotice?.([`Cloned ${vm.id} to ${name}.`, ...warnings].join(' '));
          }}
        />
      )}
      {copyDialog === 'template' && (
        <SaveTemplateModal
          vmId={vm.id}
          onClose={() => setCopyDialog(null)}
          onSaved={name => {
            setCopyDialog(null);
            onNotice?.(`Saved template "${name}" — pick it in the create wizard to start from it.`);
          }}
        />
      )}

      {vm.service && <ServiceBanner vm={vm} service={service} onOpenService={onOpenService} />}

      <Tabs label="VM sections" items={tabs} value={tab} onChange={setTab}>
        <div style={{ paddingTop: 26 }}>
          {tab === 'overview' && (
            <OverviewTab
              vm={vm}
              ip={ip}
              stats={stats}
              history={history}
              reportsStats={caps.vm_stats}
              hypervisor={caps.backend === 'vz' ? 'Apple Virtualization' : 'QEMU/KVM'}
            />
          )}
          {tab === 'storage' && <StorageTab vm={vm} snapshotsSupported={caps.snapshots} onError={onError} onRefreshVMs={onRefreshVMs} />}
          {tab === 'network' && (
            <NetworkTab vm={vm} ip={ip} bridges={bridges} uplink={uplink} onError={onError} onRefreshVMs={onRefreshVMs} />
          )}
          {tab === 'hardware' && hasHardwareTab && <HardwareTab vmId={vm.id} vcpus={vm.cpu_count} onError={onError} />}
          {tab === 'console' && (
            <ConsoleTab vmId={vm.id} name={vm.name} running={running} serial={caps.serial_console && !caps.vnc_console} />
          )}
        </div>
      </Tabs>
    </div>
  );
}
