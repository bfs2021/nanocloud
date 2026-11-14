import type { HostBridge, VMNetwork } from '../../types';
import { NetworkPicker } from './NetworkPicker';
import './create-vm.css';

interface NetworkStepProps {
  network: VMNetwork;
  bridges: HostBridge[];
  uplink: string;
  disabled: boolean;
  onChange: (next: VMNetwork) => void;
}

// NetworkStep is the picker with a sentence of context. Whether the choice is
// valid is decided in state.ts, so the picker's own notes are the only copy
// about a missing VLAN on screen.
export function NetworkStep({ network, bridges, uplink, disabled, onChange }: NetworkStepProps) {
  return (
    <div className="cv-stack">
      <div>
        <h3 className="cv-step-title">Where the VM connects</h3>
        <p className="cv-step-lede">A VM gets one network connection and one address. It can be changed later from the VM’s Network tab.</p>
      </div>
      <NetworkPicker value={network} bridges={bridges} uplink={uplink} disabled={disabled} onChange={onChange} />
    </div>
  );
}
