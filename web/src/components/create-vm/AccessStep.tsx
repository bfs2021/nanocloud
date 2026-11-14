import { Field, Input, Textarea } from '../../ui/Field';
import { CloudInitExtras, hasExtras } from '../CloudInitExtras';
import { Disclosure } from './controls';
import type { FormState } from './state';
import './create-vm.css';

interface AccessStepProps {
  form: FormState;
  update: <K extends keyof FormState>(k: K, v: FormState[K]) => void;
  provisioningOpen: boolean;
  onToggleProvisioning: () => void;
  disabled: boolean;
}

// AccessStep is how someone gets into the VM once it boots: a login, and
// optionally keys and first-boot provisioning. All of it is applied by
// cloud-init once, so none of it can be changed after the VM exists.
export function AccessStep({ form, update, provisioningOpen, onToggleProvisioning, disabled }: AccessStepProps) {
  return (
    <div className="cv-stack">
      <div>
        <h3 className="cv-step-title">How you will log in</h3>
        <p className="cv-step-lede">
          Set on the VM’s first boot by <span className="cv-mono">cloud-init</span>.
        </p>
      </div>

      <Field label="Username">
        <Input value={form.username} autoComplete="off" onChange={e => update('username', e.target.value)} />
      </Field>

      <Field label="Password" hint="Leave empty if you add an SSH key below.">
        <Input
          type="password"
          value={form.password}
          autoComplete="new-password"
          placeholder="••••••••"
          onChange={e => update('password', e.target.value)}
        />
      </Field>

      <Field label="SSH public key">
        <Textarea
          value={form.sshKey}
          rows={4}
          spellCheck={false}
          className="cv-mono"
          placeholder="ssh-ed25519 AAAA…"
          onChange={e => update('sshKey', e.target.value)}
        />
      </Field>

      {/* Collapsed, like the other advanced sections. It all runs once, at first
          boot, and the disclosure says so. */}
      <Disclosure
        title="Advanced — packages, commands, files, GitHub keys"
        summary={hasExtras(form.extras) ? 'configured' : undefined}
        open={provisioningOpen}
        onToggle={onToggleProvisioning}
      >
        <p className="cv-note">
          Applied once, on this VM’s first boot. cloud-init cannot re-run them, so none of it can be changed after the VM is created.
        </p>
        <CloudInitExtras value={form.extras} disabled={disabled} onChange={next => update('extras', next)} />
      </Disclosure>
    </div>
  );
}
