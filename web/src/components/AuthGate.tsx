import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { getAuthStatus, login, UNAUTHORIZED_EVENT } from '../api';
import { BrandMark } from '../ui/BrandMark';
import { CopyField } from '../ui/CopyField';
import { Spinner } from '../ui/Spinner';

type GateState = 'checking' | 'signed-in' | 'signed-out' | 'unreachable';

// AuthGate renders the app only for a signed-in browser. Any 401 from any
// request signs it out again, unmounting the app and with it every poll.
export function AuthGate({ children }: { children: ReactNode }) {
  const [state, setState] = useState<GateState>('checking');
  const [setupRequired, setSetupRequired] = useState(false);

  const check = useCallback(async () => {
    setState('checking');
    try {
      const status = await getAuthStatus();
      setSetupRequired(status.setup_required);
      setState(status.authenticated ? 'signed-in' : 'signed-out');
    } catch {
      setState('unreachable');
    }
  }, []);

  useEffect(() => {
    void check();
  }, [check]);
  useEffect(() => {
    const signOut = () => setState('signed-out');
    window.addEventListener(UNAUTHORIZED_EVENT, signOut);
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, signOut);
  }, []);

  if (state === 'signed-in') return <>{children}</>;
  return (
    <main style={{ minHeight: '100vh', display: 'grid', placeItems: 'center', padding: 16, background: 'var(--nd-bg)' }}>
      {state === 'checking' && <Spinner size={22} />}
      {state === 'unreachable' && (
        <Card>
          <h1 style={titleStyle}>Can’t reach nano-cloud</h1>
          <p style={{ color: 'var(--nd-muted)', fontSize: 13.5, margin: '0 0 18px' }}>
            The daemon did not answer. Check it is running (<code>sudo nanocloud doctor</code>), then try again.
          </p>
          <button type="button" style={primaryButton} onClick={() => void check()}>
            Try again
          </button>
        </Card>
      )}
      {state === 'signed-out' && <LoginForm setupRequired={setupRequired} onSignedIn={() => setState('signed-in')} />}
    </main>
  );
}

function LoginForm({ setupRequired, onSignedIn }: { setupRequired: boolean; onSignedIn: () => void }) {
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!password || busy) return;
    setBusy(true);
    setError(null);
    try {
      await login(password);
      onSignedIn();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setPassword('');
    } finally {
      setBusy(false);
    }
  };

  if (setupRequired) {
    return (
      <Card>
        <h1 style={titleStyle}>Set a password first</h1>
        <p style={{ color: 'var(--nd-muted)', fontSize: 13.5, margin: '0 0 18px', lineHeight: 1.5 }}>
          This host has no login password yet, so nano-cloud refuses every request. Run this on the host, then reload:
        </p>
        <CopyField label="On the host" value="sudo nanocloud passwd" />
      </Card>
    );
  }

  return (
    <Card>
      <form onSubmit={submit}>
        <h1 style={titleStyle}>Sign in</h1>
        {/* Password managers file a password under a username; there is one account. */}
        <input type="text" name="username" autoComplete="username" value="nanocloud" readOnly hidden />
        <label htmlFor="nc-password" style={{ display: 'block', fontSize: 12.5, color: 'var(--nd-muted)', marginBottom: 6 }}>
          Password
        </label>
        <input
          id="nc-password"
          type="password"
          autoComplete="current-password"
          autoFocus
          value={password}
          onChange={e => setPassword(e.target.value)}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? 'nc-login-error' : undefined}
          style={{
            width: '100%',
            boxSizing: 'border-box',
            height: 40,
            padding: '0 12px',
            borderRadius: 9,
            border: `1px solid ${error ? 'var(--nd-danger-line)' : 'var(--nd-line)'}`,
            background: 'var(--nd-surface-2)',
            color: 'var(--nd-text)',
            fontSize: 14,
          }}
        />
        {error && (
          <p id="nc-login-error" role="alert" style={{ color: 'var(--nd-danger)', fontSize: 13, margin: '8px 0 0' }}>
            {error}
          </p>
        )}
        <button
          type="submit"
          disabled={!password || busy}
          style={{ ...primaryButton, width: '100%', marginTop: 18, opacity: !password || busy ? 0.6 : 1 }}
        >
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
        <p style={{ color: 'var(--nd-muted)', fontSize: 12, margin: '16px 0 0', lineHeight: 1.5 }}>
          Forgot it? Run <code>sudo nanocloud passwd</code> on the host for a new one.
        </p>
      </form>
    </Card>
  );
}

function Card({ children }: { children: ReactNode }) {
  return (
    <div
      style={{
        width: '100%',
        maxWidth: 380,
        padding: 28,
        borderRadius: 14,
        border: '1px solid var(--nd-line)',
        background: 'var(--nd-surface)',
        boxShadow: 'var(--nd-shadow-lg)',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 22 }}>
        <BrandMark size={34} label="Nano Cloud" />
        <div aria-hidden="true" style={{ fontWeight: 650, fontSize: 17, letterSpacing: '-0.02em' }}>
          nano<span style={{ color: 'var(--nd-accent)' }}>·</span>cloud
        </div>
      </div>
      {children}
    </div>
  );
}

const titleStyle: React.CSSProperties = { fontSize: 20, fontWeight: 650, margin: '0 0 14px', letterSpacing: '-0.01em' };

const primaryButton: React.CSSProperties = {
  height: 40,
  padding: '0 18px',
  borderRadius: 9,
  border: 'none',
  background: 'var(--nd-accent)',
  color: 'var(--nd-on-accent)',
  fontWeight: 600,
  fontSize: 14,
  cursor: 'pointer',
};
