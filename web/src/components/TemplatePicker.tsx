import { useEffect, useState } from 'react';
import type { VMTemplate } from '../types';
import { listTemplates } from '../api';

// TemplatePicker sits above the image grid because a template answers that
// question and every one after it. Picking one fills all three steps, and the
// fields stay editable. It renders nothing when no templates exist.
export function TemplatePicker({ selected, onSelect }: { selected: string; onSelect: (t: VMTemplate | null) => void }) {
  const [templates, setTemplates] = useState<VMTemplate[]>([]);

  useEffect(() => {
    // A failure is silent on purpose: templates are an accelerator, and a
    // daemon that cannot list them must not stop anyone creating a VM.
    listTemplates()
      .then(setTemplates)
      .catch(() => {});
  }, []);

  if (templates.length === 0) return null;

  const current = templates.find(t => t.name === selected);

  return (
    <div
      style={{
        background: 'var(--nd-surface)',
        border: '1px solid var(--nd-line)',
        borderRadius: 11,
        padding: '13px 15px',
        marginBottom: 20,
      }}
    >
      <label htmlFor="template-picker" style={{ display: 'block', fontSize: 13.5, fontWeight: 500, marginBottom: 5 }}>
        Start from a template
      </label>
      <select
        id="template-picker"
        value={selected}
        onChange={e => onSelect(templates.find(t => t.name === e.target.value) ?? null)}
        style={{
          width: '100%',
          padding: '8px 10px',
          fontSize: 13.5,
          border: '1px solid var(--nd-line)',
          borderRadius: 8,
          background: 'var(--nd-surface)',
          outline: 'none',
        }}
      >
        <option value="">Start from scratch</option>
        {templates.map(t => (
          <option key={t.name} value={t.name}>
            {t.name}
          </option>
        ))}
      </select>

      {current && (
        <div style={{ marginTop: 9, fontSize: 12, color: 'var(--nd-muted)', lineHeight: 1.55 }}>
          {summarise(current)}. Everything below is filled in and still editable — you will be asked for a login password, which is never
          saved.
        </div>
      )}
    </div>
  );
}

// summarise names what the template will fill in.
function summarise(t: VMTemplate): string {
  const { spec } = t;
  const parts = [
    `${spec.vcpus} vCPU`,
    `${Math.round(spec.memory_mb / 1024)} GB RAM`,
    `${spec.disk_gb} GB disk`,
    spec.network_mode === 'bridge' ? 'bridge' : 'NAT',
  ];
  if (spec.firmware === 'uefi') parts.push(spec.secure_boot ? 'UEFI + secure boot' : 'UEFI');
  if (spec.tpm) parts.push('TPM');
  const devices = spec.pci_devices.length + spec.usb_devices.length;
  if (devices > 0) parts.push(`${devices} host device${devices === 1 ? '' : 's'}`);
  if (spec.cloud_init.packages.length > 0) parts.push(`${spec.cloud_init.packages.length} packages`);
  return parts.join(' · ');
}
