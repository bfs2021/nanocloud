import { useState, useEffect } from 'react';
import type { VMTemplate } from '../types';
import { listTemplates, deleteTemplate } from '../api';
import { formatDate } from '../design';

// TemplatesCard is where saved configurations are reviewed and deleted. Saving
// happens on a VM and using one in the wizard, so the list lives here with the
// other host-level readouts.
export function TemplatesCard() {
  const [templates, setTemplates] = useState<VMTemplate[] | null>(null);
  const [error, setError] = useState('');
  const [pending, setPending] = useState('');

  function load() {
    listTemplates()
      .then(t => {
        setTemplates(t);
        setError('');
      })
      .catch(e => setError(e instanceof Error ? e.message : String(e)));
  }

  useEffect(load, []);

  async function remove(name: string) {
    setPending(name);
    try {
      await deleteTemplate(name);
      load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setPending('');
    }
  }

  return (
    <section className="nd-card">
      <h2 className="nd-card-title">Templates</h2>
      <p className="nd-card-text" style={{ marginBottom: 16 }}>
        Saved machine configurations. Create one from any VM's page, then start the wizard from it.
      </p>

      {error && (
        <div
          style={{
            padding: '10px 14px',
            background: 'var(--nd-danger-bg)',
            border: '1px solid var(--nd-danger-line)',
            borderRadius: 8,
            color: 'var(--nd-danger)',
            fontSize: 13,
          }}
        >
          {error}
        </div>
      )}

      {templates !== null && templates.length === 0 && !error && (
        <p style={{ color: 'var(--nd-muted)', fontSize: 13.5, padding: '8px 0', margin: 0 }}>No templates saved yet.</p>
      )}

      {templates?.map((t, i) => (
        <div
          key={t.name}
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 16,
            padding: '13px 0',
            borderBottom: i === templates.length - 1 ? 'none' : '1px solid var(--nd-line-soft)',
          }}
        >
          <div style={{ minWidth: 0 }}>
            <div style={{ fontSize: 13.5, fontWeight: 500, color: 'var(--nd-text)' }}>{t.name}</div>
            <div style={{ fontSize: 12, color: 'var(--nd-muted)', marginTop: 2 }}>
              {t.spec.image_id} · {t.spec.vcpus} vCPU · {Math.round(t.spec.memory_mb / 1024)} GB · {t.spec.disk_gb} GB
              {t.source_vm && ` · from ${t.source_vm}`}
              {t.created_at_unix > 0 && ` · saved ${formatDate(new Date(t.created_at_unix * 1000).toISOString())}`}
            </div>
          </div>
          <button
            onClick={() => remove(t.name)}
            disabled={pending === t.name}
            style={{
              background: 'var(--nd-surface)',
              border: '1px solid var(--nd-line)',
              borderRadius: 8,
              padding: '6px 12px',
              fontSize: 12.5,
              color: pending === t.name ? 'var(--nd-muted)' : 'var(--nd-danger)',
              cursor: pending === t.name ? 'not-allowed' : 'pointer',
              flexShrink: 0,
            }}
          >
            {pending === t.name ? 'Deleting…' : 'Delete'}
          </button>
        </div>
      ))}
    </section>
  );
}
