import { useId, useState } from 'react';
import type { HostBridge, VMNetwork } from '../../types';
import { useCapabilities } from '../../capabilities';
import { Field, Input, Select } from '../../ui/Field';
import { ReasonNote } from './controls';
import { needsVlan } from './state';
import './create-vm.css';

// Where someone is sent when the host has no bridge to offer.
const NETWORKING_DOCS_URL = 'https://nanocloud-io.github.io/nanocloud/docs/networking.html';

const NAT_HINT =
  'A private network inside this machine. The VM can reach the internet and your other VMs. Your other devices can’t reach it directly; share a service to give it a port on this machine.';
const BRIDGE_HINT = 'The VM joins your network like a physical computer and gets an address from your router. Needs a wired connection.';
const VLAN_HINT =
  'Puts the VM on a separate network that your switch keeps apart. Only works if your router or switch is set up for VLANs. Your router decides whether VLANs can talk to each other.';

// NetworkPicker edits where a VM is attached. Mounted twice, like
// CPUShapePicker: in the create wizard, and in a VM's Network tab where
// changing it rebuilds the domain.
export function NetworkPicker({
  value,
  bridges,
  uplink,
  disabled,
  onChange,
}: {
  value: VMNetwork;
  bridges: HostBridge[];
  // The host's default-route interface — what a VLAN subinterface rides.
  // Empty when the host has no default route, the only case where a VLAN
  // cannot be offered at all.
  uplink: string;
  disabled?: boolean;
  onChange: (next: VMNetwork) => void;
}) {
  const groupName = useId();
  const hasBridge = bridges.length > 0;
  const selected = bridges.find(b => b.name === value.bridge) ?? bridges[0];
  // A tagged VM needs no operator bridge, since nano-cloud builds nc-vlan<id>
  // on the uplink, so an uplink alone is enough to offer Bridge: an ordinary
  // host's eth0, docker0 and virbr0 have no physical port, so `bridges` is
  // empty. An untagged VM still needs a bridge, which vlanRequired says.
  // None of this applies on a backend that cannot bridge (macOS).
  const caps = useCapabilities();
  const canBridge = caps.bridge && (hasBridge || !!uplink);
  const vlanRequired = needsVlan(value, bridges);

  function choose(mode: 'nat' | 'bridge') {
    onChange(
      mode === 'bridge'
        ? { mode, bridge: value.bridge || selected?.name || '', vlan_id: value.vlan_id }
        : // NAT owns its own bridge and a tag on it is invisible outside this
          // host, so both are dropped rather than carried along into a
          // request the API refuses.
          { mode, bridge: '', vlan_id: 0 },
    );
  }

  return (
    <div className="cv-stack">
      <fieldset className="cv-group">
        <legend>Network mode</legend>
        <div className="cv-choices">
          {(['nat', 'bridge'] as const).map(mode => {
            const off = disabled || (mode === 'bridge' && !canBridge);
            return (
              <label key={mode} className="cv-choice" data-selected={value.mode === mode} data-disabled={off || undefined}>
                <input type="radio" name={groupName} checked={value.mode === mode} disabled={off} onChange={() => choose(mode)} />
                <span className="cv-choice-dot" aria-hidden="true" />
                <span>
                  <span className="cv-choice-title">
                    {mode === 'nat' ? 'NAT' : 'Your network (bridge)'}
                    {mode === 'nat' && <span className="cv-choice-tag">Recommended</span>}
                  </span>
                  <span className="cv-choice-hint">{mode === 'nat' ? NAT_HINT : BRIDGE_HINT}</span>
                </span>
              </label>
            );
          })}
        </div>
        {!caps.bridge && <p className="cv-note">A Mac runs VMs on NAT only.</p>}
        {caps.bridge && !hasBridge && (
          <p className="cv-note">
            Set up a bridge on this host first.
            {canBridge && value.mode !== 'bridge' && ' Until then, a bridged VM needs a VLAN tag.'}{' '}
            <a className="cv-link" href={`${NETWORKING_DOCS_URL}#bridge`} target="_blank" rel="noreferrer">
              How to set up a bridge<span className="cv-sr-only"> (opens in a new tab)</span>
            </a>
          </p>
        )}
      </fieldset>

      {value.mode === 'bridge' && (
        <div className="cv-stack cv-stack-tight">
          {/* The host-bridge select only applies untagged: a tagged VM's bridge is
              nc-vlan<id>. A single bridge isn't a choice, so no select is shown. */}
          {value.vlan_id === 0 && bridges.length > 1 && (
            <Field label="Host bridge">
              <Select value={selected?.name ?? ''} disabled={disabled} onChange={e => onChange({ ...value, bridge: e.target.value })}>
                {bridges.map(b => (
                  <option key={b.name} value={b.name}>
                    {b.name}
                  </option>
                ))}
              </Select>
            </Field>
          )}

          <VlanDisclosure value={value} uplink={uplink} disabled={disabled} forced={vlanRequired} onChange={onChange} />
        </div>
      )}

      {vlanRequired && (
        <ReasonNote
          availability={{
            available: false,
            reason: `This host has no bridge to your network yet, so this VM needs a VLAN tag. Enter one under Advanced: VLAN tag and nano-cloud builds the connection on ${uplink} for it.`,
          }}
        />
      )}

      {value.mode === 'bridge' && value.vlan_id > 0 && uplink && (
        <p className="cv-note">
          nano-cloud will create {uplink}.{value.vlan_id} and bridge nc-vlan{value.vlan_id}. Your existing network is not modified.
        </p>
      )}
    </div>
  );
}

// VlanDisclosure keeps the tag out of the way, since most hosts want an
// untagged VM. It opens by itself when a tag is set and stays open while one
// is required. Mounted only in bridge mode, so its initial state comes from
// the VM being shown.
function VlanDisclosure({
  value,
  uplink,
  disabled,
  forced,
  onChange,
}: {
  value: VMNetwork;
  uplink: string;
  disabled?: boolean;
  forced: boolean;
  onChange: (next: VMNetwork) => void;
}) {
  const panelId = useId();
  const [open, setOpen] = useState(value.vlan_id > 0);
  const expanded = open || forced;

  return (
    <div className="cv-vlan">
      <button
        type="button"
        className="cv-vlan-toggle"
        aria-expanded={expanded}
        aria-controls={panelId}
        disabled={forced}
        onClick={() => setOpen(o => !o)}
      >
        <span aria-hidden="true">{expanded ? '▾ ' : '▸ '}</span>
        Advanced: VLAN tag
      </button>
      {expanded && (
        <div id={panelId} className="cv-vlan-panel">
          <Field
            label="VLAN ID"
            hint={
              <>
                {VLAN_HINT}
                {!uplink && ' This host has no default route to put a VLAN on, so a tag isn’t available.'}
              </>
            }
          >
            <Input
              type="number"
              min={1}
              max={4094}
              value={value.vlan_id || ''}
              placeholder="untagged"
              disabled={disabled || !uplink}
              title={!uplink ? 'this host has no default route to put a VLAN on' : undefined}
              onChange={e => onChange({ ...value, vlan_id: Number(e.target.value) || 0 })}
            />
          </Field>
        </div>
      )}
    </div>
  );
}
