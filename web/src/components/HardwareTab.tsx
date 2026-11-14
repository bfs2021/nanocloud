import { useState, useEffect, useCallback } from 'react';
import type { CPUShape, HostDeviceReport, VMSpec } from '../types';
import { getHostDevices, getSystemInfo, getVMSpec, patchVMSpec } from '../api';
import { DevicePicker, type DeviceSelection } from './DevicePicker';
import { CPUShapePicker, cpuSetError, topologyError } from './CPUShapePicker';
import { surfaceCard } from '../design';

// The Hardware tab edits what a VM is made of. A change rebuilds the domain and
// reaches the guest at its next boot, so the tab says so until the VM restarts.
// `restart_required` comes from the backend, so the badge survives a reload.

const card: React.CSSProperties = {
  ...surfaceCard,
  padding: '18px 20px',
};

const heading: React.CSSProperties = { fontSize: 14, fontWeight: 600, color: 'var(--nd-text)', margin: 0 };

const subtle: React.CSSProperties = { fontSize: 12.5, color: 'var(--nd-muted)', margin: '4px 0 0', lineHeight: 1.5 };

// selectionOf reads the picker's state out of a saved spec, so re-opening the
// tab shows what the VM actually has rather than what was last clicked.
function selectionOf(spec: VMSpec): DeviceSelection {
  return {
    pci: spec.pci_devices.map(d => d.address),
    usb: spec.usb_devices.map(d => `${d.vendor_id}:${d.product_id}`),
  };
}

function sameSelection(a: DeviceSelection, b: DeviceSelection): boolean {
  const eq = (x: string[], y: string[]) => x.length === y.length && [...x].sort().every((v, i) => v === [...y].sort()[i]);
  return eq(a.pci, b.pci) && eq(a.usb, b.usb);
}

function sameCPU(a: CPUShape, b: CPUShape): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

// Exported so the Network tab's restart notice is the same notice, not a
// second one that drifts from it.
export function Banner({ tone, children }: { tone: 'info' | 'warn'; children: React.ReactNode }) {
  const palette =
    tone === 'warn'
      ? { bg: 'var(--nd-warn-bg)', border: 'var(--nd-warn-line)', fg: 'var(--nd-warn)' }
      : { bg: 'var(--nd-accent-soft)', border: 'var(--nd-accent-line)', fg: 'var(--nd-accent)' };
  return (
    <div
      style={{
        background: palette.bg,
        border: `1px solid ${palette.border}`,
        borderRadius: 8,
        padding: '10px 14px',
        fontSize: 12.5,
        lineHeight: 1.55,
        color: palette.fg,
      }}
    >
      {children}
    </div>
  );
}

// ApplyRow is the save control for a tab that edits the spec. Shared with the
// Network tab so the two cannot drift into disagreeing about what a pending
// change looks like. `blocked` is separate from `dirty` on purpose: a change
// the backend would refuse must not be savable, but must still be discardable.
export function ApplyRow({
  dirty,
  saving,
  blocked,
  onApply,
  onDiscard,
}: {
  dirty: boolean;
  saving: boolean;
  blocked?: boolean;
  onApply: () => void;
  onDiscard: () => void;
}) {
  const on = dirty && !saving && !blocked;
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
      <button
        onClick={onApply}
        disabled={!on}
        style={{
          background: on ? 'var(--nd-accent)' : 'var(--nd-track)',
          color: on ? 'var(--nd-on-accent)' : 'var(--nd-muted)',
          border: 'none',
          borderRadius: 8,
          padding: '8px 16px',
          fontSize: 13,
          fontWeight: 500,
          cursor: on ? 'pointer' : 'default',
        }}
      >
        {saving ? 'Applying…' : 'Apply changes'}
      </button>
      {dirty && !saving && (
        <button
          onClick={onDiscard}
          style={{
            background: 'none',
            border: 'none',
            fontSize: 12.5,
            color: 'var(--nd-text-2)',
            cursor: 'pointer',
            textDecoration: 'underline',
            padding: 0,
          }}
        >
          Discard
        </button>
      )}
    </div>
  );
}

export function HardwareTab({
  vmId,
  vcpus,
  onError,
}: {
  vmId: string;
  // The VM's vCPU count, which a fixed topology has to multiply out to.
  vcpus: number;
  // Matches VMDetail's own handler: the caller supplies the sentence, this
  // component supplies the cause, and the toast composes both.
  onError?: (fallback: string, err: unknown) => void;
}) {
  const [report, setReport] = useState<HostDeviceReport | null>(null);
  const [spec, setSpec] = useState<VMSpec | null>(null);
  const [selection, setSelection] = useState<DeviceSelection>({ pci: [], usb: [] });
  const [cpu, setCPU] = useState<CPUShape | null>(null);
  const [hostCPUs, setHostCPUs] = useState(0);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    try {
      const [devices, current] = await Promise.all([getHostDevices(), getVMSpec(vmId)]);
      setReport(devices);
      setSpec(current);
      setSelection(selectionOf(current));
      setCPU(current.cpu);
    } catch (e) {
      onError?.('Could not read this VM’s hardware', e);
    }
    // Separately, and swallowed: the host's CPU count only sharpens the pinning
    // hint. Failing the whole tab over it would hide the devices too.
    try {
      setHostCPUs((await getSystemInfo()).cpus);
    } catch {
      setHostCPUs(0);
    }
  }, [vmId, onError]);

  useEffect(() => {
    load();
  }, [load]);

  const save = async () => {
    setSaving(true);
    try {
      // Both lists are always sent: an omitted one means "leave it alone",
      // which would make clearing every device impossible from this screen.
      const next = await patchVMSpec(vmId, {
        pci_devices: selection.pci,
        usb_devices: selection.usb,
        ...(cpu ? { cpu } : {}),
      });
      setSpec(next);
      // Re-seeded from the response, not from `selection`: the backend expands
      // a PCI pick to its whole IOMMU group, so the two legitimately differ.
      setSelection(selectionOf(next));
      setCPU(next.cpu);
    } catch (e) {
      onError?.('Could not apply the hardware change', e);
    } finally {
      setSaving(false);
    }
  };

  // A shape the backend would refuse can't be saved; the reason shows under
  // the field instead of in a toast.
  const cpuInvalid = cpu !== null && (topologyError(cpu, vcpus) !== null || cpuSetError(cpu.cpu_set, hostCPUs) !== null);

  const dirty = spec !== null && cpu !== null && (!sameSelection(selection, selectionOf(spec)) || !sameCPU(cpu, spec.cpu));

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
      {spec?.restart_required && (
        <Banner tone="warn">
          <strong>Restart to apply.</strong> This VM’s recorded hardware differs from the machine that is running. Hardware changes are made
          by rebuilding the VM’s definition, which the guest only picks up when it next boots — stop it and start it again.
        </Banner>
      )}

      {spec?.warnings.map(w => (
        <Banner key={w} tone="warn">
          {w}
        </Banner>
      ))}

      {/* CPU first: it describes every VM, where passthrough is the thing a
          small minority need. */}
      <div style={card}>
        <h3 style={heading}>CPU</h3>
        <p style={subtle}>What the guest sees as its processor, and which of this host's CPUs it may run on.</p>

        <div style={{ marginTop: 16 }}>
          {cpu && <CPUShapePicker value={cpu} vcpus={vcpus} hostCPUs={hostCPUs} disabled={saving} onChange={setCPU} />}
        </div>
      </div>

      <div style={card}>
        <h3 style={heading}>Passthrough devices</h3>
        <p style={subtle}>
          Hand a whole PCI or USB device from this host to the guest. Devices are assigned entire — a GPU cannot be split between the host
          and a VM, or between two VMs.
        </p>

        <div style={{ marginTop: 16 }}>
          <DevicePicker report={report} selection={selection} onChange={setSelection} disabled={saving} />
        </div>
      </div>

      {/* One action row for the whole tab: both cards are edits to the same
          spec and are saved by the same PATCH, so two buttons would imply two
          independent rebuilds. */}
      <ApplyRow
        dirty={dirty}
        saving={saving}
        blocked={cpuInvalid}
        onApply={save}
        onDiscard={() => {
          if (!spec) return;
          setSelection(selectionOf(spec));
          setCPU(spec.cpu);
        }}
      />
    </div>
  );
}
