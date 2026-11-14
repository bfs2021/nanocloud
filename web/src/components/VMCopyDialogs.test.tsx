import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { CloneVMModal, SaveTemplateModal } from './VMCopyDialogs';
import { cloneVM, saveTemplate } from '../api';

vi.mock('../api', () => ({
  cloneVM: vi.fn().mockResolvedValue({ name: 'web-01-copy' }),
  saveTemplate: vi.fn().mockResolvedValue({ name: 'Ubuntu web server' }),
}));

beforeEach(() => {
  vi.clearAllMocks();
});

describe('CloneVMModal', () => {
  it('sends the name and the start choice, and reports the warnings back', async () => {
    vi.mocked(cloneVM).mockResolvedValue({
      name: 'web-02',
      warnings: ['web-01’s passed-through host devices were not copied'],
    });
    const onCloned = vi.fn();
    const user = userEvent.setup();
    render(<CloneVMModal vmId="web-01" onClose={vi.fn()} onCloned={onCloned} />);

    const name = screen.getByLabelText('Name for the clone');
    expect(name).toHaveValue('web-01-copy');
    await user.clear(name);
    await user.type(name, 'web-02');
    await user.click(screen.getByLabelText(/Start the clone/));
    await user.click(screen.getByRole('button', { name: 'Clone' }));

    await waitFor(() => expect(cloneVM).toHaveBeenCalledWith('web-01', 'web-02', true));
    expect(onCloned).toHaveBeenCalledWith('web-02', ['web-01’s passed-through host devices were not copied']);
  });

  // The disk and the seed ISO are written from the name before libvirt ever
  // sees it, so the rule is a path-safety boundary, not a style preference.
  it('refuses a name that is not a safe path component', async () => {
    const user = userEvent.setup();
    render(<CloneVMModal vmId="web-01" onClose={vi.fn()} onCloned={vi.fn()} />);

    const name = screen.getByLabelText('Name for the clone');
    await user.clear(name);
    await user.type(name, '../escape');

    expect(screen.getByRole('button', { name: 'Clone' })).toBeDisabled();
    expect(cloneVM).not.toHaveBeenCalled();
  });

  // The fix is only available *before* the copy is taken, which is why the
  // caveat is here and not in the result.
  it('states the machine-id caveat while it can still be acted on', () => {
    render(<CloneVMModal vmId="web-01" onClose={vi.fn()} onCloned={vi.fn()} />);
    expect(screen.getByText(/same machine-id and SSH host keys/)).toBeInTheDocument();
    expect(screen.getByText(/cloud-init clean/)).toBeInTheDocument();
  });

  it('keeps the dialog open and shows why when the clone fails', async () => {
    vi.mocked(cloneVM).mockRejectedValue(new Error('"web-01" is running — stop it before cloning'));
    const onCloned = vi.fn();
    const user = userEvent.setup();
    render(<CloneVMModal vmId="web-01" onClose={vi.fn()} onCloned={onCloned} />);

    await user.click(screen.getByRole('button', { name: 'Clone' }));

    expect(await screen.findByText(/stop it before cloning/)).toBeInTheDocument();
    expect(onCloned).not.toHaveBeenCalled();
  });
});

describe('SaveTemplateModal', () => {
  it('saves under the typed name', async () => {
    const onSaved = vi.fn();
    const user = userEvent.setup();
    render(<SaveTemplateModal vmId="web-01" onClose={vi.fn()} onSaved={onSaved} />);

    // Nothing to save until it is called something.
    expect(screen.getByRole('button', { name: 'Save template' })).toBeDisabled();

    await user.type(screen.getByLabelText('Template name'), 'Ubuntu web server');
    await user.click(screen.getByRole('button', { name: 'Save template' }));

    await waitFor(() => expect(saveTemplate).toHaveBeenCalledWith('Ubuntu web server', 'web-01'));
    expect(onSaved).toHaveBeenCalledWith('Ubuntu web server');
  });

  // A template keeps a configuration, not a disk or a credential.
  it('says what a template does not keep', () => {
    render(<SaveTemplateModal vmId="web-01" onClose={vi.fn()} onSaved={vi.fn()} />);
    expect(screen.getByText(/login password is not saved/)).toBeInTheDocument();
    expect(screen.getByText(/Attached disks are not part of a template/)).toBeInTheDocument();
  });
});
