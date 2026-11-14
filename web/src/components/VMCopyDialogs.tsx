import { useState, type ReactNode } from 'react';
import { cloneVM, saveTemplate } from '../api';

// The two ways to make something from a VM you already have. They share a
// shell because they ask the same question — "call it what?" — and differ only
// in what they copy: a clone takes the disks, a template takes the shape.

// A VM name is a path component before libvirt ever sees it: the qcow2 and the
// seed ISO are written from it. The wizard's rule, mirrored here for the same
// reason it exists there.
const VM_NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$/;
const VM_NAME_RULE = 'Start with a letter or digit; letters, digits, dots, dashes and underscores only.';

const input: React.CSSProperties = {
  width: '100%',
  padding: '9px 11px',
  fontSize: 13.5,
  border: '1px solid var(--nd-line)',
  borderRadius: 8,
  outline: 'none',
  boxSizing: 'border-box',
};

function Dialog({
  title,
  subtitle,
  children,
  confirmLabel,
  busyLabel,
  busy,
  disabled,
  error,
  onConfirm,
  onClose,
}: {
  title: string;
  subtitle: string;
  children: ReactNode;
  confirmLabel: string;
  busyLabel: string;
  busy: boolean;
  disabled: boolean;
  error: string;
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        background: 'var(--nd-scrim)',
        zIndex: 40,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 32,
      }}
      onClick={() => {
        if (!busy) onClose();
      }}
    >
      <div
        onClick={e => e.stopPropagation()}
        style={{
          width: 460,
          maxWidth: '100%',
          background: 'var(--nd-surface)',
          borderRadius: 14,
          padding: 22,
          boxShadow: 'var(--nd-shadow-lg)',
        }}
      >
        <h2 style={{ margin: 0, fontSize: 16, fontWeight: 600 }}>{title}</h2>
        <p style={{ margin: '6px 0 16px', fontSize: 12.5, color: 'var(--nd-muted)', lineHeight: 1.55 }}>{subtitle}</p>

        {children}

        {error && (
          <div
            style={{
              marginTop: 12,
              background: 'var(--nd-danger-bg)',
              border: '1px solid var(--nd-danger-line)',
              borderRadius: 8,
              padding: '8px 11px',
              fontSize: 12.5,
              lineHeight: 1.5,
              color: 'var(--nd-danger)',
            }}
          >
            {error}
          </div>
        )}

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 18 }}>
          <button
            onClick={onClose}
            disabled={busy}
            style={{
              background: 'var(--nd-surface)',
              border: '1px solid var(--nd-line)',
              borderRadius: 8,
              padding: '8px 15px',
              fontSize: 13,
              color: busy ? 'var(--nd-muted)' : 'var(--nd-text)',
              cursor: busy ? 'not-allowed' : 'pointer',
            }}
          >
            Cancel
          </button>
          <button
            onClick={onConfirm}
            disabled={busy || disabled}
            style={{
              background: busy || disabled ? 'var(--nd-accent-line)' : 'var(--nd-accent)',
              border: 'none',
              borderRadius: 8,
              padding: '8px 15px',
              fontSize: 13,
              fontWeight: 600,
              color: 'var(--nd-on-accent)',
              cursor: busy || disabled ? 'not-allowed' : 'pointer',
            }}
          >
            {busy ? busyLabel : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}

// CloneVMModal copies a stopped VM, disks and all. The machine-id and SSH host
// key caveat is shown before the copy, because its fix (`cloud-init clean` in
// the source) only helps before then.
export function CloneVMModal({
  vmId,
  onClose,
  onCloned,
}: {
  vmId: string;
  onClose: () => void;
  onCloned: (name: string, warnings: string[]) => void;
}) {
  const [name, setName] = useState(`${vmId}-copy`.slice(0, 63));
  const [start, setStart] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const invalid = !VM_NAME_RE.test(name);

  async function submit() {
    setBusy(true);
    setError('');
    try {
      const res = await cloneVM(vmId, name, start);
      onCloned(res.name, res.warnings ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  }

  return (
    <Dialog
      title={`Clone ${vmId}`}
      subtitle="Every disk is copied in full, so the clone keeps working if you delete the original. That takes a while — a copy is the size of what the VM actually holds."
      confirmLabel="Clone"
      busyLabel="Copying disks…"
      busy={busy}
      disabled={invalid}
      error={error}
      onConfirm={submit}
      onClose={onClose}
    >
      <label htmlFor="clone-name" style={{ display: 'block', fontSize: 12.5, fontWeight: 500, marginBottom: 5 }}>
        Name for the clone
      </label>
      <input
        id="clone-name"
        autoFocus
        value={name}
        disabled={busy}
        onChange={e => setName(e.target.value)}
        style={{ ...input, borderColor: invalid ? 'var(--nd-danger-line)' : 'var(--nd-line)' }}
      />
      {invalid && <div style={{ fontSize: 12, color: 'var(--nd-danger)', marginTop: 5 }}>{VM_NAME_RULE}</div>}

      <label style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 14, cursor: 'pointer' }}>
        <input
          type="checkbox"
          checked={start}
          disabled={busy}
          onChange={e => setStart(e.target.checked)}
          style={{ accentColor: 'var(--nd-accent)' }}
        />
        <span style={{ fontSize: 13 }}>Start the clone once it is ready</span>
      </label>

      <div
        style={{
          marginTop: 14,
          background: 'var(--nd-warn-bg)',
          border: '1px solid var(--nd-warn-line)',
          borderRadius: 8,
          padding: '9px 11px',
          fontSize: 12,
          lineHeight: 1.55,
          color: 'var(--nd-warn)',
        }}
      >
        The clone gets its own MAC address, IP and hostname, but it starts from a copy of this VM's disk — so it carries the same machine-id
        and SSH host keys. If anything on your network tells hosts apart by those, run{' '}
        <code style={{ fontFamily: 'var(--nd-font-mono)' }}>sudo cloud-init clean</code> inside {vmId} before cloning it.
      </div>

      <div style={{ marginTop: 10, fontSize: 12, color: 'var(--nd-muted)', lineHeight: 1.55 }}>
        Passed-through host devices are not copied — a device can only be attached to one running VM at a time.
      </div>
    </Dialog>
  );
}

// SaveTemplateModal keeps a VM's configuration, not its contents.
export function SaveTemplateModal({ vmId, onClose, onSaved }: { vmId: string; onClose: () => void; onSaved: (name: string) => void }) {
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const trimmed = name.trim();
  const invalid = trimmed === '' || trimmed.length > 63 || /[/\\]/.test(trimmed);

  async function submit() {
    setBusy(true);
    setError('');
    try {
      const saved = await saveTemplate(trimmed, vmId);
      onSaved(saved.name);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  }

  return (
    <Dialog
      title={`Save ${vmId} as a template`}
      subtitle="Keeps the image, resources, network, firmware, CPU shape and cloud-init extras, so the wizard can start from them next time. Nothing on the disk is copied."
      confirmLabel="Save template"
      busyLabel="Saving…"
      busy={busy}
      disabled={invalid}
      error={error}
      onConfirm={submit}
      onClose={onClose}
    >
      <label htmlFor="template-name" style={{ display: 'block', fontSize: 12.5, fontWeight: 500, marginBottom: 5 }}>
        Template name
      </label>
      <input
        id="template-name"
        autoFocus
        value={name}
        disabled={busy}
        placeholder="Ubuntu web server"
        onChange={e => setName(e.target.value)}
        style={input}
      />
      <div style={{ marginTop: 10, fontSize: 12, color: 'var(--nd-muted)', lineHeight: 1.55 }}>
        The login password is not saved — it is hashed into the guest and never kept — so creating a VM from this template asks for a new
        one. Attached disks are not part of a template either; add them after the VM exists.
      </div>
    </Dialog>
  );
}
