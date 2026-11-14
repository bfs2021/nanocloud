import { useEffect, useState } from 'react';
import { changePassword, createToken, listTokens, logout, revokeToken, UNAUTHORIZED_EVENT } from '../../api';
import { formatDate } from '../../design';
import type { APIToken, FileSettings, NewAPIToken } from '../../types';
import { Button } from '../../ui/Button';
import { useConfirm } from '../../ui/ConfirmDialog';
import { CopyField } from '../../ui/CopyField';
import { Field, Input } from '../../ui/Field';
import { messageOf, SettingsCard } from './common';

const MIN_PASSWORD = 8;

export function SecuritySection({ file }: { file?: FileSettings }) {
  return (
    <>
      <PasswordCard />
      <TokensCard />
      <CertificateCard file={file} />
      <SignOutCard />
    </>
  );
}

// Changing the password signs out every other browser; this one stays in.
function PasswordCard() {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [again, setAgain] = useState('');
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);

  const tooShort = next !== '' && next.length < MIN_PASSWORD;
  const mismatch = again !== '' && again !== next;
  const ready = current !== '' && next.length >= MIN_PASSWORD && next === again;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!ready || busy) return;
    setBusy(true);
    setResult(null);
    try {
      await changePassword(current, next);
      setCurrent('');
      setNext('');
      setAgain('');
      setResult({ ok: true, text: 'Password changed. Every other browser is signed out.' });
    } catch (err) {
      setResult({ ok: false, text: messageOf(err) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <SettingsCard title="Password">
      <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--nd-space-3)', maxWidth: 360 }}>
        {/* One account; the name is only for password managers. */}
        <input type="text" name="username" autoComplete="username" value="nanocloud" readOnly hidden />
        <Field label="Current password">
          <Input type="password" autoComplete="current-password" value={current} onChange={e => setCurrent(e.target.value)} />
        </Field>
        <Field label="New password" error={tooShort ? `At least ${MIN_PASSWORD} characters.` : undefined}>
          <Input type="password" autoComplete="new-password" value={next} onChange={e => setNext(e.target.value)} />
        </Field>
        <Field label="New password, again" error={mismatch ? 'Does not match.' : undefined}>
          <Input type="password" autoComplete="new-password" value={again} onChange={e => setAgain(e.target.value)} />
        </Field>
        <div>
          <Button type="submit" variant="primary" disabled={!ready} busy={busy}>
            Change password
          </Button>
        </div>
        {result && (
          <p role={result.ok ? 'status' : 'alert'} className={result.ok ? 'nd-card-text' : 'nd-field-error'}>
            {result.text}
          </p>
        )}
      </form>
    </SettingsCard>
  );
}

// A token's secret exists in exactly one response, so the card holds on to
// it until the person closes it.
function TokensCard() {
  const [tokens, setTokens] = useState<APIToken[] | null>(null);
  const [name, setName] = useState('');
  const [created, setCreated] = useState<NewAPIToken | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirm, confirmDialog] = useConfirm();

  useEffect(() => {
    listTokens().then(setTokens, e => setError(messageOf(e)));
  }, []);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    if (!name.trim() || busy) return;
    setBusy(true);
    setError(null);
    try {
      const tok = await createToken(name.trim());
      setCreated(tok);
      setName('');
      setTokens(await listTokens());
    } catch (err) {
      setError(messageOf(err));
    } finally {
      setBusy(false);
    }
  }

  async function revoke(tok: APIToken) {
    const ok = await confirm({
      title: `Revoke ${tok.name}?`,
      body: 'Anything still using it is refused from now on.',
      confirmLabel: 'Revoke token',
    });
    if (!ok) return;
    try {
      await revokeToken(tok.id);
      setTokens(prev => prev?.filter(t => t.id !== tok.id) ?? null);
    } catch (err) {
      setError(messageOf(err));
    }
  }

  return (
    <SettingsCard title="API tokens">
      {confirmDialog}
      <p className="nd-card-text">
        For scripts and agents: send one as <code className="nd-mono">Authorization: Bearer nc_…</code>. Each has the same access as signing
        in. Name each after what uses it, so you know what stops working when you revoke it.
      </p>

      {created && (
        <div
          role="status"
          style={{
            display: 'grid',
            gap: 'var(--nd-space-3)',
            padding: 'var(--nd-space-4)',
            borderRadius: 'var(--nd-radius-lg)',
            border: '1px solid var(--nd-accent-line)',
            background: 'var(--nd-accent-soft)',
          }}
        >
          <p className="nd-card-text" style={{ color: 'var(--nd-text)' }}>
            Copy <strong>{created.name}</strong> now. It is not shown again.
          </p>
          <CopyField label="Token" value={created.token} />
          <div>
            <Button size="sm" onClick={() => setCreated(null)}>
              Done
            </Button>
          </div>
        </div>
      )}

      {tokens && tokens.length > 0 && (
        <ul className="nd-rows" style={{ listStyle: 'none', margin: 0, padding: 0 }}>
          {tokens.map(t => (
            <li key={t.id} className="nd-setting-row" style={{ alignItems: 'center' }}>
              <div style={{ minWidth: 0 }}>
                <div style={{ fontWeight: 600, fontSize: 'var(--nd-text-md)' }}>{t.name}</div>
                <div className="nd-card-text">
                  Created {formatDate(t.created_at)} · {lastUsed(t)}
                </div>
              </div>
              <Button size="sm" variant="danger" onClick={() => void revoke(t)} aria-label={`Revoke ${t.name}`}>
                Revoke
              </Button>
            </li>
          ))}
        </ul>
      )}
      {tokens && tokens.length === 0 && <p className="nd-card-text">No tokens yet.</p>}

      <form onSubmit={create} style={{ display: 'flex', gap: 'var(--nd-space-2)', alignItems: 'flex-end', flexWrap: 'wrap' }}>
        <div style={{ flex: '1 1 220px' }}>
          <Field label="New token name">
            <Input value={name} onChange={e => setName(e.target.value)} placeholder="backup script" maxLength={64} />
          </Field>
        </div>
        <Button type="submit" variant="primary" disabled={!name.trim()} busy={busy}>
          Create token
        </Button>
      </form>
      {error && (
        <p role="alert" className="nd-field-error">
          {error}
        </p>
      )}
    </SettingsCard>
  );
}

function lastUsed(t: APIToken) {
  const at = t.last_used ? new Date(t.last_used) : null;
  return at && at.getFullYear() > 2000 ? `last used ${formatDate(t.last_used!)}` : 'never used';
}

function CertificateCard({ file }: { file?: FileSettings }) {
  if (!file) return null;
  return (
    <SettingsCard title="HTTPS certificate">
      {file.tls_fingerprint ? (
        <>
          <p className="nd-card-text">
            If the certificate is self-signed, the browser warns the first time. Before you accept, check that the fingerprint it shows
            matches this one.
          </p>
          <CopyField label="SHA-256 fingerprint" value={file.tls_fingerprint} />
          {file.tls_cert && <p className="nd-card-text nd-mono">{file.tls_cert}</p>}
        </>
      ) : (
        <p className="nd-card-text">
          HTTPS is off (<code className="nd-mono">tls: off</code> in {file.config_path || 'config.yaml'}), so the password and console
          sessions cross the network unencrypted. Leave it off only behind a reverse proxy that serves HTTPS.
        </p>
      )}
    </SettingsCard>
  );
}

function SignOutCard() {
  const [error, setError] = useState<string | null>(null);
  async function signOut() {
    try {
      await logout();
    } catch (err) {
      setError(messageOf(err));
      return;
    }
    window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
  }
  return (
    <SettingsCard title="This browser">
      <div className="nd-setting-row" style={{ alignItems: 'center' }}>
        <p className="nd-card-text">Stays signed in until it goes 30 days without a visit.</p>
        <Button onClick={() => void signOut()}>Sign out</Button>
      </div>
      {error && (
        <p role="alert" className="nd-field-error">
          {error}
        </p>
      )}
    </SettingsCard>
  );
}
