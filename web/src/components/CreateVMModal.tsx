import { useEffect, useMemo, useRef, useState } from 'react';
import type { CatalogImage, HealthCheck, HostBridge, StoragePool, VMTemplate } from '../types';
import { createVM } from '../api';
import { EMPTY_EXTRAS } from './CloudInitExtras';
import { useConfirm } from '../ui/ConfirmDialog';
import { Button } from '../ui/Button';
import { Dialog } from '../ui/Dialog';
import { AccessStep } from './create-vm/AccessStep';
import { firmwareOptions } from './create-vm/firmware';
import { ImageStep } from './create-vm/ImageStep';
import { NetworkStep } from './create-vm/NetworkStep';
import { ResourcesStep, type OpenSections } from './create-vm/ResourcesStep';
import { ReviewStep } from './create-vm/ReviewStep';
import { Stepper } from './create-vm/Stepper';
import { useCreatePipeline } from './create-vm/useCreatePipeline';
import { useHostInventory } from './create-vm/useHostInventory';
import {
  STEPS,
  buildRequest,
  clampToLimits,
  firstInvalidStep,
  hasErrors,
  hostLimits,
  initialForm,
  stepIndex,
  validateStep,
  type FormState,
  type StepId,
} from './create-vm/state';
import { Spinner } from '../ui/Spinner';
import './create-vm/create-vm.css';

// The picker lives with the wizard's other files but is also mounted by a VM's
// Network tab, which imports it from here.
export { NetworkPicker } from './create-vm/NetworkPicker';

interface CreateVMModalProps {
  catalog: CatalogImage[];
  pools: StoragePool[];
  bridges: HostBridge[];
  // The host's default-route interface, from GET /network/interfaces. Passed
  // straight through to NetworkPicker — empty means no VLAN can be offered.
  uplink: string;
  // The GET /system/deps results Settings renders. UEFI and TPM are host
  // capabilities, so the wizard offers them only when these say so.
  readinessChecks: HealthCheck[];
  // The host's size, from GET /system. The vCPU and memory sliders stop there;
  // 0 or absent keeps their fixed ceilings.
  hostCPUs?: number;
  hostMemoryMB?: number;
  // Owned by App and shared with ImagesView, so a download's progress survives
  // this modal closing.
  pullProgress: Record<string, number>;
  pullErrors: Record<string, string>;
  onPullImage: (imageId: string) => void;
  onCancelPullImage: (imageId: string) => void;
  onClose: () => void;
  onCreated: () => void;
}

export function CreateVMModal({
  catalog,
  pools,
  bridges,
  uplink,
  readinessChecks,
  hostCPUs: hostCPUsProp,
  hostMemoryMB,
  pullProgress,
  pullErrors,
  onPullImage,
  onCancelPullImage,
  onClose,
  onCreated,
}: CreateVMModalProps) {
  const pipeline = useCreatePipeline({ catalog, pullProgress, pullErrors });
  const [confirm, confirmElement] = useConfirm();
  const bodyRef = useRef<HTMLDivElement>(null);

  const defaultPool = pools.find(p => p.default)?.name || pools[0]?.name || '';
  const [form, setForm] = useState<FormState>(() => initialForm(defaultPool));
  const [step, setStep] = useState<StepId>('image');
  // Set by an Edit link on Review, so the way back is one click rather than
  // Continue through every step in between.
  const [fromReview, setFromReview] = useState(false);
  // Whether the person has changed anything themselves. The effects below that
  // tidy the form (default pool, secure boot) deliberately do not count, so
  // opening and closing the wizard never asks to discard nothing.
  const [touched, setTouched] = useState(false);

  // The template a prefill came from, only so the picker can show it selected.
  // From then on the form is the source of truth.
  const [templateName, setTemplateName] = useState('');
  const [open, setOpen] = useState<OpenSections>({ cpu: false, devices: false, provisioning: false });

  const limits = useMemo(() => hostLimits(hostCPUsProp, hostMemoryMB), [hostCPUsProp, hostMemoryMB]);
  const inventory = useHostInventory({ wantCPUs: open.cpu, wantDevices: open.devices, knownCPUs: limits.cpus });
  const ctx = { limits, hostCPUs: inventory.hostCPUs, bridges };

  const distro = catalog.find(i => i.id === form.imageId)?.os;
  const secureBootOk = firmwareOptions(readinessChecks, form.firmware, distro).secureBoot.available;

  // Keep the form from holding a combination the API rejects. Turning UEFI off
  // (or picking Alpine) clears secure boot, whose toggle is then disabled and
  // couldn't be used to undo it.
  useEffect(() => {
    if (form.secureBoot && !secureBootOk) setForm(f => ({ ...f, secureBoot: false }));
  }, [form.secureBoot, secureBootOk]);

  useEffect(() => {
    if (defaultPool && !form.pool) setForm(f => ({ ...f, pool: defaultPool }));
  }, [defaultPool, form.pool]);

  // A new step starts at the top and takes focus. Otherwise focus is lost when
  // Continue becomes Create VM, and Escape and the Tab trap stop working.
  const shownStep = useRef(step);
  useEffect(() => {
    if (shownStep.current === step) return;
    shownStep.current = step;
    bodyRef.current?.scrollTo?.({ top: 0 });
    bodyRef.current?.focus();
  }, [step]);

  function update<K extends keyof FormState>(k: K, v: FormState[K]) {
    setTouched(true);
    setForm(f => ({ ...f, [k]: v }));
  }

  // applyTemplate fills every step from a saved configuration, except the name,
  // which must be unique, and the password, which is never stored.
  function applyTemplate(t: VMTemplate | null) {
    setTemplateName(t?.name ?? '');
    if (!t) return;
    setTouched(true);

    const { spec } = t;
    setForm(f => ({
      ...f,
      imageId: spec.image_id,
      // A template saved on a bigger host can be replayed on a smaller one.
      ...clampToLimits({ cpu: spec.vcpus || f.cpu, ram: spec.memory_mb ? Math.max(1, Math.round(spec.memory_mb / 1024)) : f.ram }, limits),
      disk: spec.disk_gb || f.disk,
      // A template saved on a host with a bridge can be replayed on one without.
      network:
        spec.network_mode === 'bridge' && bridges.length > 0
          ? { mode: 'bridge', bridge: bridges[0].name, vlan_id: 0 }
          : { mode: 'nat', bridge: '', vlan_id: 0 },
      firmware: spec.firmware === 'uefi' ? 'uefi' : 'bios',
      secureBoot: spec.secure_boot,
      tpm: spec.tpm,
      cpuShape: spec.cpu,
      devices: { pci: [...spec.pci_devices], usb: [...spec.usb_devices] },
      username: spec.cloud_init.username || f.username,
      // The form carries one key and the API takes one key, so a template with
      // several can only restore the first. The rest go in after the VM exists.
      sshKey: spec.cloud_init.ssh_keys[0] ?? f.sshKey,
      extras: {
        ...EMPTY_EXTRAS,
        packages: spec.cloud_init.packages.join('\n'),
        runcmd: spec.cloud_init.runcmd.join('\n'),
        writeFiles: spec.cloud_init.write_files.map(w => ({ ...w })),
      },
    }));

    // Open the sections a template filled in, so nothing it changed is hidden
    // behind a collapsed header nobody opens.
    setOpen(o => ({
      cpu: o.cpu || spec.cpu.mode !== 'host-model' || !!spec.cpu.topology || !!spec.cpu.cpu_set,
      devices: o.devices || spec.pci_devices.length + spec.usb_devices.length > 0,
      provisioning:
        o.provisioning || spec.cloud_init.packages.length + spec.cloud_init.runcmd.length + spec.cloud_init.write_files.length > 0,
    }));
  }

  // Escape and the backdrop both land here. A filled-in form is not thrown
  // away by a stray click; an untouched one closes straight away.
  async function requestClose() {
    if (pipeline.busy) return;
    if (touched) {
      const discard = await confirm({
        title: 'Discard this VM?',
        body: 'Everything you have chosen so far will be lost.',
        confirmLabel: 'Discard',
        cancelLabel: 'Keep editing',
      });
      if (!discard) return;
    }
    onClose();
  }

  function create() {
    if (!form.name.trim() || !form.imageId) return;
    void pipeline.run(form.imageId, async () => {
      await createVM(buildRequest(form));
      onCreated();
      onClose();
    });
  }

  function edit(target: StepId) {
    setFromReview(true);
    setStep(target);
  }

  const index = stepIndex(step);
  const errors = validateStep(step, form, ctx);
  const blocked = hasErrors(errors);
  const invalidStep = firstInvalidStep(form, ctx);
  const onReview = step === 'review';
  const busy = pipeline.busy;

  function goTo(target: StepId) {
    if (target === 'review') setFromReview(false);
    setStep(target);
  }

  const selectedImage = catalog.find(i => i.id === form.imageId);

  return (
    <>
      <Dialog title="Create virtual machine" onClose={requestClose} locked={busy} width={720}>
        <div className="cv-wizard">
          <Stepper current={step} locked={busy} onGo={goTo} />

          <div
            className="cv-body"
            ref={bodyRef}
            tabIndex={-1}
            role="group"
            aria-label={`Step ${index + 1} of ${STEPS.length}: ${STEPS[index].label}`}
          >
            {step === 'image' && (
              <ImageStep
                catalog={catalog}
                imageId={form.imageId}
                templateName={templateName}
                stateOf={pipeline.effState}
                progressOf={pipeline.progressOf}
                errorOf={pipeline.errorOf}
                onSelect={id => update('imageId', id)}
                onTemplate={applyTemplate}
                onPull={onPullImage}
                onCancelPull={onCancelPullImage}
              />
            )}
            {step === 'resources' && (
              <ResourcesStep
                form={form}
                update={update}
                errors={errors}
                limits={limits}
                hostCPUs={inventory.hostCPUs}
                hostDevices={inventory.devices}
                pools={pools}
                readinessChecks={readinessChecks}
                distro={distro}
                open={open}
                setOpen={setOpen}
                disabled={busy}
              />
            )}
            {step === 'access' && (
              <AccessStep
                form={form}
                update={update}
                provisioningOpen={open.provisioning}
                onToggleProvisioning={() => setOpen(o => ({ ...o, provisioning: !o.provisioning }))}
                disabled={busy}
              />
            )}
            {step === 'network' && (
              <NetworkStep network={form.network} bridges={bridges} uplink={uplink} disabled={busy} onChange={n => update('network', n)} />
            )}
            {step === 'review' && (
              <ReviewStep
                form={form}
                image={selectedImage}
                imageState={selectedImage ? pipeline.effState(selectedImage) : 'idle'}
                bridges={bridges}
                uplink={uplink}
                invalidStep={invalidStep}
                error={pipeline.error}
                onEdit={edit}
              />
            )}
          </div>

          <div className="cv-footer">
            {busy && (
              <div className="cv-busy" role="status">
                <span className="cv-busy-label">
                  <Spinner size={11} track="var(--nd-accent-line)" />
                  {pipeline.phase === 'downloading' ? 'Downloading base image…' : 'Creating VM…'}
                </span>
                {pipeline.phase === 'downloading' && (
                  <span className="cv-busy-stop">
                    {Math.round(pipeline.progress)}%
                    <Button size="sm" variant="quiet" onClick={() => pipeline.cancel(form.imageId)}>
                      Stop
                    </Button>
                  </span>
                )}
                {pipeline.phase === 'downloading' && (
                  <div className="cv-progress">
                    <div style={{ width: `${pipeline.progress}%` }} />
                  </div>
                )}
              </div>
            )}

            <div className="cv-actions">
              <Button variant="quiet" disabled={busy} onClick={onClose}>
                Cancel
              </Button>
              <span className="cv-spacer" />
              {index > 0 && !busy && <Button onClick={() => goTo(STEPS[index - 1].id)}>Back</Button>}
              {!onReview && fromReview && (
                <Button variant="primary" disabled={blocked} onClick={() => goTo('review')}>
                  Back to review
                </Button>
              )}
              {!onReview && !fromReview && (
                <Button variant="primary" disabled={blocked} onClick={() => goTo(STEPS[index + 1].id)}>
                  Continue
                </Button>
              )}
              {onReview && (
                <Button variant="primary" busy={busy} disabled={invalidStep !== null} onClick={create}>
                  {pipeline.phase === 'downloading' ? 'Downloading…' : pipeline.phase === 'creating' ? 'Creating…' : 'Create VM'}
                </Button>
              )}
            </div>
          </div>
        </div>
      </Dialog>
      {confirmElement}
    </>
  );
}
