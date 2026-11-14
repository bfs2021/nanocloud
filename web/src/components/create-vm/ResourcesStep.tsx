import type { Dispatch, SetStateAction } from 'react';
import type { HealthCheck, HostDeviceReport, StoragePool } from '../../types';
import { useCapabilities } from '../../capabilities';
import { Field, Input, Select } from '../../ui/Field';
import { CPUShapePicker, isDefaultCPUShape } from '../CPUShapePicker';
import { DevicePicker } from '../DevicePicker';
import { Disclosure, Toggle } from './controls';
import { firmwareOptions } from './firmware';
import { DEFAULT_MAX_CPUS, DEFAULT_MAX_MEMORY_GB, type FieldErrors, type FormState, type HostLimits } from './state';
import './create-vm.css';

export interface OpenSections {
  cpu: boolean;
  devices: boolean;
  provisioning: boolean;
}

interface ResourcesStepProps {
  form: FormState;
  update: <K extends keyof FormState>(k: K, v: FormState[K]) => void;
  errors: FieldErrors;
  limits: HostLimits;
  hostCPUs: number;
  hostDevices: HostDeviceReport | null;
  pools: StoragePool[];
  readinessChecks: HealthCheck[];
  distro: string | undefined;
  open: OpenSections;
  setOpen: Dispatch<SetStateAction<OpenSections>>;
  disabled: boolean;
}

// RangeField is a 1-to-max slider. The readout beside it is the value; the
// hint says where the ceiling came from, so a cap never looks arbitrary.
function RangeField({
  label,
  value,
  unit,
  max,
  hint,
  error,
  onChange,
}: {
  label: string;
  value: number;
  unit: string;
  max: number;
  hint: string;
  error?: string;
  onChange: (v: number) => void;
}) {
  return (
    <Field label={label} hint={hint} error={error}>
      <div className="cv-range-row">
        <Input
          type="range"
          className="cv-range"
          min={1}
          max={max}
          step={1}
          value={value}
          aria-valuetext={`${value} ${unit}`}
          onChange={e => onChange(Number(e.target.value))}
        />
        <span className="cv-range-value">
          {value} {unit}
        </span>
      </div>
    </Field>
  );
}

export function ResourcesStep({
  form,
  update,
  errors,
  limits,
  hostCPUs,
  hostDevices,
  pools,
  readinessChecks,
  distro,
  open,
  setOpen,
  disabled,
}: ResourcesStepProps) {
  const caps = useCapabilities();
  const toggle = (k: keyof OpenSections) => setOpen(o => ({ ...o, [k]: !o[k] }));

  const { uefi, swtpm, secureBoot } = firmwareOptions(readinessChecks, form.firmware, distro);

  const maxCPUs = limits.cpus || DEFAULT_MAX_CPUS;
  const maxMemory = limits.memoryGB || DEFAULT_MAX_MEMORY_GB;
  const deviceCount = form.devices.pci.length + form.devices.usb.length;

  return (
    <div className="cv-stack">
      <Field label="VM name" error={errors.name}>
        <Input value={form.name} placeholder="my-vm" onChange={e => update('name', e.target.value)} />
      </Field>

      <RangeField
        label="CPU cores"
        value={form.cpu}
        unit="vCPU"
        max={maxCPUs}
        hint={limits.cpus ? `This host has ${limits.cpus} CPUs.` : 'Up to 16 vCPUs.'}
        error={errors.cpu}
        onChange={v => update('cpu', v)}
      />

      <RangeField
        label="Memory"
        value={form.ram}
        unit="GB"
        max={maxMemory}
        hint={limits.memoryGB ? `This host has ${limits.memoryGB} GB of memory.` : 'Up to 64 GB.'}
        error={errors.ram}
        onChange={v => update('ram', v)}
      />

      <div className="cv-row">
        <Field label="Disk size (GB)" error={errors.disk}>
          <Input type="number" min={1} value={form.disk} onChange={e => update('disk', Number(e.target.value))} />
        </Field>

        <Field label="Storage pool">
          <Select value={form.pool} onChange={e => update('pool', e.target.value)}>
            {pools.length === 0 && <option value="">Default pool</option>}
            {pools.map(p => (
              <option key={p.name} value={p.name}>
                {p.name} — {(p.free_bytes / 1024 / 1024 / 1024).toFixed(0)} GB free of {(p.total_bytes / 1024 / 1024 / 1024).toFixed(0)} GB
              </option>
            ))}
          </Select>
        </Field>
      </div>

      {/* A backend with no firmware choice (macOS: always UEFI) says so
          instead of offering toggles it would refuse. */}
      {!caps.uefi_toggle && (
        <section className="cv-section">
          <h3 className="cv-section-title">Firmware</h3>
          <p className="cv-note">UEFI. Apple Virtualization boots every VM with UEFI; secure boot and a TPM are not available.</p>
        </section>
      )}

      {/* Firmware stays low-key: BIOS suits nearly every VM. */}
      {caps.uefi_toggle && (
        <section className="cv-section">
          <h3 className="cv-section-title">Firmware</h3>
          <p className="cv-note">
            BIOS boots every image in the catalog. UEFI is needed for secure boot, and for GPU passthrough later. It cannot be changed after
            the VM is created.
          </p>
          <div className="cv-stack cv-stack-tight">
            <Toggle
              label="UEFI firmware"
              hint="Boots the guest through OVMF instead of SeaBIOS."
              checked={form.firmware === 'uefi'}
              onChange={v => update('firmware', v ? 'uefi' : 'bios')}
              availability={uefi}
            />
            <Toggle
              label="Secure boot"
              hint="The guest will only run a signed bootloader and kernel."
              checked={form.secureBoot}
              onChange={v => update('secureBoot', v)}
              availability={secureBoot}
            />
            <Toggle
              label="Emulated TPM 2.0"
              hint="Required by Windows 11; used by Linux for measured boot and sealing LUKS keys."
              checked={form.tpm}
              onChange={v => update('tpm', v)}
              availability={swtpm}
            />
          </div>
        </section>
      )}

      {/* Collapsed: the default CPU suits nearly every VM. */}
      {caps.cpu_tuning && (
        <Disclosure
          title="Advanced — CPU model, topology and pinning"
          summary={isDefaultCPUShape(form.cpuShape) ? undefined : 'customised'}
          open={open.cpu}
          onToggle={() => toggle('cpu')}
        >
          <CPUShapePicker
            value={form.cpuShape}
            vcpus={form.cpu}
            hostCPUs={hostCPUs}
            disabled={disabled}
            onChange={next => update('cpuShape', next)}
          />
        </Disclosure>
      )}

      {/* Collapsed, and last: passthrough is for a small minority of VMs. */}
      {caps.passthrough && (
        <Disclosure
          title="Advanced — host devices"
          summary={deviceCount > 0 ? `${deviceCount} selected` : undefined}
          open={open.devices}
          onToggle={() => toggle('devices')}
        >
          <p className="cv-note">
            Assign a whole PCI or USB device from this host to the guest. This can also be changed later from the VM’s Hardware tab.
          </p>
          <DevicePicker report={hostDevices} selection={form.devices} onChange={next => update('devices', next)} />
        </Disclosure>
      )}
    </div>
  );
}
