import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ImagesView } from './ImagesView';
import { MOCK_CACHED_IMAGES, MOCK_CATALOG_IMAGES } from '../mock';

vi.mock('../api', () => ({
  deleteImage: vi.fn().mockResolvedValue(undefined),
}));

import { deleteImage } from '../api';

// Pull tracking (progress/errors/start/cancel) is owned by App and passed in
// as props — see App.tsx. These defaults represent "nothing in flight".
const noPulls = {
  pullProgress: {},
  pullErrors: {},
  onPullImage: vi.fn(),
  onCancelPullImage: vi.fn(),
  onError: vi.fn(),
};

beforeEach(() => {
  vi.clearAllMocks();
});

describe('ImagesView', () => {
  it('renders cached images and the catalog', () => {
    render(<ImagesView cached={MOCK_CACHED_IMAGES} catalog={MOCK_CATALOG_IMAGES} {...noPulls} onRefresh={vi.fn()} />);
    // Cached section lists each cached image by its derived qcow2 filename
    // (the display name alone also appears in the catalog below).
    for (const img of MOCK_CACHED_IMAGES) {
      const filename = `${img.name.toLowerCase().replace(/\s/g, '-')}.qcow2`;
      expect(screen.getByText(filename)).toBeInTheDocument();
    }
    // Catalog: already-cached entries show a "Cached" badge, idle ones a Pull button.
    const cachedCount = MOCK_CATALOG_IMAGES.filter(i => i.state === 'cached').length;
    const idleCount = MOCK_CATALOG_IMAGES.filter(i => i.state === 'idle').length;
    expect(screen.getAllByText('Cached')).toHaveLength(cachedCount);
    expect(screen.getAllByRole('button', { name: 'Pull' })).toHaveLength(idleCount);
  });

  it('says why the lists may be stale when a refresh failed', () => {
    render(<ImagesView cached={[]} catalog={[]} {...noPulls} onRefresh={vi.fn()} loadError="connection refused" />);
    expect(screen.getByRole('alert')).toHaveTextContent("Couldn't load images: connection refused");
  });

  it('shows the empty-cache message when nothing is cached', () => {
    render(<ImagesView cached={[]} catalog={MOCK_CATALOG_IMAGES} {...noPulls} onRefresh={vi.fn()} />);
    expect(screen.getByText('No images cached yet. Pull one from the catalog below.')).toBeInTheDocument();
  });

  it('calls onPullImage when Pull is clicked', async () => {
    const user = userEvent.setup();
    const onPullImage = vi.fn();
    const idle = MOCK_CATALOG_IMAGES.find(i => i.state === 'idle')!;
    render(<ImagesView cached={[]} catalog={[idle]} {...noPulls} onPullImage={onPullImage} onRefresh={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: 'Pull' }));
    expect(onPullImage).toHaveBeenCalledWith(idle.id);
  });

  it('shows progress UI when pullProgress has an in-flight entry', () => {
    // Pull tracking lives in App so it survives this view unmounting;
    // ImagesView renders purely from the prop.
    const idle = MOCK_CATALOG_IMAGES.find(i => i.state === 'idle')!;
    render(<ImagesView cached={[]} catalog={[idle]} {...noPulls} pullProgress={{ [idle.id]: 42 }} onRefresh={vi.fn()} />);
    expect(screen.getByText('Pulling…')).toBeInTheDocument();
    expect(screen.getByText('42%')).toBeInTheDocument();
  });

  it('deletes a cached image once confirmed, and refreshes', async () => {
    const user = userEvent.setup();
    const onRefresh = vi.fn();
    const img = MOCK_CACHED_IMAGES[0];
    render(<ImagesView cached={[img]} catalog={[]} {...noPulls} onRefresh={onRefresh} />);

    await user.click(screen.getByRole('button', { name: 'Delete' }));
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(deleteImage).not.toHaveBeenCalled();

    await user.click(screen.getByRole('button', { name: 'Delete' }));
    await user.click(screen.getByRole('button', { name: 'Delete image' }));
    expect(deleteImage).toHaveBeenCalledWith(img.id);
    expect(onRefresh).toHaveBeenCalled();
  });

  it('reports a delete the daemon refused instead of swallowing it', async () => {
    vi.mocked(deleteImage).mockRejectedValueOnce(new Error('image is in use'));
    const user = userEvent.setup();
    const img = MOCK_CACHED_IMAGES[0];
    render(<ImagesView cached={[img]} catalog={[]} {...noPulls} onRefresh={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: 'Delete' }));
    await user.click(screen.getByRole('button', { name: 'Delete image' }));
    expect(noPulls.onError).toHaveBeenCalledWith(`Failed to delete ${img.name}`, expect.any(Error));
  });
});
