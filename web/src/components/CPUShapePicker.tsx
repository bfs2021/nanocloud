import type { CPUShape } from '../types';

// The CPU's three settings, edited together because they describe one thing.
// Mounted in the create wizard and in the VM's Hardware tab.
//
// It shows two rules before the backend has to refuse them: a topology must
// multiply out to the VM's vCPU count or the VM won't define, and a cpuset can
// only name CPUs the host has.

const sectionLabel: React.CSSProperties = {
  fontSize: 11,
  fontWeight: 600,
  color: 'var(--nd-text-2)',
  letterSpacing: '0.06em',
  textTransform: 'uppercase',
  fontFamily: 'var(--nd-font-mono)',
  margin: '0 0 6px',
};

const hint: React.CSSProperties = { fontSize: 12, color: 'var(--nd-muted)', margin: '4px 0 0', lineHeight: 1.5 };

const numberInput: React.CSSProperties = {
  width: '100%',
  padding: '7px 9px',
  border: '1px solid var(--nd-line)',
  borderRadius: 7,
  fontSize: 13,
  fontFamily: 'var(--nd-font-mono)',
  outline: 'none',
};

// DEFAULT_CPU_SHAPE is the machine a VM gets when nobody touches this.
export const DEFAULT_CPU_SHAPE: CPUShape = { mode: 'host-model', topology: null, cpu_set: '' };

// isDefaultCPUShape lets the create request omit the CPU when nobody touched
// it, keeping the keys out of metadata.json.
export function isDefaultCPUShape(shape: CPUShape): boolean {
  return shape.mode === 'host-model' && shape.topology === null && shape.cpu_set.trim() === '';
}

// topologyError states the arithmetic rule in the user's own numbers. Returning
// null is the whole contract — callers disable their save button on a non-null.
export function topologyError(shape: CPUShape, vcpus: number): string | null {
  const t = shape.topology;
  if (!t) return null;
  if (t.sockets < 1 || t.cores < 1 || t.threads < 1) {
    return 'Sockets, cores and threads must each be at least 1.';
  }
  const product = t.sockets * t.cores * t.threads;
  if (product !== vcpus) {
    return `${t.sockets} × ${t.cores} × ${t.threads} is ${product} vCPUs, but this VM has ${vcpus}. libvirt will not define a domain whose topology and vCPU count disagree.`;
  }
  return null;
}

// cpuSetError checks what can be checked in the browser: the syntax, and — when
// the host's CPU count is known — that every CPU named exists. hostCPUs is 0
// when libvirt is unreachable, and then only the syntax is judged, matching the
// backend's own rule rather than inventing a stricter one.
export function cpuSetError(spec: string, hostCPUs: number): string | null {
  const trimmed = spec.trim();
  if (trimmed === '') return null;

  for (const raw of trimmed.split(',')) {
    const token = raw.trim().replace(/^\^/, '');
    if (!/^\d+(\s*-\s*\d+)?$/.test(token)) {
      return `“${raw.trim()}” is not a CPU or a range. Use forms like 4-7, 0,2,4 or 0-7,^4.`;
    }
    const [lo, hi = lo] = token.split('-').map(n => Number(n.trim()));
    if (hi < lo) return `“${raw.trim()}” counts backwards — the second number must be the larger one.`;
    if (hostCPUs > 0 && hi >= hostCPUs) {
      return `This host has ${hostCPUs} CPUs, numbered 0-${hostCPUs - 1}, so CPU ${hi} does not exist.`;
    }
  }
  return null;
}

function ModeOption({
  label,
  hintText,
  selected,
  disabled,
  onSelect,
}: {
  label: string;
  hintText: string;
  selected: boolean;
  disabled?: boolean;
  onSelect: () => void;
}) {
  return (
    <label
      style={{
        display: 'flex',
        alignItems: 'flex-start',
        gap: 10,
        padding: '10px 12px',
        border: `1px solid ${selected ? 'var(--nd-accent-line)' : 'var(--nd-line-soft)'}`,
        background: selected ? 'var(--nd-accent-soft)' : 'var(--nd-surface)',
        borderRadius: 9,
        cursor: disabled ? 'not-allowed' : 'pointer',
        opacity: disabled ? 0.55 : 1,
      }}
    >
      <input
        type="radio"
        checked={selected}
        disabled={disabled}
        onChange={onSelect}
        style={{ marginTop: 2, cursor: disabled ? 'not-allowed' : 'pointer' }}
      />
      <span>
        <span style={{ fontSize: 13, fontWeight: 500, color: 'var(--nd-text)' }}>{label}</span>
        <span style={{ ...hint, display: 'block' }}>{hintText}</span>
      </span>
    </label>
  );
}

export function CPUShapePicker({
  value,
  vcpus,
  hostCPUs,
  disabled,
  onChange,
}: {
  value: CPUShape;
  // The VM's vCPU count, which the topology has to agree with.
  vcpus: number;
  // 0 when the host's CPU count is not known.
  hostCPUs: number;
  disabled?: boolean;
  onChange: (next: CPUShape) => void;
}) {
  const topo = value.topology;
  const topoErr = topologyError(value, vcpus);
  const setErr = cpuSetError(value.cpu_set, hostCPUs);

  // Turning the topology on starts from a layout that is already valid for this
  // VM, so the field never opens on an error the user did not make.
  const toggleTopology = (on: boolean) => onChange({ ...value, topology: on ? { sockets: vcpus, cores: 1, threads: 1 } : null });

  const setPart = (part: 'sockets' | 'cores' | 'threads', n: number) => topo && onChange({ ...value, topology: { ...topo, [part]: n } });

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
      <div>
        <p style={sectionLabel}>CPU model</p>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <ModeOption
            label="host-model"
            hintText="A named CPU close to this host's. The guest sees nearly every feature the host has, and the VM can still move to a similar machine."
            selected={value.mode === 'host-model'}
            disabled={disabled}
            onSelect={() => onChange({ ...value, mode: 'host-model' })}
          />
          <ModeOption
            label="host-passthrough"
            hintText="This host's exact CPU, features and all. Needed for nested virtualisation; ties the VM to this machine."
            selected={value.mode === 'host-passthrough'}
            disabled={disabled}
            onSelect={() => onChange({ ...value, mode: 'host-passthrough' })}
          />
        </div>
      </div>

      <div>
        <p style={sectionLabel}>Topology</p>
        <label style={{ display: 'flex', alignItems: 'center', gap: 9, cursor: disabled ? 'not-allowed' : 'pointer' }}>
          <input
            type="checkbox"
            checked={topo !== null}
            disabled={disabled}
            onChange={e => toggleTopology(e.target.checked)}
            style={{ cursor: disabled ? 'not-allowed' : 'pointer' }}
          />
          <span style={{ fontSize: 13, color: 'var(--nd-text)' }}>Present a fixed socket / core / thread layout</span>
        </label>
        <p style={hint}>
          Left off, QEMU presents this VM's {vcpus} vCPU{vcpus === 1 ? '' : 's'} as {vcpus} single-core socket
          {vcpus === 1 ? '' : 's'}. Set it when software inside the guest licenses per socket, or when the guest's scheduler should know
          which cores share a cache.
        </p>

        {topo && (
          <>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 10, marginTop: 12 }}>
              {(['sockets', 'cores', 'threads'] as const).map(part => (
                <div key={part}>
                  <label style={{ ...sectionLabel, textTransform: 'capitalize', letterSpacing: 0, fontSize: 11.5 }}>{part}</label>
                  <input
                    type="number"
                    min={1}
                    aria-label={part}
                    value={topo[part]}
                    disabled={disabled}
                    onChange={e => setPart(part, Number(e.target.value))}
                    style={numberInput}
                  />
                </div>
              ))}
            </div>
            {topoErr && <p style={{ ...hint, color: 'var(--nd-warn)' }}>{topoErr}</p>}
          </>
        )}
      </div>

      <div>
        <p style={sectionLabel}>Pin to host CPUs</p>
        <input
          type="text"
          aria-label="cpu_set"
          placeholder={hostCPUs > 0 ? `e.g. 0-${Math.max(0, Math.min(3, hostCPUs - 1))}` : 'e.g. 0-3'}
          value={value.cpu_set}
          disabled={disabled}
          onChange={e => onChange({ ...value, cpu_set: e.target.value })}
          style={numberInput}
        />
        <p style={hint}>
          Confines every vCPU to these host CPUs. Leave empty to let the host scheduler place the VM anywhere.
          {hostCPUs > 0 && ` This host has ${hostCPUs} CPUs, numbered 0-${hostCPUs - 1}.`}
        </p>
        {setErr && <p style={{ ...hint, color: 'var(--nd-warn)' }}>{setErr}</p>}
      </div>
    </div>
  );
}
