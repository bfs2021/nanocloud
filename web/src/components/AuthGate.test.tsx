import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import { AuthGate } from './AuthGate';
import { UNAUTHORIZED_EVENT } from '../api';

function respond(status: number, body = '') {
  return { ok: status < 400, status, statusText: '', text: () => Promise.resolve(body) } as Response;
}

const fetchMock = vi.fn();

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
});

function routes(table: Record<string, () => Response>) {
  fetchMock.mockImplementation((path: string) => {
    const r = table[path];
    if (!r) throw new Error(`unexpected fetch ${path}`);
    return Promise.resolve(r());
  });
}

describe('AuthGate', () => {
  it('shows the app to a signed-in browser', async () => {
    routes({ '/auth/status': () => respond(200, '{"authenticated":true,"setup_required":false}') });
    render(
      <AuthGate>
        <p>the app</p>
      </AuthGate>,
    );
    expect(await screen.findByText('the app')).toBeInTheDocument();
  });

  it('signs in, and a 401 anywhere signs out again', async () => {
    let signedIn = false;
    routes({
      '/auth/status': () => respond(200, JSON.stringify({ authenticated: signedIn, setup_required: false })),
      '/auth/login': () => {
        signedIn = true;
        return respond(200, '{"key":"page-half"}');
      },
    });
    render(
      <AuthGate>
        <p>the app</p>
      </AuthGate>,
    );

    const field = await screen.findByLabelText('Password');
    expect(screen.queryByText('the app')).not.toBeInTheDocument();
    fireEvent.change(field, { target: { value: 'correct horse' } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByText('the app')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith('/auth/login', expect.objectContaining({ body: '{"password":"correct horse"}' }));

    act(() => {
      window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
    });
    expect(await screen.findByLabelText('Password')).toBeInTheDocument();
    expect(screen.queryByText('the app')).not.toBeInTheDocument();
  });

  it('says why a sign-in failed and clears the field', async () => {
    routes({
      '/auth/status': () => respond(200, '{"authenticated":false,"setup_required":false}'),
      '/auth/login': () => respond(401, 'wrong password'),
    });
    render(
      <AuthGate>
        <p>the app</p>
      </AuthGate>,
    );
    const field = await screen.findByLabelText('Password');
    fireEvent.change(field, { target: { value: 'nope' } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('wrong password');
    await waitFor(() => expect(field).toHaveValue(''));
  });

  it('tells a host with no password how to set one', async () => {
    routes({ '/auth/status': () => respond(200, '{"authenticated":false,"setup_required":true}') });
    render(
      <AuthGate>
        <p>the app</p>
      </AuthGate>,
    );
    expect(await screen.findByText('sudo nanocloud passwd')).toBeInTheDocument();
    expect(screen.queryByLabelText('Password')).not.toBeInTheDocument();
  });

  it('offers a retry when the daemon does not answer', async () => {
    fetchMock.mockRejectedValue(new TypeError('Failed to fetch'));
    render(
      <AuthGate>
        <p>the app</p>
      </AuthGate>,
    );
    expect(await screen.findByRole('button', { name: 'Try again' })).toBeInTheDocument();
  });
});
