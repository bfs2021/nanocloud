import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { CreateVMModal, NetworkPicker } from './CreateVMModal';
import { MOCK_CATALOG_IMAGES, MOCK_STORAGE_POOLS, MOCK_BRIDGES, MOCK_SYSTEM_DEPS, MOCK_UPLINK } from '../mock';

vi.mock('../api', () => ({
  createVM: vi.fn().mockResolvedValue({ name: 'my-vm' }),
  pullImage: vi.fn().mockResolvedValue(undefined),
  getImageStatus: vi.fn().mockResolvedValue({ state: 'cached', progress: 100 }),
  cancelPull: vi.fn().mockResolvedValue(undefined),
  getHostDevices: vi.fn().mockResolvedValue({ iommu: { enabled: true, groups_seen: 1, message: '' }, pci: [], usb: [] }),
  getSystemInfo: vi.fn().mockResolvedValue({ hostname: 'h', cpu_percent: 0, memory_used_mb: 0, memory_total_mb: 0, cpus: 8 }),
  listTemplates: vi.fn().mockResolvedValue([]),
  fetchGitHubKeys: vi.fn(async (user: string) => {
    if (user === 'nobody') throw new Error('cloudinit: GitHub has no account named "nobody"');
    return [`ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMockKey ${user}@github`];
  }),
}));

import { createVM, listTemplates, pullImage } from '../api';
import type { HostBridge, VMNetwork, VMTemplate } from '../types';

// A catalog entry that is already cached, so Create VM goes straight to
// createVM without the download pipeline.
const cachedImage = MOCK_CATALOG_IMAGES.find(i => i.state === 'cached')!;

// The required props at ordinary mock values, so a test can override just the
// one it cares about.
const baseProps = {
  catalog: MOCK_CATALOG_IMAGES,
  pools: MOCK_STORAGE_POOLS,
  bridges: MOCK_BRIDGES,
  uplink: MOCK_UPLINK,
  readinessChecks: MOCK_SYSTEM_DEPS.checks,
  pullProgress: {},
  pullErrors: {},
  onPullImage: vi.fn(),
  onCancelPullImage: vi.fn(),
  onClose: vi.fn(),
  onCreated: vi.fn(),
};

function renderModal(checks = MOCK_SYSTEM_DEPS.checks) {
  const onClose = vi.fn();
  const onCreated = vi.fn();
  render(<CreateVMModal {...baseProps} readinessChecks={checks} onClose={onClose} onCreated={onCreated} />);
  return { onClose, onCreated };
}

type User = ReturnType<typeof userEvent.setup>;

async function continueTimes(user: User, n: number) {
  for (let i = 0; i < n; i++) await user.click(screen.getByRole('button', { name: 'Continue' }));
}

// Drives the wizard from the Image step to Network, where NetworkPicker lives:
// pick the already-cached image, then accept Resources and Access as they are.
async function goToNetworkStep() {
  const user = userEvent.setup();
  await user.click(screen.getByText(cachedImage.name));
  await continueTimes(user, 3);
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('CreateVMModal wizard', () => {
  it('disables Continue until an image is selected', async () => {
    const user = userEvent.setup();
    renderModal();
    const next = screen.getByRole('button', { name: 'Continue' });
    expect(next).toBeDisabled();
    await user.click(screen.getByText(cachedImage.name));
    expect(next).toBeEnabled();
  });

  it('requires a VM name on the resources step', async () => {
    const user = userEvent.setup();
    renderModal();
    await user.click(screen.getByText(cachedImage.name));
    await user.click(screen.getByRole('button', { name: 'Continue' }));

    const nameInput = screen.getByPlaceholderText('my-vm');
    const next = screen.getByRole('button', { name: 'Continue' });
    await user.clear(nameInput);
    expect(next).toBeDisabled();
    await user.type(nameInput, 'e2e-box');
    expect(next).toBeEnabled();
  });

  it('supports navigating back a step', async () => {
    const user = userEvent.setup();
    renderModal();
    await user.click(screen.getByText(cachedImage.name));
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    expect(screen.getByPlaceholderText('my-vm')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Back' }));
    // Back on step 1: the image grid is visible again.
    expect(screen.getByText('Base image')).toBeInTheDocument();
  });

  it('submits the expected payload and closes on success', async () => {
    const user = userEvent.setup();
    const { onClose, onCreated } = renderModal();

    // Step 1: pick a cached image.
    await user.click(screen.getByText(cachedImage.name));
    await user.click(screen.getByRole('button', { name: 'Continue' }));

    // Step 2: rename the VM, keep default sliders.
    const nameInput = screen.getByPlaceholderText('my-vm');
    await user.clear(nameInput);
    await user.type(nameInput, 'test-vm');

    // Access and Network at their defaults, then Review is the only place to
    // create from.
    await continueTimes(user, 3);
    await user.click(screen.getByRole('button', { name: 'Create VM' }));

    await waitFor(() => expect(createVM).toHaveBeenCalledTimes(1));
    expect(createVM).toHaveBeenCalledWith({
      name: 'test-vm',
      image_id: cachedImage.id,
      vcpus: 2,
      memory_mb: 4 * 1024,
      disk_gb: 20,
      pool_name: MOCK_STORAGE_POOLS.find(p => p.default)!.name,
      network_mode: 'nat',
      cloud_init: { username: 'admin', password: '', ssh_public_key: '' },
    });
    expect(onCreated).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });

  it('surfaces a create error without closing the modal', async () => {
    vi.mocked(createVM).mockRejectedValueOnce(new Error('disk pool full'));
    const user = userEvent.setup();
    const { onClose } = renderModal();

    await user.click(screen.getByText(cachedImage.name));
    await continueTimes(user, 4);
    await user.click(screen.getByRole('button', { name: 'Create VM' }));

    expect(await screen.findByText('disk pool full')).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });
});

// Firmware is the first thing in the wizard the host can veto. A silently
// disabled or silently accepted checkbox fails the same way: a black screen at
// first boot.
describe('CreateVMModal firmware', () => {
  const alpine = MOCK_CATALOG_IMAGES.find(i => i.os === 'alpine' && i.state === 'cached')!;
  const ubuntu = MOCK_CATALOG_IMAGES.find(i => i.os === 'ubuntu' && i.state === 'cached')!;

  // Walks to step 2, where the firmware toggles live.
  async function openResources(user: ReturnType<typeof userEvent.setup>, imageName: string) {
    await user.click(screen.getByText(imageName));
    await user.click(screen.getByRole('button', { name: 'Continue' }));
  }

  const uefiBox = () => screen.getByRole('checkbox', { name: /UEFI firmware/ });
  const secureBox = () => screen.getByRole('checkbox', { name: /Secure boot/ });
  const tpmBox = () => screen.getByRole('checkbox', { name: /Emulated TPM/ });

  it('sends UEFI, secure boot and TPM only when they are turned on', async () => {
    const user = userEvent.setup();
    // MOCK_SYSTEM_DEPS has swtpm missing, which is the point of the mock —
    // override it here so all three can actually be exercised.
    renderModal([
      { name: 'ovmf', ok: true, required: false, message: 'UEFI firmware present' },
      { name: 'swtpm', ok: true, required: false, message: 'swtpm installed' },
    ]);
    await openResources(user, ubuntu.name);

    await user.click(uefiBox());
    await user.click(secureBox());
    await user.click(tpmBox());
    await continueTimes(user, 3);
    await user.click(screen.getByRole('button', { name: 'Create VM' }));

    await waitFor(() => expect(createVM).toHaveBeenCalledTimes(1));
    expect(vi.mocked(createVM).mock.calls[0][0]).toMatchObject({
      firmware: 'uefi',
      secure_boot: true,
      tpm: true,
    });
  });

  it('offers no firmware option the host has not reported', async () => {
    const user = userEvent.setup();
    // An older daemon, or one whose checks have not loaded yet. Assuming the
    // host can do UEFI here fails at define time, after the disk and the seed
    // ISO have already been written.
    renderModal([]);
    await openResources(user, ubuntu.name);

    expect(uefiBox()).toBeDisabled();
    expect(tpmBox()).toBeDisabled();
    expect(screen.getAllByText(/has not reported/i).length).toBeGreaterThan(0);
  });

  it('shows the install command when the host is missing swtpm', async () => {
    const user = userEvent.setup();
    renderModal();
    await openResources(user, ubuntu.name);

    expect(tpmBox()).toBeDisabled();
    // The remedy, not just the diagnosis: this is the same contract the
    // readiness banner keeps, and the only actionable thing on the screen.
    expect(screen.getByText(/apt install swtpm swtpm-tools/)).toBeInTheDocument();
  });

  it('requires UEFI before secure boot can be selected', async () => {
    const user = userEvent.setup();
    renderModal();
    await openResources(user, ubuntu.name);

    expect(secureBox()).toBeDisabled();
    expect(screen.getByText(/Secure boot is a UEFI feature/)).toBeInTheDocument();

    await user.click(uefiBox());
    expect(secureBox()).toBeEnabled();
  });

  it('refuses secure boot on Alpine and says why', async () => {
    const user = userEvent.setup();
    renderModal();
    await openResources(user, alpine.name);

    await user.click(uefiBox());
    // UEFI alone is fine on Alpine; only the signature check is fatal, and the
    // reason names the missing piece rather than just refusing.
    expect(uefiBox()).toBeChecked();
    expect(secureBox()).toBeDisabled();
    expect(screen.getByText(/does not ship a signed bootloader/)).toBeInTheDocument();
  });

  // The combination the API rejects with a 400. Once UEFI is off the
  // secure-boot toggle is disabled, so the user couldn't clear it.
  it('clears secure boot when UEFI is switched back off', async () => {
    const user = userEvent.setup();
    renderModal();
    await openResources(user, ubuntu.name);

    await user.click(uefiBox());
    await user.click(secureBox());
    expect(secureBox()).toBeChecked();

    await user.click(uefiBox());
    await waitFor(() => expect(secureBox()).not.toBeChecked());

    await continueTimes(user, 3);
    await user.click(screen.getByRole('button', { name: 'Create VM' }));
    await waitFor(() => expect(createVM).toHaveBeenCalledTimes(1));

    const payload = vi.mocked(createVM).mock.calls[0][0];
    expect(payload).not.toHaveProperty('secure_boot');
    expect(payload).not.toHaveProperty('firmware');
  });
});

// The CPU section is the one place in the wizard where a plausible number
// produces a VM that will not define at all — libvirt rejects a topology whose
// product is not the vCPU count, and the rejection names neither field.
describe('CreateVMModal CPU shape', () => {
  const ubuntu = MOCK_CATALOG_IMAGES.find(i => i.os === 'ubuntu' && i.state === 'cached')!;

  async function openCPUSection(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByText(ubuntu.name));
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    await user.click(screen.getByRole('button', { name: /Advanced — CPU/ }));
  }

  // Collapsed by default; the default machine sends nothing (see the payload test).
  it('starts collapsed', async () => {
    const user = userEvent.setup();
    renderModal();
    await user.click(screen.getByText(ubuntu.name));
    await user.click(screen.getByRole('button', { name: 'Continue' }));

    expect(screen.queryByText('CPU model')).not.toBeInTheDocument();
  });

  it('blocks Continue on a topology that does not match the vCPU count', async () => {
    const user = userEvent.setup();
    renderModal();
    await openCPUSection(user);

    // The wizard's default is 2 vCPUs, so the layout opens as 2 × 1 × 1.
    await user.click(screen.getByRole('checkbox', { name: /fixed socket/i }));
    expect(screen.getByRole('button', { name: 'Continue' })).toBeEnabled();

    await user.clear(screen.getByLabelText('cores'));
    await user.type(screen.getByLabelText('cores'), '3');

    expect(screen.getByText(/is 6 vCPUs, but this VM has 2/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Continue' })).toBeDisabled();
  });

  it('sends the CPU shape only when it departs from the default', async () => {
    const user = userEvent.setup();
    renderModal();
    await openCPUSection(user);

    await user.type(screen.getByLabelText('cpu_set'), '0-3');
    await continueTimes(user, 3);
    await user.click(screen.getByRole('button', { name: 'Create VM' }));

    await waitFor(() => expect(createVM).toHaveBeenCalledTimes(1));
    expect(vi.mocked(createVM).mock.calls[0][0].cpu).toEqual({
      mode: 'host-model',
      topology: null,
      cpu_set: '0-3',
    });
  });
});

// The provisioning extras are first-boot only, so everything about them has to
// be right before the VM exists — there is no editing them afterwards.
describe('CreateVMModal cloud-init extras', () => {
  const ubuntu = MOCK_CATALOG_IMAGES.find(i => i.os === 'ubuntu' && i.state === 'cached')!;

  async function openProvisioning(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByText(ubuntu.name));
    await continueTimes(user, 2);
    await user.click(screen.getByRole('button', { name: /Advanced — packages/ }));
  }

  // The extras live on Access; creating happens two steps later, on Review.
  async function createFromAccess(user: User) {
    await continueTimes(user, 2);
    await user.click(screen.getByRole('button', { name: 'Create VM' }));
  }

  it('sends packages, commands and files in the shape the API takes', async () => {
    const user = userEvent.setup();
    renderModal();
    await openProvisioning(user);

    // Commas as well as newlines: "nginx, git" is what people type.
    await user.type(screen.getByLabelText('packages'), 'nginx, git');
    await user.type(screen.getByLabelText('run commands'), 'systemctl enable nginx');
    await user.click(screen.getByRole('button', { name: /add a file/i }));
    await user.type(screen.getByLabelText('file 1 path'), '/etc/motd');
    await user.type(screen.getByLabelText('file 1 content'), 'hello');

    await createFromAccess(user);

    await waitFor(() => expect(createVM).toHaveBeenCalledTimes(1));
    expect(vi.mocked(createVM).mock.calls[0][0].cloud_init).toMatchObject({
      packages: ['nginx', 'git'],
      runcmd: ['systemctl enable nginx'],
      write_files: [{ path: '/etc/motd', content: 'hello' }],
    });
  });

  // Opening the section without entering anything must not change the request.
  it('sends none of it when nothing was entered', async () => {
    const user = userEvent.setup();
    renderModal();
    await openProvisioning(user);
    await createFromAccess(user);

    await waitFor(() => expect(createVM).toHaveBeenCalledTimes(1));
    const ci = vi.mocked(createVM).mock.calls[0][0].cloud_init;
    for (const key of ['github_user', 'packages', 'runcmd', 'write_files']) {
      expect(ci).not.toHaveProperty(key);
    }
  });

  // Resolving the import in the daemon lets the keys be checked before the
  // first login.
  it('previews the keys a GitHub import would install', async () => {
    const user = userEvent.setup();
    renderModal();
    await openProvisioning(user);

    await user.type(screen.getByLabelText('github username'), 'octocat');
    await user.click(screen.getByRole('button', { name: 'Check' }));

    expect(await screen.findByText(/1 key will be installed/)).toBeInTheDocument();
  });

  it('shows why an import would fail rather than creating a VM nobody can log into', async () => {
    const user = userEvent.setup();
    renderModal();
    await openProvisioning(user);

    await user.type(screen.getByLabelText('github username'), 'nobody');
    await user.click(screen.getByRole('button', { name: 'Check' }));

    expect(await screen.findByText(/no account named/i)).toBeInTheDocument();
  });
});

// A template is only useful if picking one actually reaches the request. The
// assertion is on the payload rather than on the form: every field it fills in
// is editable afterwards, so what matters is what survives all three steps.
describe('CreateVMModal templates', () => {
  const saved: VMTemplate = {
    name: 'Ubuntu web server',
    created_at_unix: 1754000000,
    source_vm: 'web-01',
    spec: {
      image_id: MOCK_CATALOG_IMAGES.find(i => i.os === 'ubuntu' && i.state === 'cached')!.id,
      os: 'ubuntu',
      vcpus: 8,
      memory_mb: 8192,
      disk_gb: 60,
      network_mode: 'nat',
      firmware: '',
      secure_boot: false,
      tpm: false,
      cpu: { mode: 'host-model', topology: null, cpu_set: '' },
      pci_devices: [],
      usb_devices: [],
      cloud_init: {
        username: 'deploy',
        packages: ['nginx'],
        runcmd: ['systemctl enable nginx'],
        write_files: [],
        ssh_keys: ['ssh-ed25519 AAAAkey deploy@laptop'],
      },
    },
  };

  it('fills every step from the saved configuration', async () => {
    vi.mocked(listTemplates).mockResolvedValue([saved]);
    const user = userEvent.setup();
    renderModal();

    await user.selectOptions(await screen.findByLabelText('Start from a template'), saved.name);
    // The image the template names is selected, so Continue is live without
    // touching the grid at all.
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    expect(screen.getByText('8 vCPU')).toBeInTheDocument();

    await continueTimes(user, 3);
    await user.click(screen.getByRole('button', { name: 'Create VM' }));

    await waitFor(() => expect(createVM).toHaveBeenCalledTimes(1));
    const payload = vi.mocked(createVM).mock.calls[0][0];
    expect(payload).toMatchObject({
      image_id: saved.spec.image_id,
      vcpus: 8,
      memory_mb: 8192,
      disk_gb: 60,
    });
    expect(payload.cloud_init).toMatchObject({
      username: 'deploy',
      ssh_public_key: 'ssh-ed25519 AAAAkey deploy@laptop',
      packages: ['nginx'],
      runcmd: ['systemctl enable nginx'],
    });
  });

  // A template from a bigger machine must not leave the sliders holding a
  // number the host cannot honour: the thumb would sit at the end while the
  // form still said 8.
  it('fits a template to the host it is replayed on', async () => {
    vi.mocked(listTemplates).mockResolvedValue([saved]);
    const user = userEvent.setup();
    render(<CreateVMModal {...baseProps} hostCPUs={4} hostMemoryMB={4096} />);

    await user.selectOptions(await screen.findByLabelText('Start from a template'), saved.name);
    await user.click(screen.getByRole('button', { name: 'Continue' }));

    expect(screen.getByText('4 vCPU')).toBeInTheDocument();
    expect(screen.getByText('4 GB')).toBeInTheDocument();
  });

  // No templates means no control: an empty dropdown would explain a feature
  // nobody has used yet, in the middle of doing something else.
  it('shows nothing when none are saved', async () => {
    vi.mocked(listTemplates).mockResolvedValue([]);
    renderModal();
    await waitFor(() => expect(listTemplates).toHaveBeenCalled());
    expect(screen.queryByLabelText('Start from a template')).not.toBeInTheDocument();
  });
});

// The picker is mounted here and in a VM's Network tab, so it is tested on its
// own rather than through three wizard steps.
describe('NetworkPicker', () => {
  const bridged = { mode: 'bridge' as const, bridge: '', vlan_id: 0 };
  const twoBridges: HostBridge[] = [
    { name: 'br0', uplink: 'eth0' },
    { name: 'br-lan', uplink: 'eth1' },
  ];

  function renderPicker(bridges: HostBridge[], uplink = 'eth0', value: VMNetwork = bridged) {
    const onChange = vi.fn();
    render(<NetworkPicker value={value} bridges={bridges} uplink={uplink} onChange={onChange} />);
    return onChange;
  }

  // One bridge is not a choice, so no select is shown. Two are, but only for an
  // untagged VM: a tagged one's bridge is nc-vlan<id>.
  it('offers a bridge select only above one bridge, and only when untagged', () => {
    const { unmount } = render(
      <NetworkPicker value={bridged} bridges={[{ name: 'br0', uplink: 'eth0' }]} uplink="eth0" onChange={vi.fn()} />,
    );
    expect(screen.queryByLabelText('Host bridge')).not.toBeInTheDocument();
    unmount();

    const { unmount: unmountTwo } = render(<NetworkPicker value={bridged} bridges={twoBridges} uplink="eth0" onChange={vi.fn()} />);
    expect(screen.getByLabelText('Host bridge')).toBeInTheDocument();
    unmountTwo();

    renderPicker(twoBridges, 'eth0', { mode: 'bridge', bridge: 'br0', vlan_id: 20 });
    expect(screen.queryByLabelText('Host bridge')).not.toBeInTheDocument();
  });

  // The VLAN field is disabled only on a host with no default route;
  // nano-cloud provisions the VLAN itself.
  it('enables the VLAN field whenever the host has an uplink, regardless of bridge', async () => {
    renderPicker([{ name: 'br0', uplink: 'eth0' }], 'eth0');
    await userEvent.click(screen.getByRole('button', { name: /Advanced: VLAN tag/ }));
    expect(screen.getByLabelText('VLAN ID')).toBeEnabled();
  });

  it('disables the VLAN field when the host has no default route', async () => {
    renderPicker([{ name: 'br0', uplink: 'eth0' }], '');
    await userEvent.click(screen.getByRole('button', { name: /Advanced: VLAN tag/ }));
    const input = screen.getByLabelText('VLAN ID') as HTMLInputElement;
    expect(input).toBeDisabled();
    expect(input.title).toBe('this host has no default route to put a VLAN on');
    expect(input).toHaveAccessibleDescription(/no default route/);
  });

  // Most hosts want an untagged VM, so the tag sits behind a disclosure — but
  // a tag that is already set, or one the host requires, must not be hidden.
  describe('VLAN disclosure', () => {
    const toggle = () => screen.getByRole('button', { name: /Advanced: VLAN tag/ });

    it('is collapsed by default and opens on click, tying the help text to the field', async () => {
      const user = userEvent.setup();
      renderPicker([{ name: 'br0', uplink: 'eth0' }]);
      expect(toggle()).toHaveAttribute('aria-expanded', 'false');
      expect(screen.queryByLabelText('VLAN ID')).not.toBeInTheDocument();

      await user.click(toggle());
      expect(toggle()).toHaveAttribute('aria-expanded', 'true');
      expect(screen.getByLabelText('VLAN ID')).toHaveAccessibleDescription(/separate network that your switch keeps apart/);

      await user.click(toggle());
      expect(screen.queryByLabelText('VLAN ID')).not.toBeInTheDocument();
    });

    it('is reachable and operable from the keyboard', async () => {
      const user = userEvent.setup();
      renderPicker([{ name: 'br0', uplink: 'eth0' }]);
      toggle().focus();
      await user.keyboard('{Enter}');
      expect(toggle()).toHaveAttribute('aria-expanded', 'true');
    });

    it('is expanded when a VLAN is already set', () => {
      renderPicker([{ name: 'br0', uplink: 'eth0' }], 'eth0', { mode: 'bridge', bridge: '', vlan_id: 20 });
      expect(toggle()).toHaveAttribute('aria-expanded', 'true');
      expect(screen.getByLabelText('VLAN ID')).toHaveValue(20);
    });

    it('is expanded, and cannot be collapsed, while a tag is required', () => {
      renderPicker([], 'eth0');
      expect(toggle()).toHaveAttribute('aria-expanded', 'true');
      expect(toggle()).toBeDisabled();
      expect(screen.getByLabelText('VLAN ID')).toBeInTheDocument();
    });

    it('is not offered in NAT mode', () => {
      renderPicker([{ name: 'br0', uplink: 'eth0' }], 'eth0', { mode: 'nat', bridge: '', vlan_id: 0 });
      expect(screen.queryByRole('button', { name: /Advanced: VLAN tag/ })).not.toBeInTheDocument();
    });
  });

  // The three host shapes the choice has to explain itself in.
  describe('copy by host state', () => {
    const nat = { mode: 'nat' as const, bridge: '', vlan_id: 0 };

    it('explains NAT and bridge in plain words, with NAT recommended', () => {
      renderPicker([{ name: 'br0', uplink: 'eth0' }], 'eth0', nat);
      expect(screen.getByText('Recommended')).toBeInTheDocument();
      expect(screen.getByText(/A private network inside this machine/)).toBeInTheDocument();
      expect(screen.getByText(/gets an address from your router/)).toBeInTheDocument();
      expect(screen.getByLabelText(/Your network \(bridge\)/)).toBeEnabled();
      expect(screen.queryByText(/Set up a bridge on this host first/)).not.toBeInTheDocument();
    });

    it('points at the networking guide when the host has no bridge to offer', () => {
      renderPicker([], '', nat);
      expect(screen.getByLabelText(/Your network \(bridge\)/)).toBeDisabled();
      expect(screen.getByText(/Set up a bridge on this host first/)).toBeInTheDocument();
      const link = screen.getByRole('link', { name: /How to set up a bridge/ });
      expect(link).toHaveAttribute('href', 'https://nanocloud-io.github.io/nanocloud/docs/networking.html#bridge');
      expect(link).toHaveAttribute('target', '_blank');
    });

    it('still lets a bridgeless host with an uplink choose bridge, and says a tag is needed', () => {
      renderPicker([], 'eth0', nat);
      expect(screen.getByLabelText(/Your network \(bridge\)/)).toBeEnabled();
      expect(screen.getByText(/a bridged VM needs a VLAN tag/)).toBeInTheDocument();
    });
  });

  // An ordinary host reports no bridges: eth0 isn't one, and docker0 and virbr0
  // have no physical port. It can still carry a tagged VM.
  it('allows Bridge mode on a host with no bridges but an uplink', async () => {
    const user = userEvent.setup();
    const onChange = renderPicker([], 'eth0', { mode: 'nat', bridge: '', vlan_id: 0 });

    await user.click(screen.getByText('Your network (bridge)'));
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ mode: 'bridge' }));
  });

  it('refuses Bridge mode only when there is neither a bridge nor an uplink', async () => {
    const user = userEvent.setup();
    const onChange = renderPicker([], '', { mode: 'nat', bridge: '', vlan_id: 0 });

    await user.click(screen.getByText('Your network (bridge)'));
    expect(onChange).not.toHaveBeenCalled();
  });

  // Reachable is not the same as valid: with no host bridge, an untagged VM is
  // what the server answers 422 to, so the requirement has to be on screen
  // before the request rather than in the error after it.
  it('says a VLAN is required when bridge mode is reached without a host bridge', () => {
    const { unmount } = render(<NetworkPicker value={bridged} bridges={[]} uplink="eth0" onChange={vi.fn()} />);
    expect(screen.getByText(/no bridge to your network yet/i)).toBeInTheDocument();
    unmount();

    // Tagged, so nc-vlan<id> is the bridge — nothing is missing.
    render(<NetworkPicker value={{ mode: 'bridge', bridge: '', vlan_id: 20 }} bridges={[]} uplink="eth0" onChange={vi.fn()} />);
    expect(screen.queryByText(/no bridge to your network yet/i)).not.toBeInTheDocument();
  });
});

// These exercise the picker through the wizard rather than in isolation,
// since the statement-of-intent line and the disabled reason are both about
// what the operator sees at the point they'd actually hit them.
describe('CreateVMModal network step', () => {
  it('enables the VLAN field on any host with an uplink, with no command to run', async () => {
    render(<CreateVMModal {...baseProps} bridges={[{ name: 'br0', uplink: 'eth0' }]} uplink="eth0" />);
    await goToNetworkStep();

    fireEvent.click(screen.getByText('Your network (bridge)'));
    fireEvent.click(screen.getByRole('button', { name: /Advanced: VLAN tag/ }));

    const vlan = screen.getByLabelText('VLAN ID') as HTMLInputElement;
    expect(vlan.disabled).toBe(false);
    expect(screen.queryByText(/vlan_filtering/)).toBeNull();
    expect(screen.queryByText(/Copy/)).toBeNull();
  });

  it('states what it will create rather than what you must run', async () => {
    render(<CreateVMModal {...baseProps} bridges={[{ name: 'br0', uplink: 'eth0' }]} uplink="eth0" />);
    await goToNetworkStep();

    fireEvent.click(screen.getByText('Your network (bridge)'));
    fireEvent.click(screen.getByRole('button', { name: /Advanced: VLAN tag/ }));
    fireEvent.change(screen.getByLabelText('VLAN ID'), { target: { value: '20' } });

    expect(screen.getByText(/will create eth0\.20 and bridge nc-vlan20/)).toBeInTheDocument();
    expect(screen.getByText(/existing network is not modified/)).toBeInTheDocument();
  });

  it('offers no VLAN when the host has no default route', async () => {
    render(<CreateVMModal {...baseProps} bridges={[{ name: 'br0', uplink: 'eth0' }]} uplink="" />);
    await goToNetworkStep();

    fireEvent.click(screen.getByText('Your network (bridge)'));
    fireEvent.click(screen.getByRole('button', { name: /Advanced: VLAN tag/ }));
    expect((screen.getByLabelText('VLAN ID') as HTMLInputElement).disabled).toBe(true);
  });

  // The request would be refused with a 422 at the very end, so the way out
  // is on this screen instead.
  it('holds Continue back while bridge mode has no bridge and no VLAN', async () => {
    render(<CreateVMModal {...baseProps} bridges={[]} uplink="eth0" />);
    const user = userEvent.setup();
    await user.click(screen.getByText(cachedImage.name));
    await continueTimes(user, 3);

    await user.click(screen.getByText('Your network (bridge)'));
    expect(screen.getByRole('button', { name: 'Continue' })).toBeDisabled();

    await user.type(screen.getByLabelText('VLAN ID'), '20');
    expect(screen.getByRole('button', { name: 'Continue' })).toBeEnabled();
  });

  it('is its own step, after Access and before Review', async () => {
    const user = userEvent.setup();
    renderModal();
    await user.click(screen.getByText(cachedImage.name));
    await continueTimes(user, 2);

    expect(screen.getByLabelText('Username')).toBeInTheDocument();
    expect(screen.queryByRole('group', { name: 'Network mode' })).not.toBeInTheDocument();

    await continueTimes(user, 1);
    expect(screen.getByRole('group', { name: 'Network mode' })).toBeInTheDocument();
    expect(screen.queryByLabelText('Username')).not.toBeInTheDocument();
  });
});

describe('CreateVMModal review', () => {
  async function toReview(user: User, name = 'web-1') {
    await user.click(screen.getByText(cachedImage.name));
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    await user.clear(screen.getByLabelText('VM name'));
    await user.type(screen.getByLabelText('VM name'), name);
    await continueTimes(user, 3);
  }

  it('offers Create VM only on the last step', async () => {
    const user = userEvent.setup();
    renderModal();
    await user.click(screen.getByText(cachedImage.name));

    for (let i = 0; i < 4; i++) {
      expect(screen.queryByRole('button', { name: 'Create VM' })).not.toBeInTheDocument();
      await user.click(screen.getByRole('button', { name: 'Continue' }));
    }
    expect(screen.getByRole('button', { name: 'Create VM' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Continue' })).not.toBeInTheDocument();
  });

  it('says in words what will be created, and never shows the password', async () => {
    const user = userEvent.setup();
    renderModal();
    await user.click(screen.getByText(cachedImage.name));
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    await user.clear(screen.getByLabelText('VM name'));
    await user.type(screen.getByLabelText('VM name'), 'web-1');
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    await user.type(screen.getByLabelText('Password'), 'hunter2');
    await continueTimes(user, 2);

    expect(screen.getByText('web-1')).toBeInTheDocument();
    expect(screen.getByText('2 vCPUs, 4 GB memory, 20 GB disk')).toBeInTheDocument();
    expect(screen.getByText(/NAT\. A private network inside this machine/)).toBeInTheDocument();
    expect(screen.getByText('admin, with a password')).toBeInTheDocument();
    expect(screen.getByText('Already downloaded.')).toBeInTheDocument();
    expect(screen.queryByText(/hunter2/)).not.toBeInTheDocument();
  });

  it('warns when nothing would let anyone log in', async () => {
    const user = userEvent.setup();
    renderModal();
    await toReview(user);
    expect(screen.getByText(/you may not be able to log in/)).toBeInTheDocument();
  });

  it('says an uncached image downloads first', async () => {
    const uncached = MOCK_CATALOG_IMAGES.find(i => i.state === 'idle')!;
    const user = userEvent.setup();
    renderModal();
    await user.click(screen.getByText(uncached.name));
    await continueTimes(user, 4);
    expect(screen.getByText(/Downloads first/)).toBeInTheDocument();
  });

  it('jumps back to the step an Edit link names, and returns in one click', async () => {
    const user = userEvent.setup();
    renderModal();
    await toReview(user);

    await user.click(screen.getByRole('button', { name: 'Edit access' }));
    const username = screen.getByLabelText('Username');
    await user.clear(username);
    await user.type(username, 'ops');

    // Not Continue through Network again: the way back is direct.
    expect(screen.queryByRole('button', { name: 'Continue' })).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Back to review' }));

    expect(screen.getByText('ops, no password')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Create VM' })).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Create VM' }));
    await waitFor(() => expect(createVM).toHaveBeenCalledTimes(1));
    expect(vi.mocked(createVM).mock.calls[0][0].cloud_init.username).toBe('ops');
  });

  it('lands on the right step from each Edit link', async () => {
    const user = userEvent.setup();
    renderModal();
    await toReview(user);

    const landing: Array<[string, () => HTMLElement]> = [
      ['Edit image', () => screen.getByRole('radiogroup')],
      ['Edit resources', () => screen.getByLabelText('VM name')],
      ['Edit network', () => screen.getByRole('group', { name: 'Network mode' })],
    ];
    for (const [link, marker] of landing) {
      await user.click(screen.getByRole('button', { name: link }));
      expect(marker()).toBeInTheDocument();
      await user.click(screen.getByRole('button', { name: 'Back to review' }));
    }
  });

  // A value that went stale after its step was passed — here a template
  // applied before the host's size arrived — must not reach the API.
  it('refuses to create while an earlier step is invalid, and points at it', async () => {
    const user = userEvent.setup();
    const { rerender } = render(<CreateVMModal {...baseProps} />);
    await user.click(screen.getByText(cachedImage.name));
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    fireEvent.change(screen.getByLabelText('CPU cores'), { target: { value: '8' } });
    await continueTimes(user, 3);

    rerender(<CreateVMModal {...baseProps} hostCPUs={4} />);

    expect(screen.getByRole('button', { name: 'Create VM' })).toBeDisabled();
    await user.click(screen.getByRole('button', { name: 'Go to resources' }));
    expect(screen.getByText('This host has 4 CPUs, so 8 is too many.')).toBeInTheDocument();
  });
});

describe('CreateVMModal closing', () => {
  const scrim = () => screen.getByRole('dialog', { name: 'Create virtual machine' }).parentElement!;

  it('closes straight away when nothing has been changed', () => {
    const { onClose } = renderModal();
    fireEvent.mouseDown(scrim());
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('dialog', { name: 'Discard this VM?' })).not.toBeInTheDocument();
  });

  it('asks before a backdrop click throws away a form that was changed', async () => {
    const user = userEvent.setup();
    const { onClose } = renderModal();
    await user.click(screen.getByText(cachedImage.name));

    fireEvent.mouseDown(scrim());
    expect(screen.getByRole('dialog', { name: 'Discard this VM?' })).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();

    await user.click(screen.getByRole('button', { name: 'Keep editing' }));
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog', { name: 'Discard this VM?' })).not.toBeInTheDocument();
    expect(screen.getByRole('radio', { name: cachedImage.name })).toBeChecked();

    fireEvent.mouseDown(scrim());
    await user.click(screen.getByRole('button', { name: 'Discard' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('asks on Escape too, and Escape on the question keeps the form', async () => {
    const user = userEvent.setup();
    const { onClose } = renderModal();
    await user.click(screen.getByText(cachedImage.name));

    await user.keyboard('{Escape}');
    expect(screen.getByRole('dialog', { name: 'Discard this VM?' })).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();

    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog', { name: 'Discard this VM?' })).not.toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it('counts a change on a later step, not just the first', async () => {
    const user = userEvent.setup();
    const { onClose } = renderModal();
    await user.click(screen.getByText(cachedImage.name));
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    await user.type(screen.getByLabelText('VM name'), '-x');
    await user.keyboard('{Escape}');

    expect(screen.getByRole('dialog', { name: 'Discard this VM?' })).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  // Continue becomes Create VM on the last step. Focus has to follow, or
  // Escape never reaches the dialog's key handler.
  it('still answers Escape on the last step', async () => {
    const user = userEvent.setup();
    const { onClose } = renderModal();
    await user.click(screen.getByText(cachedImage.name));
    await continueTimes(user, 4);

    await user.keyboard('{Escape}');
    expect(screen.getByRole('dialog', { name: 'Discard this VM?' })).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it('moves focus to the new step so it is announced and keys keep working', async () => {
    const user = userEvent.setup();
    renderModal();
    await user.click(screen.getByText(cachedImage.name));
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    expect(screen.getByRole('group', { name: 'Step 2 of 5: Resources' })).toHaveFocus();
  });

  // Cancel is the person saying so in words, so it does not second-guess.
  it('closes at once from the Cancel button', async () => {
    const user = userEvent.setup();
    const { onClose } = renderModal();
    await user.click(screen.getByText(cachedImage.name));
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

describe('CreateVMModal host limits', () => {
  async function openResources(user: User) {
    await user.click(screen.getByText(cachedImage.name));
    await user.click(screen.getByRole('button', { name: 'Continue' }));
  }

  it('caps vCPUs and memory at the host and says so', async () => {
    const user = userEvent.setup();
    render(<CreateVMModal {...baseProps} hostCPUs={4} hostMemoryMB={8192} />);
    await openResources(user);

    expect(screen.getByLabelText('CPU cores')).toHaveAttribute('max', '4');
    expect(screen.getByLabelText('Memory')).toHaveAttribute('max', '8');
    expect(screen.getByText('This host has 4 CPUs.')).toBeInTheDocument();
    expect(screen.getByText('This host has 8 GB of memory.')).toBeInTheDocument();
  });

  it.each([
    ['absent', undefined],
    ['zero', 0],
  ])('keeps the fixed ceilings when the host size is %s', async (_label, size) => {
    const user = userEvent.setup();
    render(<CreateVMModal {...baseProps} hostCPUs={size} hostMemoryMB={size} />);
    await openResources(user);

    expect(screen.getByLabelText('CPU cores')).toHaveAttribute('max', '16');
    expect(screen.getByLabelText('Memory')).toHaveAttribute('max', '64');
    expect(screen.queryByText(/This host has/)).not.toBeInTheDocument();
  });

  it('rounds host memory down to whole gigabytes, and never below one', async () => {
    const user = userEvent.setup();
    render(<CreateVMModal {...baseProps} hostMemoryMB={16000} />);
    await openResources(user);
    expect(screen.getByLabelText('Memory')).toHaveAttribute('max', '15');
  });
});

describe('CreateVMModal image choices', () => {
  const radios = () => screen.getAllByRole('radio');

  it('is a radio group of focusable, named controls', () => {
    renderModal();
    expect(screen.getByRole('radiogroup', { name: 'Base image' })).toBeInTheDocument();
    expect(radios()).toHaveLength(MOCK_CATALOG_IMAGES.length);
    expect(screen.getByRole('radio', { name: cachedImage.name })).toBeInTheDocument();

    // Browsers only give radios arrow-key movement when they share a name;
    // user-event moves between unnamed ones anyway, so this is pinned directly.
    const names = new Set(radios().map(r => r.getAttribute('name')));
    expect(names.size).toBe(1);
    expect([...names][0]).toBeTruthy();
  });

  it('moves the choice with the arrow keys and enables Continue', async () => {
    const user = userEvent.setup();
    renderModal();
    expect(screen.getByRole('button', { name: 'Continue' })).toBeDisabled();

    radios()[0].focus();
    await user.keyboard('{ArrowDown}');
    expect(radios()[1]).toBeChecked();
    expect(radios()[1]).toHaveFocus();
    expect(screen.getByRole('button', { name: 'Continue' })).toBeEnabled();

    await user.keyboard('{ArrowUp}');
    expect(radios()[0]).toBeChecked();
    expect(radios()[1]).not.toBeChecked();
  });

  it('chooses the focused image with Space', async () => {
    const user = userEvent.setup();
    renderModal();
    radios()[2].focus();
    await user.keyboard(' ');
    expect(radios()[2]).toBeChecked();
  });

  // Download is a separate control on the card; pressing it must not also
  // choose the image, or a download would be a commitment to it.
  it('does not select an image by starting its download', async () => {
    const uncached = MOCK_CATALOG_IMAGES.find(i => i.state === 'idle')!;
    const user = userEvent.setup();
    renderModal();
    const card = screen.getByRole('radio', { name: uncached.name }).closest('.cv-image') as HTMLElement;
    await user.click(card.querySelector('button')!);
    expect(baseProps.onPullImage).toHaveBeenCalledWith(uncached.id);
    expect(screen.getByRole('radio', { name: uncached.name })).not.toBeChecked();
  });
});

// Choosing an image never waits on a download — Create does — so the request
// cannot reach the backend for an image that is not on disk.
describe('CreateVMModal image download gating', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('continues without the image, then downloads it before creating', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const uncached = MOCK_CATALOG_IMAGES.find(i => i.state === 'idle')!;
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    renderModal();

    await user.click(screen.getByText(uncached.name));
    await continueTimes(user, 4);
    expect(pullImage).not.toHaveBeenCalled();

    await user.click(screen.getByRole('button', { name: 'Create VM' }));
    await waitFor(() => expect(pullImage).toHaveBeenCalledWith(uncached.id));
    expect(createVM).not.toHaveBeenCalled();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2100);
    });
    await waitFor(() => expect(createVM).toHaveBeenCalledTimes(1));
    expect(vi.mocked(pullImage).mock.invocationCallOrder[0]).toBeLessThan(vi.mocked(createVM).mock.invocationCallOrder[0]);
  });
});
