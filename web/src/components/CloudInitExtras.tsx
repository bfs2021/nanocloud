import { useState } from 'react';
import type { WriteFile } from '../types';
import { fetchGitHubKeys } from '../api';

// The provisioning half of cloud-init: packages, commands, files, and keys
// imported from a GitHub account. All of it runs once, on first boot, which
// the section says.
//
// The daemon resolves the GitHub import, not cloud-init: `ssh_import_id` needs
// a package Alpine lacks and egress the guest may not have. That is also what
// lets the keys be previewed here.

export interface CloudInitExtrasValue {
  githubUser: string;
  packages: string;
  runcmd: string;
  writeFiles: WriteFile[];
}

export const EMPTY_EXTRAS: CloudInitExtrasValue = {
  githubUser: '',
  packages: '',
  runcmd: '',
  writeFiles: [],
};

// toList splits a textarea into the list the API takes. Commas as well as
// newlines for packages, because "nginx, git" is what people type.
export function toList(text: string, splitOnCommas: boolean): string[] {
  return text
    .split(splitOnCommas ? /[\n,]/ : /\n/)
    .map(s => s.trim())
    .filter(Boolean);
}

// hasExtras reports whether anything was actually filled in, so the request can
// omit the keys entirely for a VM that asked for none.
export function hasExtras(v: CloudInitExtrasValue): boolean {
  return (
    v.githubUser.trim() !== '' ||
    toList(v.packages, true).length > 0 ||
    toList(v.runcmd, false).length > 0 ||
    v.writeFiles.some(f => f.path.trim() !== '')
  );
}

const labelStyle: React.CSSProperties = {
  fontSize: 12,
  fontWeight: 500,
  color: 'var(--nd-text-2)',
  display: 'block',
  marginBottom: 6,
};

const inputStyle: React.CSSProperties = {
  width: '100%',
  background: 'var(--nd-surface)',
  border: '1px solid var(--nd-line)',
  borderRadius: 9,
  padding: '9px 11px',
  fontSize: 13,
  fontFamily: 'var(--nd-font-mono)',
  outline: 'none',
  boxSizing: 'border-box',
};

const hint: React.CSSProperties = { fontSize: 11.5, color: 'var(--nd-muted)', margin: '5px 0 0', lineHeight: 1.5 };

export function CloudInitExtras({
  value,
  disabled,
  onChange,
}: {
  value: CloudInitExtrasValue;
  disabled?: boolean;
  onChange: (next: CloudInitExtrasValue) => void;
}) {
  const [keys, setKeys] = useState<string[] | null>(null);
  const [keysError, setKeysError] = useState<string | null>(null);
  const [checking, setChecking] = useState(false);

  const set = <K extends keyof CloudInitExtrasValue>(key: K, v: CloudInitExtrasValue[K]) => onChange({ ...value, [key]: v });

  const preview = async () => {
    setChecking(true);
    setKeys(null);
    setKeysError(null);
    try {
      setKeys(await fetchGitHubKeys(value.githubUser.trim()));
    } catch (e) {
      setKeysError(e instanceof Error ? e.message : String(e));
    } finally {
      setChecking(false);
    }
  };

  const setFile = (i: number, patch: Partial<WriteFile>) =>
    set(
      'writeFiles',
      value.writeFiles.map((f, j) => (j === i ? { ...f, ...patch } : f)),
    );

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
      <div>
        <label style={labelStyle}>Import SSH keys from GitHub</label>
        <div style={{ display: 'flex', gap: 8 }}>
          <input
            type="text"
            placeholder="github-username"
            aria-label="github username"
            value={value.githubUser}
            disabled={disabled}
            onChange={e => {
              set('githubUser', e.target.value);
              setKeys(null);
              setKeysError(null);
            }}
            style={inputStyle}
          />
          <button
            type="button"
            onClick={preview}
            disabled={disabled || checking || value.githubUser.trim() === ''}
            style={{
              background: 'var(--nd-accent-soft)',
              border: '1px solid var(--nd-accent-line)',
              borderRadius: 9,
              padding: '0 14px',
              fontSize: 12.5,
              fontWeight: 600,
              color: 'var(--nd-accent)',
              cursor: value.githubUser.trim() === '' ? 'default' : 'pointer',
              whiteSpace: 'nowrap',
            }}
          >
            {checking ? 'Checking…' : 'Check'}
          </button>
        </div>
        <p style={hint}>
          nano-cloud fetches the account's public keys now and writes them into the VM. The guest never contacts GitHub, so this works on
          images without <code>ssh-import-id</code> and on networks with no outbound access.
        </p>
        {keys && (
          <p style={{ ...hint, color: 'var(--nd-ready)' }}>
            {keys.length} key{keys.length === 1 ? '' : 's'} will be installed:{' '}
            {keys.map(k => k.split(' ').slice(-1)[0] || k.slice(0, 24)).join(', ')}
          </p>
        )}
        {keysError && <p style={{ ...hint, color: 'var(--nd-warn)' }}>{keysError}</p>}
      </div>

      <div>
        <label style={labelStyle}>Packages</label>
        <input
          type="text"
          placeholder="nginx, git, htop"
          aria-label="packages"
          value={value.packages}
          disabled={disabled}
          onChange={e => set('packages', e.target.value)}
          style={inputStyle}
        />
        <p style={hint}>Installed on first boot. The package index is refreshed first.</p>
      </div>

      <div>
        <label style={labelStyle}>Run commands</label>
        <textarea
          rows={3}
          placeholder={'systemctl enable --now nginx\ncurl -fsSL https://example.com/setup.sh | sh'}
          aria-label="run commands"
          value={value.runcmd}
          disabled={disabled}
          onChange={e => set('runcmd', e.target.value)}
          style={{ ...inputStyle, resize: 'vertical', lineHeight: 1.5 }}
        />
        <p style={hint}>One per line, run in order as root, once, late in the first boot.</p>
      </div>

      <div>
        <label style={labelStyle}>Files</label>
        {value.writeFiles.map((f, i) => (
          <div key={i} style={{ display: 'flex', flexDirection: 'column', gap: 7, marginBottom: 10 }}>
            <div style={{ display: 'flex', gap: 8 }}>
              <input
                type="text"
                placeholder="/etc/motd"
                aria-label={`file ${i + 1} path`}
                value={f.path}
                disabled={disabled}
                onChange={e => setFile(i, { path: e.target.value })}
                style={inputStyle}
              />
              <input
                type="text"
                placeholder="0644"
                aria-label={`file ${i + 1} permissions`}
                value={f.permissions ?? ''}
                disabled={disabled}
                onChange={e => setFile(i, { permissions: e.target.value })}
                style={{ ...inputStyle, width: 92, flexShrink: 0 }}
              />
              <button
                type="button"
                onClick={() =>
                  set(
                    'writeFiles',
                    value.writeFiles.filter((_, j) => j !== i),
                  )
                }
                disabled={disabled}
                style={{
                  background: 'none',
                  border: '1px solid var(--nd-line)',
                  borderRadius: 9,
                  padding: '0 11px',
                  fontSize: 12.5,
                  color: 'var(--nd-text-2)',
                  cursor: 'pointer',
                }}
              >
                Remove
              </button>
            </div>
            <textarea
              rows={2}
              placeholder="file contents"
              aria-label={`file ${i + 1} content`}
              value={f.content}
              disabled={disabled}
              onChange={e => setFile(i, { content: e.target.value })}
              style={{ ...inputStyle, resize: 'vertical', lineHeight: 1.5 }}
            />
          </div>
        ))}
        <button
          type="button"
          onClick={() => set('writeFiles', [...value.writeFiles, { path: '', content: '' }])}
          disabled={disabled}
          style={{
            background: 'none',
            border: '1px dashed var(--nd-line)',
            borderRadius: 9,
            padding: '7px 13px',
            fontSize: 12.5,
            color: 'var(--nd-text)',
            cursor: 'pointer',
          }}
        >
          + Add a file
        </button>
        <p style={hint}>
          Written before the commands above run, so a command here can read a file written there. Paths must be absolute; permissions are an
          octal mode like <code>0644</code>.
        </p>
      </div>
    </div>
  );
}
