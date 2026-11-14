import { useState, useEffect } from 'react';
import type { HostDeviceReport, IOMMUStatus, PCIDevice, USBDevice } from '../types';
import { getHostDevices } from '../api';
import { RemedySnippet } from './RemedySnippet';
import { CheckBadge } from './CheckBadge';
import { Spinner } from '../ui/Spinner';

// The Settings section that answers: can this host hand a device to a VM, and
// if not, how to fix it. Each row leads with a verdict and its reason; the raw
// addresses come second.

const codeChip: React.CSSProperties = {
  fontFamily: 'var(--nd-font-mono)',
  fontSize: 13,
  color: 'var(--nd-text)',
  background: 'var(--nd-surface-2)',
  padding: '1px 6px',
  borderRadius: 5,
  letterSpacing: '0.01em',
};

const subHeading: React.CSSProperties = {
  fontSize: 11.5,
  fontWeight: 600,
  color: 'var(--nd-text-2)',
  margin: '0 0 2px',
  letterSpacing: '0.06em',
  textTransform: 'uppercase',
  fontFamily: 'var(--nd-font-mono)',
};

const quietText: React.CSSProperties = { fontSize: 13, color: 'var(--nd-muted)', padding: '10px 0' };

// Note is the per-device explanation box. It borrows RemedySnippet's geometry
// so a reason, a warning and a remedy all read as the same kind of aside.
function Note({ tone, children }: { tone: 'warn' | 'muted'; children: React.ReactNode }) {
  const palette =
    tone === 'warn'
      ? { bg: 'var(--nd-warn-bg)', border: 'var(--nd-warn-line)', fg: 'var(--nd-warn)' }
      : { bg: 'var(--nd-surface-2)', border: 'var(--nd-line)', fg: 'var(--nd-text-2)' };

  return (
    <div
      style={{
        marginTop: 6,
        background: palette.bg,
        border: `1px solid ${palette.border}`,
        borderRadius: 6,
        padding: '6px 10px',
        fontSize: 12,
        lineHeight: 1.5,
        color: palette.fg,
      }}
    >
      {children}
    </div>
  );
}

// The headline state. IOMMU off is the most common reason passthrough is
// impossible, and the fix is a kernel argument plus a reboot.
function IOMMUHeadline({ iommu }: { iommu: IOMMUStatus }) {
  const on = iommu.enabled;
  const palette = on
    ? { bg: 'var(--nd-ready-bg)', border: 'var(--nd-ready-line)', fg: 'var(--nd-ready)' }
    : { bg: 'var(--nd-danger-bg)', border: 'var(--nd-danger-line)', fg: 'var(--nd-danger)' };

  return (
    <div
      style={{
        background: palette.bg,
        border: `1px solid ${palette.border}`,
        borderRadius: 8,
        padding: '14px 16px',
        marginBottom: 20,
        display: 'flex',
        alignItems: 'flex-start',
        gap: 10,
      }}
    >
      <svg width="16" height="16" viewBox="0 0 15 15" fill="none" style={{ flexShrink: 0, marginTop: 2 }}>
        <circle cx="7.5" cy="7.5" r="6.5" style={{ stroke: palette.fg }} strokeWidth="1.3" />
        {on ? (
          <path d="M4.6 7.7l2 2 3.8-4" style={{ stroke: palette.fg }} strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
        ) : (
          <>
            <path d="M7.5 4.5v3.5" style={{ stroke: palette.fg }} strokeWidth="1.4" strokeLinecap="round" />
            <circle cx="7.5" cy="10.5" r="0.8" style={{ fill: palette.fg }} />
          </>
        )}
      </svg>

      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 14, fontWeight: 600, color: palette.fg }}>{iommu.message}</div>
        {!on && (
          <div style={{ fontSize: 12.5, color: 'var(--nd-danger)', marginTop: 4 }}>
            The kernel can only isolate a device for a guest once the IOMMU is on, so nothing on this host can be passed through until it
            is. This takes a kernel argument and a reboot.
          </div>
        )}
        {iommu.remedy && <RemedySnippet command={iommu.remedy} tone={on ? 'neutral' : 'danger'} />}
      </div>
    </div>
  );
}

// title falls back through what libvirt actually filled in: <product> is
// routinely blank on bridges and chipset functions, where the class is the
// only name there is.
function title(dev: PCIDevice): string {
  return dev.product || numericID(dev) || dev.class || 'Unrecognised device';
}

// numericID is the last identity a device has before it becomes just its
// class. Two GPUs both read "VGA compatible controller", so a title that falls
// straight through to the class cannot be used to pick between them.
function numericID(dev: PCIDevice): string {
  return dev.vendor_id && dev.product_id ? `[${dev.vendor_id}:${dev.product_id}]` : '';
}

// meta renders the facts that qualify a device. Absent values are rendered as
// their meaning, never as "" or -1.
function meta(dev: PCIDevice): string {
  return [
    dev.vendor,
    // The class is dropped when it is already serving as the title, and the
    // numeric ID when the title fell back to it.
    dev.product ? dev.class : numericID(dev) ? dev.class : '',
    dev.driver ? `bound to ${dev.driver}` : 'no driver bound',
    dev.iommu_group >= 0 ? `IOMMU group ${dev.iommu_group}` : 'IOMMU group unknown',
  ]
    .filter(Boolean)
    .join(' · ');
}

// IOMMU group members move as a unit, so lead with that; the addresses follow
// so the operator knows what else leaves the host. A non-empty `siblings`
// implies a real group number.
function siblingConsequence(dev: PCIDevice): string {
  const n = dev.siblings.length;
  return `Moves together with ${n} other device${n === 1 ? '' : 's'} in IOMMU group ${dev.iommu_group} — the whole group goes to the guest or none of it does: ${dev.siblings.join(', ')}`;
}

function PCIDeviceRow({ dev, isLast }: { dev: PCIDevice; isLast: boolean }) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'flex-start',
        justifyContent: 'space-between',
        gap: 16,
        padding: '14px 0',
        borderBottom: isLast ? 'none' : '1px solid var(--nd-line-soft)',
      }}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0, flex: 1 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          <code style={codeChip}>{dev.address}</code>
          <span style={{ fontSize: 13.5, color: 'var(--nd-text)', fontWeight: 500 }}>{title(dev)}</span>
        </div>
        <div style={{ fontSize: 12, color: 'var(--nd-muted)', marginTop: 2 }}>{meta(dev)}</div>

        {/* The backend writes the reason to follow a lead-in, so it is rendered as
            is: capitalising would mangle a reason opening on `vfio-pci`. */}
        {dev.reason && (
          <Note tone="warn">
            <strong>Why:</strong> {dev.reason}
          </Note>
        )}

        {/* Orthogonal to `usable`: this card can be entirely passable and
            still be the wrong one to pass, so it gets its own sentence rather
            than folding into the badge. */}
        {dev.boot_vga && (
          <Note tone="warn">
            This card is driving the host's own display. Passing it through takes the console with it — single-GPU passthrough works, but
            you lose the screen you are reading this on and can only get back in over the network.
          </Note>
        )}

        {dev.siblings.length > 0 && <Note tone="muted">{siblingConsequence(dev)}</Note>}
      </div>

      <div style={{ flexShrink: 0, marginTop: 1 }}>
        {/* "Not recommended" rather than "Not passable": the strongest reason
            the backend gives is a risk ("probably carries the host's own
            disks"), not an impossibility, and the operator makes that call. */}
        <CheckBadge variant={dev.usable ? 'ok' : 'warn'} label={dev.usable ? 'Passable' : 'Not recommended'} />
      </div>
    </div>
  );
}

function USBDeviceRow({ dev, isLast }: { dev: USBDevice; isLast: boolean }) {
  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 3,
        padding: '11px 0',
        borderBottom: isLast ? 'none' : '1px solid var(--nd-line-soft)',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <code style={codeChip}>{`${dev.vendor_id}:${dev.product_id}`}</code>
        <span style={{ fontSize: 13, color: 'var(--nd-text)' }}>{dev.description || 'Unnamed USB device'}</span>
      </div>
      {/* An observation, never the handle you would select — the caveat is
          stated once for the whole section rather than on every row. */}
      <div style={{ fontSize: 11.5, color: 'var(--nd-muted)' }}>Seen at {dev.bus_seen}</div>
    </div>
  );
}

// A real host reports dozens of PCI functions, nearly all chipset plumbing.
// The count leads, then only the passable devices and boot-VGA cards; the
// rest stay one click away with their reasons.
function PCISection({ devices }: { devices: PCIDevice[] }) {
  const [showAll, setShowAll] = useState(false);

  if (devices.length === 0) {
    return <p style={quietText}>No PCI devices reported for this host.</p>;
  }

  const passable = devices.filter(d => d.usable).length;
  const answers = devices.filter(d => d.usable || d.boot_vga);
  const withheld = devices.length - answers.length;
  const shown = showAll ? devices : answers;

  return (
    <>
      <p style={{ fontSize: 13, color: 'var(--nd-text)', margin: '0 0 2px' }}>
        <strong style={{ fontWeight: 600 }}>{passable}</strong> of {devices.length} PCI device
        {devices.length === 1 ? '' : 's'} on this host can be passed through.
      </p>

      {shown.map((dev, i) => (
        <PCIDeviceRow key={dev.address} dev={dev} isLast={i === shown.length - 1} />
      ))}

      {withheld > 0 && (
        <button
          onClick={() => setShowAll(v => !v)}
          aria-expanded={showAll}
          style={{
            marginTop: shown.length > 0 ? 12 : 4,
            background: 'none',
            border: 'none',
            padding: 0,
            color: 'var(--nd-accent)',
            fontSize: 12.5,
            fontWeight: 600,
            cursor: 'pointer',
          }}
        >
          {showAll ? '▾ ' : '▸ '}
          {showAll ? 'Hide' : 'Show'} the other {withheld} device{withheld === 1 ? '' : 's'}
        </button>
      )}
    </>
  );
}

export function HostDevices() {
  const [report, setReport] = useState<HostDeviceReport | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    getHostDevices()
      .then(data => {
        if (!cancelled) setReport(data);
      })
      .catch(() => {
        // No message from the error: its only cause is an unreachable libvirt,
        // which the readiness checks above already report.
        if (!cancelled) setUnavailable(true);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <section className="nd-card">
      <h2 className="nd-card-title">Passthrough devices</h2>
      <p className="nd-card-text" style={{ marginBottom: 16 }}>
        Whether this host can hand a physical device straight to a VM, and which devices are candidates. Read-only.
      </p>

      {loading && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 9, color: 'var(--nd-muted)', fontSize: 13.5, padding: '12px 0' }}>
          <Spinner size={15} shrink />
          Reading host devices…
        </div>
      )}

      {!loading && unavailable && <p style={quietText}>Couldn't read this host's devices.</p>}

      {!loading && report && (
        <>
          <IOMMUHeadline iommu={report.iommu} />

          <div style={{ marginBottom: 22 }}>
            <h3 style={subHeading}>PCI devices</h3>
            <PCISection devices={report.pci} />
          </div>

          {/* Quieter than PCI: USB passthrough needs no IOMMU and works on any host. */}
          <div>
            <h3 style={subHeading}>USB devices</h3>
            {report.usb.length === 0 ? (
              <p style={quietText}>No USB devices reported for this host.</p>
            ) : (
              <>
                <p style={{ fontSize: 12, color: 'var(--nd-muted)', margin: '0 0 4px' }}>
                  Identified by vendor and product ID — bus and device numbers are reassigned on every replug.
                </p>
                {report.usb.map((dev, i) => (
                  <USBDeviceRow key={`${dev.vendor_id}:${dev.product_id}:${dev.bus_seen}`} dev={dev} isLast={i === report.usb.length - 1} />
                ))}
              </>
            )}
          </div>
        </>
      )}
    </section>
  );
}
