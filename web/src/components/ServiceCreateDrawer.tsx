import { useEffect, useId, useRef, useState } from 'react';
import type { CreateServicePayload, Service, ServiceRecipe } from '../types';
import { createService } from '../api';
import { SERVICE_NAME_PATTERN, machineCount, machines, machineSummary, sizeSummary, suggestName } from '../services';
import { ServiceGlyph } from '../ui/ServiceGlyph';

interface ServiceCreateDrawerProps {
  recipe: ServiceRecipe;
  // Names already used by a service or a VM; a service's VM takes its name.
  taken: Set<string>;
  onClose: () => void;
  onCreated: (svc: Service) => void;
}

const input: React.CSSProperties = {
  width: '100%',
  minHeight: 38,
  padding: '0 12px',
  borderRadius: 9,
  border: '1px solid var(--nd-line)',
  background: 'var(--nd-surface)',
  color: 'var(--nd-text)',
  fontSize: 14,
};

const label: React.CSSProperties = { display: 'block', fontSize: 13, fontWeight: 500, marginBottom: 6 };
const help: React.CSSProperties = { fontSize: 12, color: 'var(--nd-muted)', marginTop: 5 };
const fieldError: React.CSSProperties = { fontSize: 12, color: 'var(--nd-danger)', marginTop: 5 };

// ServiceCreateDrawer asks for the few things a service needs — a name, a
// size, its settings — and launches it. Everything else, secrets included,
// the daemon decides.
export function ServiceCreateDrawer({ recipe, taken, onClose, onCreated }: ServiceCreateDrawerProps) {
  const [name, setName] = useState(() => suggestName(recipe, taken));
  const [size, setSize] = useState(() => (recipe.sizes.find(s => s.default) ?? recipe.sizes[0]).id);
  const settings = recipe.params.filter(p => !p.generate);
  const generated = recipe.params.filter(p => p.generate);
  const [params, setParams] = useState<Record<string, string>>(() => Object.fromEntries(settings.map(p => [p.id, p.default ?? ''])));
  const [showAccess, setShowAccess] = useState(false);
  const [githubUser, setGithubUser] = useState('');
  const [sshKey, setSshKey] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const titleId = useId();
  const firstField = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    firstField.current?.focus();
    firstField.current?.select();
    return () => previous?.focus?.();
  }, []);

  const nameProblem = !SERVICE_NAME_PATTERN.test(name)
    ? 'Lowercase letters, digits and dashes, starting with a letter.'
    : taken.has(name)
      ? `${name} is already taken by a service or a VM.`
      : null;
  const paramProblems = Object.fromEntries(
    settings.map(p => [p.id, p.pattern && !new RegExp(p.pattern).test(params[p.id] ?? '') ? `Use ${p.hint ?? 'a valid value'}.` : null]),
  );
  const invalid = !!nameProblem || Object.values(paramProblems).some(Boolean);
  const parts = machines(recipe, params);
  const vmCount = machineCount(parts);
  const chosenSize = recipe.sizes.find(s => s.id === size) ?? recipe.sizes[0];

  async function launch(e: React.FormEvent) {
    e.preventDefault();
    if (invalid || busy) return;
    setBusy(true);
    setError(null);
    const payload: CreateServicePayload = { name, recipe: recipe.id, size, params };
    if (githubUser.trim()) payload.github_user = githubUser.trim();
    if (sshKey.trim()) payload.ssh_public_key = sshKey.trim();
    try {
      onCreated(await createService(payload));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setBusy(false);
    }
  }

  return (
    <div
      onMouseDown={e => {
        if (e.target === e.currentTarget && !busy) onClose();
      }}
      style={{ position: 'fixed', inset: 0, zIndex: 40, background: 'var(--nd-scrim)', display: 'flex', justifyContent: 'flex-end' }}
    >
      <form
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onSubmit={launch}
        onKeyDown={e => {
          if (e.key === 'Escape' && !busy) onClose();
        }}
        style={{
          width: 'min(460px, 100%)',
          height: '100%',
          display: 'flex',
          flexDirection: 'column',
          background: 'var(--nd-bg)',
          borderLeft: '1px solid var(--nd-line)',
          boxShadow: 'var(--nd-shadow-lg)',
          animation: 'ncfadein 0.18s ease',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '20px 22px', borderBottom: '1px solid var(--nd-line)' }}>
          <ServiceGlyph recipe={recipe.id} size={40} />
          <div style={{ flex: 1, minWidth: 0 }}>
            <h2 id={titleId} style={{ margin: 0, fontSize: 18, fontWeight: 650, letterSpacing: '-0.015em' }}>
              Launch {recipe.name}
            </h2>
            <div style={{ fontSize: 12.5, color: 'var(--nd-muted)' }}>
              {vmCount > 1 ? `On ${vmCount} Ubuntu 24.04 VMs` : 'On its own Ubuntu 24.04 VM'}
            </div>
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            aria-label="Close"
            style={{ border: 'none', background: 'none', color: 'var(--nd-muted)', fontSize: 20, cursor: busy ? 'not-allowed' : 'pointer' }}
          >
            ✕
          </button>
        </div>

        <div style={{ flex: 1, overflowY: 'auto', padding: '20px 22px', display: 'flex', flexDirection: 'column', gap: 20 }}>
          <div>
            <label style={label} htmlFor={`${titleId}-name`}>
              Name
            </label>
            <input
              id={`${titleId}-name`}
              ref={firstField}
              value={name}
              onChange={e => setName(e.target.value.toLowerCase())}
              aria-invalid={!!nameProblem}
              spellCheck={false}
              autoComplete="off"
              style={{ ...input, fontFamily: 'var(--nd-font-mono)', borderColor: nameProblem ? 'var(--nd-danger-line)' : 'var(--nd-line)' }}
            />
            {nameProblem ? (
              <div style={fieldError}>{nameProblem}</div>
            ) : (
              <div style={help}>
                {recipe.roles?.length
                  ? `Its VMs are named after it: ${name}-${recipe.roles[0].id.replace(/_/g, '-')} and so on.`
                  : "Also the VM's name and hostname."}
              </div>
            )}
          </div>

          <fieldset style={{ border: 'none', margin: 0, padding: 0 }}>
            <legend style={{ ...label, padding: 0 }}>Size</legend>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {recipe.sizes.map(s => {
                const on = s.id === size;
                return (
                  <label
                    key={s.id}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 10,
                      padding: '10px 12px',
                      borderRadius: 10,
                      cursor: 'pointer',
                      border: `1px solid ${on ? 'var(--nd-accent)' : 'var(--nd-line)'}`,
                      background: on ? 'var(--nd-accent-soft)' : 'var(--nd-surface)',
                    }}
                  >
                    <input type="radio" name="size" value={s.id} checked={on} onChange={() => setSize(s.id)} />
                    <span style={{ fontWeight: 550, fontSize: 14, minWidth: 64 }}>{s.label}</span>
                    <span style={{ fontFamily: 'var(--nd-font-mono)', fontSize: 12, color: 'var(--nd-text-2)' }}>{sizeSummary(s)}</span>
                  </label>
                );
              })}
            </div>
          </fieldset>

          {settings.map(p => (
            <div key={p.id}>
              <label style={label} htmlFor={`${titleId}-${p.id}`}>
                {p.label}
              </label>
              <input
                id={`${titleId}-${p.id}`}
                value={params[p.id] ?? ''}
                onChange={e => setParams(prev => ({ ...prev, [p.id]: e.target.value }))}
                aria-invalid={!!paramProblems[p.id]}
                spellCheck={false}
                autoComplete="off"
                style={{
                  ...input,
                  fontFamily: 'var(--nd-font-mono)',
                  borderColor: paramProblems[p.id] ? 'var(--nd-danger-line)' : 'var(--nd-line)',
                }}
              />
              {paramProblems[p.id] && <div style={fieldError}>{paramProblems[p.id]}</div>}
            </div>
          ))}

          {vmCount > 1 && !invalid && (
            <div
              role="status"
              style={{
                fontSize: 13,
                lineHeight: 1.5,
                padding: '10px 12px',
                borderRadius: 9,
                border: '1px solid var(--nd-accent-line)',
                background: 'var(--nd-accent-soft)',
                color: 'var(--nd-text)',
              }}
            >
              Creates {vmCount} VMs — {machineSummary(parts)} — each {sizeSummary(chosenSize)}.
            </div>
          )}

          {generated.length > 0 && (
            <div style={{ fontSize: 12.5, color: 'var(--nd-muted)', lineHeight: 1.5 }}>
              Generated for you: {generated.map(p => p.label.toLowerCase()).join(' and ')}. Once it is ready, the service shows everything
              you need to connect.
            </div>
          )}

          <div>
            <button
              type="button"
              onClick={() => setShowAccess(v => !v)}
              aria-expanded={showAccess}
              style={{ border: 'none', background: 'none', padding: 0, color: 'var(--nd-accent)', fontSize: 13, cursor: 'pointer' }}
            >
              {showAccess ? '− ' : '+ '}SSH access to the {vmCount > 1 ? 'VMs' : 'VM'} (optional)
            </button>
            {showAccess && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginTop: 12 }}>
                <div>
                  <label style={label} htmlFor={`${titleId}-gh`}>
                    GitHub username
                  </label>
                  <input
                    id={`${titleId}-gh`}
                    value={githubUser}
                    onChange={e => setGithubUser(e.target.value)}
                    placeholder="octocat"
                    spellCheck={false}
                    style={input}
                  />
                  <div style={help}>Its published SSH keys are allowed to log in as nanocloud.</div>
                </div>
                <div>
                  <label style={label} htmlFor={`${titleId}-key`}>
                    Public key
                  </label>
                  <textarea
                    id={`${titleId}-key`}
                    value={sshKey}
                    onChange={e => setSshKey(e.target.value)}
                    placeholder="ssh-ed25519 AAAA… you@laptop"
                    spellCheck={false}
                    rows={3}
                    style={{ ...input, padding: '8px 12px', fontFamily: 'var(--nd-font-mono)', fontSize: 12.5, resize: 'vertical' }}
                  />
                </div>
              </div>
            )}
          </div>

          {error && (
            <div
              role="alert"
              style={{
                padding: '10px 12px',
                borderRadius: 9,
                background: 'var(--nd-danger-bg)',
                border: '1px solid var(--nd-danger-line)',
                color: 'var(--nd-danger)',
                fontSize: 13,
              }}
            >
              {error}
            </div>
          )}
        </div>

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 10, padding: '16px 22px', borderTop: '1px solid var(--nd-line)' }}>
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            style={{
              minHeight: 38,
              padding: '0 16px',
              borderRadius: 9,
              border: '1px solid var(--nd-line)',
              background: 'var(--nd-surface)',
              color: 'var(--nd-text)',
              fontSize: 13.5,
              cursor: 'pointer',
            }}
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={invalid || busy}
            style={{
              minHeight: 38,
              padding: '0 18px',
              borderRadius: 9,
              border: 'none',
              background: 'var(--nd-accent)',
              color: 'var(--nd-on-accent)',
              fontSize: 13.5,
              fontWeight: 600,
              cursor: invalid || busy ? 'not-allowed' : 'pointer',
              opacity: invalid || busy ? 0.6 : 1,
            }}
          >
            {busy ? 'Launching…' : `Launch ${recipe.name}`}
          </button>
        </div>
      </form>
    </div>
  );
}
