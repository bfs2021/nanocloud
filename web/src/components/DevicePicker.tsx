import { useState } from 'react';
import type { HostDeviceReport, PCIDevice, USBDevice } from '../types';

// The selection counterpart to HostDevices' readout in Settings: that one
// answers "can this host pass a device through", this one "which one, for this
// VM". A device the host can't release is listed but not selectable, with its
// reason, and a PCI pick brings its IOMMU-group siblings, so the saved count
// can exceed the boxes ticked.

export interface DeviceSelection {
  pci: string[];
  usb: string[];
}

const codeChip: React.CSSProperties = {
  fontFamily: 'var(--nd-font-mono)',
  fontSize: 12.5,
  color: 'var(--nd-text)',
  background: 'var(--nd-surface-2)',
  padding: '1px 6px',
  borderRadius: 5,
};

const sectionLabel: React.CSSProperties = {
  fontSize: 11,
  fontWeight: 600,
  color: 'var(--nd-text-2)',
  letterSpacing: '0.06em',
  textTransform: 'uppercase',
  fontFamily: 'var(--nd-font-mono)',
  margin: '0 0 6px',
};

const quiet: React.CSSProperties = { fontSize: 12.5, color: 'var(--nd-muted)', padding: '8px 0' };

function usbKey(d: USBDevice): string {
  return `${d.vendor_id}:${d.product_id}`;
}

// A row is a label so the whole thing is a click target, not just the 13px box.
function DeviceRow({
  checked,
  disabled,
  onToggle,
  code,
  title,
  meta,
  notes,
}: {
  checked: boolean;
  disabled?: boolean;
  onToggle: () => void;
  code: string;
  title: string;
  meta?: string;
  notes?: React.ReactNode;
}) {
  return (
    <label
      style={{
        display: 'flex',
        alignItems: 'flex-start',
        gap: 10,
        padding: '9px 0',
        borderBottom: '1px solid var(--nd-line-soft)',
        cursor: disabled ? 'not-allowed' : 'pointer',
        opacity: disabled ? 0.55 : 1,
      }}
    >
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={onToggle}
        style={{ marginTop: 3, cursor: disabled ? 'not-allowed' : 'pointer' }}
      />
      <span style={{ minWidth: 0, flex: 1 }}>
        <span style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          <code style={codeChip}>{code}</code>
          <span style={{ fontSize: 13, color: 'var(--nd-text)' }}>{title}</span>
        </span>
        {meta && <span style={{ display: 'block', fontSize: 11.5, color: 'var(--nd-muted)', marginTop: 2 }}>{meta}</span>}
        {notes}
      </span>
    </label>
  );
}

function RowNote({ tone, children }: { tone: 'warn' | 'muted'; children: React.ReactNode }) {
  const palette =
    tone === 'warn'
      ? { bg: 'var(--nd-warn-bg)', border: 'var(--nd-warn-line)', fg: 'var(--nd-warn)' }
      : { bg: 'var(--nd-surface-2)', border: 'var(--nd-line)', fg: 'var(--nd-text-2)' };
  return (
    <span
      style={{
        display: 'block',
        marginTop: 5,
        background: palette.bg,
        border: `1px solid ${palette.border}`,
        borderRadius: 6,
        padding: '5px 9px',
        fontSize: 11.5,
        lineHeight: 1.5,
        color: palette.fg,
      }}
    >
      {children}
    </span>
  );
}

// The picker exists so a GPU can be chosen from a list, so falling straight
// through to the class defeats it: two cards both read "VGA compatible
// controller". The numeric IDs sit in between — not friendly, but unique.
function pciTitle(d: PCIDevice): string {
  if (d.product) return d.product;
  if (d.vendor_id && d.product_id) return `${d.class || 'PCI device'} [${d.vendor_id}:${d.product_id}]`;
  return d.class || 'Unrecognised device';
}

export function DevicePicker({
  report,
  selection,
  onChange,
  disabled,
}: {
  report: HostDeviceReport | null;
  selection: DeviceSelection;
  onChange: (next: DeviceSelection) => void;
  disabled?: boolean;
}) {
  // A real host reports dozens of PCI functions, mostly chipset plumbing. As in
  // the Settings readout, the ones that answer the question come first.
  const [showAll, setShowAll] = useState(false);

  if (!report) {
    return <p style={quiet}>Reading the host's device inventory…</p>;
  }

  const toggle = (kind: 'pci' | 'usb', key: string) => {
    const current = selection[kind];
    const next = current.includes(key) ? current.filter(k => k !== key) : [...current, key];
    onChange({ ...selection, [kind]: next });
  };

  const answers = report.pci.filter(d => d.usable || d.boot_vga || selection.pci.includes(d.address));
  const shownPCI = showAll ? report.pci : answers;
  const withheld = report.pci.length - answers.length;

  return (
    <div>
      <p style={sectionLabel}>PCI devices</p>

      {/* Without IOMMU nothing below can be saved, so say it once here rather
          than on every row. */}
      {!report.iommu.enabled && (
        <RowNote tone="warn">
          {report.iommu.message}. PCI passthrough is unavailable until this is fixed; USB devices below are unaffected.
        </RowNote>
      )}

      {report.pci.length === 0 && <p style={quiet}>No PCI devices reported for this host.</p>}

      {shownPCI.map(d => (
        <DeviceRow
          key={d.address}
          checked={selection.pci.includes(d.address)}
          disabled={disabled || !d.usable || !report.iommu.enabled}
          onToggle={() => toggle('pci', d.address)}
          code={d.address}
          title={pciTitle(d)}
          meta={[d.vendor, d.driver ? `bound to ${d.driver}` : 'no driver bound'].filter(Boolean).join(' · ')}
          notes={
            <>
              {d.reason && (
                <RowNote tone="warn">
                  <strong>Why not:</strong> {d.reason}
                </RowNote>
              )}
              {d.boot_vga && (
                <RowNote tone="warn">
                  This card is driving the host's own display. Passing it through takes the console with it — you keep network access, but
                  not the screen you are reading this on.
                </RowNote>
              )}
              {d.siblings.length > 0 && (
                <RowNote tone="muted">
                  Selecting this also attaches {d.siblings.length} other device
                  {d.siblings.length === 1 ? '' : 's'} in IOMMU group {d.iommu_group} — a group goes to the guest whole or not at all:{' '}
                  {d.siblings.join(', ')}
                </RowNote>
              )}
            </>
          }
        />
      ))}

      {withheld > 0 && !showAll && (
        <button
          type="button"
          onClick={() => setShowAll(true)}
          style={{
            marginTop: 8,
            background: 'none',
            border: 'none',
            padding: 0,
            fontSize: 12.5,
            color: 'var(--nd-text-2)',
            cursor: 'pointer',
            textDecoration: 'underline',
          }}
        >
          Show {withheld} device{withheld === 1 ? '' : 's'} this host cannot release
        </button>
      )}

      <p style={{ ...sectionLabel, marginTop: 18 }}>USB devices</p>
      {report.usb.length === 0 && <p style={quiet}>No USB devices plugged into this host.</p>}
      {report.usb.map(d => (
        <DeviceRow
          key={usbKey(d)}
          checked={selection.usb.includes(usbKey(d))}
          disabled={disabled}
          onToggle={() => toggle('usb', usbKey(d))}
          code={usbKey(d)}
          title={d.description || 'Unnamed USB device'}
          // Matched on vendor:product, never on the bus address, so that the
          // device is still the right one after it is unplugged and replugged.
          meta={`Seen at ${d.bus_seen}`}
        />
      ))}
    </div>
  );
}
