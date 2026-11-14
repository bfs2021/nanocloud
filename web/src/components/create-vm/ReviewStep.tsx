import type { ReactNode } from 'react';
import type { CatalogImage, HostBridge, ImageState } from '../../types';
import { formatBytes } from '../../design';
import { useCapabilities } from '../../capabilities';
import { Button } from '../../ui/Button';
import { isDefaultCPUShape } from '../CPUShapePicker';
import { toList } from '../CloudInitExtras';
import { STEPS, type FormState, type StepId } from './state';
import './create-vm.css';

interface ReviewStepProps {
  form: FormState;
  image: CatalogImage | undefined;
  imageState: ImageState;
  bridges: HostBridge[];
  uplink: string;
  // The earliest step that no longer validates, if any. Create is refused until
  // it is fixed, and the way there is one click.
  invalidStep: StepId | null;
  error: string;
  onEdit: (step: StepId) => void;
}

function Section({ id, title, onEdit, children }: { id: StepId; title: string; onEdit: (s: StepId) => void; children: ReactNode }) {
  return (
    <section className="cv-review-section" aria-label={title}>
      <div className="cv-review-head">
        <h3 className="cv-section-title">{title}</h3>
        <Button size="sm" variant="quiet" aria-label={`Edit ${title.toLowerCase()}`} onClick={() => onEdit(id)}>
          Edit
        </Button>
      </div>
      <dl className="cv-review-list">{children}</dl>
    </section>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="cv-review-row">
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  );
}

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

// ReviewStep says, in plain words, what pressing Create VM will do. Each part
// carries an Edit button back to the step that owns it. The password is
// reported as set or not, never shown.
export function ReviewStep({ form, image, imageState, bridges, uplink, invalidStep, error, onEdit }: ReviewStepProps) {
  const caps = useCapabilities();

  const firmware = !caps.uefi_toggle
    ? 'UEFI (this host always boots VMs with UEFI)'
    : form.firmware === 'uefi'
      ? ['UEFI', form.secureBoot && 'secure boot', form.tpm && 'emulated TPM 2.0'].filter(Boolean).join(', ')
      : form.tpm
        ? 'BIOS, emulated TPM 2.0'
        : 'BIOS';

  const shape = form.cpuShape;
  const cpuNotes = [
    shape.mode === 'host-passthrough' && 'the host’s exact CPU',
    shape.topology && `${shape.topology.sockets} × ${shape.topology.cores} × ${shape.topology.threads} sockets, cores and threads`,
    shape.cpu_set.trim() && `pinned to host CPUs ${shape.cpu_set.trim()}`,
  ].filter(Boolean);
  const devices = form.devices.pci.length + form.devices.usb.length;

  const { extras } = form;
  const packages = toList(extras.packages, true).length;
  const commands = toList(extras.runcmd, false).length;
  const files = extras.writeFiles.filter(f => f.path.trim()).length;
  const extraNotes = [
    extras.githubUser.trim() && `keys from GitHub user ${extras.githubUser.trim()}`,
    packages > 0 && plural(packages, 'package'),
    commands > 0 && plural(commands, 'command'),
    files > 0 && plural(files, 'file'),
  ].filter(Boolean);
  const hasLogin = form.password !== '' || form.sshKey.trim() !== '' || extras.githubUser.trim() !== '';

  const net = form.network;
  const bridgeName = net.bridge || bridges[0]?.name || 'the host bridge';
  const network =
    net.mode === 'nat'
      ? 'NAT. A private network inside this machine.'
      : net.vlan_id > 0
        ? `Your network (bridge) on VLAN ${net.vlan_id}. nano-cloud creates ${uplink}.${net.vlan_id} and nc-vlan${net.vlan_id} for it.`
        : `Your network (bridge ${bridgeName}). The VM gets an address from your router.`;

  return (
    <div className="cv-stack">
      <div>
        <h3 className="cv-step-title">Review</h3>
        <p className="cv-step-lede">Check everything below, then create the VM. Use Edit to change a part.</p>
      </div>

      {invalidStep && (
        <div className="cv-alert" role="alert">
          <span>The {STEPS.find(s => s.id === invalidStep)?.label.toLowerCase()} step needs attention before this VM can be created.</span>
          <Button size="sm" onClick={() => onEdit(invalidStep)}>
            Go to {STEPS.find(s => s.id === invalidStep)?.label.toLowerCase()}
          </Button>
        </div>
      )}

      <Section id="image" title="Image" onEdit={onEdit}>
        <Row label="Boots from">{image ? image.name : 'No image chosen'}</Row>
        {image && (
          <Row label="Download">
            {imageState === 'cached'
              ? 'Already downloaded.'
              : imageState === 'pulling'
                ? 'Downloading now. Creating waits for it to finish.'
                : `Downloads first (${formatBytes(image.size_bytes)}), then the VM is created.`}
          </Row>
        )}
      </Section>

      <Section id="resources" title="Resources" onEdit={onEdit}>
        <Row label="Name">{form.name.trim()}</Row>
        <Row label="Size">
          {plural(form.cpu, 'vCPU')}, {form.ram} GB memory, {form.disk} GB disk
        </Row>
        <Row label="Storage pool">{form.pool || 'Default pool'}</Row>
        <Row label="Firmware">{firmware}</Row>
        {cpuNotes.length > 0 && <Row label="CPU">{cpuNotes.join(', ')}</Row>}
        {!isDefaultCPUShape(shape) && cpuNotes.length === 0 && <Row label="CPU">Custom settings</Row>}
        {devices > 0 && <Row label="Host devices">{plural(devices, 'device')} passed through</Row>}
      </Section>

      <Section id="access" title="Access" onEdit={onEdit}>
        <Row label="Login">
          {form.username || 'No username'}
          {form.password ? ', with a password' : ', no password'}
        </Row>
        <Row label="SSH key">{form.sshKey.trim() ? 'One key added.' : 'None added.'}</Row>
        {extraNotes.length > 0 && <Row label="First boot">{extraNotes.join(', ')}</Row>}
      </Section>

      <Section id="network" title="Network" onEdit={onEdit}>
        <Row label="Connection">{network}</Row>
      </Section>

      {!hasLogin && (
        <p className="cv-note">
          No password, SSH key or GitHub keys are set, so you may not be able to log in to this VM. Edit access to add one.
        </p>
      )}

      {error && (
        <div className="cv-alert" role="alert">
          {error}
        </div>
      )}
    </div>
  );
}
