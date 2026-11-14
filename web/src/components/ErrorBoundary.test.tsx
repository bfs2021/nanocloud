import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ErrorBoundary } from './ErrorBoundary';

function Broken(): JSX.Element {
  throw new Error('params is null');
}

describe('ErrorBoundary', () => {
  it('shows an overlay that crashed as a dialog of its own, which closes it', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const onDismiss = vi.fn();
    render(
      <div>
        <h1>Services</h1>
        <ErrorBoundary label="Launch Kubernetes" onDismiss={onDismiss}>
          <Broken />
        </ErrorBoundary>
      </div>,
    );
    // The rest of the page is still there: the crash stayed in the overlay.
    expect(screen.getByRole('heading', { name: 'Services' })).toBeInTheDocument();
    const dialog = screen.getByRole('alertdialog', { name: 'Launch Kubernetes ran into a problem' });
    expect(dialog).toHaveTextContent('params is null');
    await userEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onDismiss).toHaveBeenCalledOnce();
  });

  it('shows a section that crashed in place, with a retry', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    render(
      <ErrorBoundary label="Services">
        <Broken />
      </ErrorBoundary>,
    );
    expect(screen.queryByRole('alertdialog')).toBeNull();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument();
  });
});
