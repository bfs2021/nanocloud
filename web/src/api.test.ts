import { describe, it, expect, vi, beforeEach } from 'vitest';
import {
  UNAUTHORIZED_EVENT,
  listVMs,
  deleteVM,
  startVM,
  listCatalog,
  getImageStatus,
  createVM,
  consoleWebSocketURL,
  listServiceCatalog,
  login,
  logout,
  serviceFileURL,
} from './api';
import { MOCK_VMS } from './mock';

// Build a minimal Response-like object for the fetch stub.
function fakeResponse(opts: { status?: number; ok?: boolean; body?: string; statusText?: string }) {
  const { status = 200, ok = status < 400, body = '', statusText = '' } = opts;
  return {
    ok,
    status,
    statusText,
    text: () => Promise.resolve(body),
  } as Response;
}

const fetchMock = vi.fn();

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
  localStorage.clear();
});

// sent is what the n-th fetch call carried: its path, method, headers and body.
function sent(n = 0) {
  const [path, init] = fetchMock.mock.calls[n] as [string, RequestInit];
  return { path, method: init.method ?? 'GET', headers: new Headers(init.headers), body: init.body };
}

describe('apiFetch behavior', () => {
  it('announces a 401 so the auth gate can sign the browser out', async () => {
    const heard = vi.fn();
    window.addEventListener(UNAUTHORIZED_EVENT, heard);
    fetchMock.mockResolvedValue(fakeResponse({ status: 401, body: '{"error":"login required"}' }));
    await expect(listVMs()).rejects.toThrow();
    window.removeEventListener(UNAUTHORIZED_EVENT, heard);
    expect(heard).toHaveBeenCalledTimes(1);
  });

  it('parses a JSON array response', async () => {
    fetchMock.mockResolvedValue(fakeResponse({ body: JSON.stringify(MOCK_VMS) }));
    const vms = await listVMs();
    expect(sent()).toMatchObject({ path: '/vms', method: 'GET' });
    expect(vms).toHaveLength(MOCK_VMS.length);
    expect(vms[0].name).toBe('web-prod-01');
  });

  it('returns undefined for 204 No Content without touching the body', async () => {
    const res = fakeResponse({ status: 204 });
    const textSpy = vi.spyOn(res, 'text');
    fetchMock.mockResolvedValue(res);
    await expect(deleteVM('vm1')).resolves.toBeUndefined();
    expect(textSpy).not.toHaveBeenCalled();
  });

  it('tolerates an empty body on a 200/202 response', async () => {
    fetchMock.mockResolvedValue(fakeResponse({ status: 202, body: '' }));
    await expect(startVM('vm1')).resolves.toBeUndefined();
    expect(sent()).toMatchObject({ path: '/vms/vm1/start', method: 'POST' });
  });

  it('throws the response body text on a non-2xx status', async () => {
    fetchMock.mockResolvedValue(fakeResponse({ status: 422, body: 'image not cached' }));
    await expect(startVM('vm1')).rejects.toThrow('image not cached');
  });

  it('falls back to HTTP status when the error body is empty', async () => {
    fetchMock.mockResolvedValue(fakeResponse({ status: 500, body: '' }));
    await expect(listVMs()).rejects.toThrow('HTTP 500');
  });

  it('sends the create-VM payload as JSON', async () => {
    fetchMock.mockResolvedValue(fakeResponse({ body: '{"name":"my-vm"}' }));
    const payload = {
      name: 'my-vm',
      image_id: 'c1',
      vcpus: 2,
      memory_mb: 4096,
      disk_gb: 20,
      pool_name: 'local-ssd',
      network_mode: 'nat',
      cloud_init: { username: 'admin', password: '', ssh_public_key: '' },
    };
    await createVM(payload);
    const req = sent();
    expect(req).toMatchObject({ path: '/vms', method: 'POST', body: JSON.stringify(payload) });
    expect(req.headers.get('Content-Type')).toBe('application/json');
  });
});

describe('session key', () => {
  it('is kept from the login and sent with every request until sign-out', async () => {
    fetchMock.mockResolvedValueOnce(fakeResponse({ body: '{"key":"k-half"}' }));
    await login('pw');
    expect(sent(0).headers.get('X-Nanocloud-Key')).toBeNull();

    fetchMock.mockResolvedValue(fakeResponse({ body: '[]' }));
    await listVMs();
    expect(sent(1).headers.get('X-Nanocloud-Key')).toBe('k-half');
    expect(consoleWebSocketURL('vm1')).toBe('ws://localhost:3000/vms/vm1/console?key=k-half');
    expect(serviceFileURL('k8s', 'kubeconfig')).toBe('/services/k8s/files/kubeconfig?key=k-half');

    fetchMock.mockResolvedValue(fakeResponse({ status: 204 }));
    await logout();
    await listVMs().catch(() => {});
    expect(sent(3).headers.get('X-Nanocloud-Key')).toBeNull();
  });
});

describe('listCatalog normalization', () => {
  it('maps backend status to frontend state', async () => {
    const raw = [
      { id: 'a', name: 'A', os: 'ubuntu', arch: 'amd64', size_bytes: 1, status: 'cached', progress: 0 },
      { id: 'b', name: 'B', os: 'debian', arch: 'amd64', size_bytes: 2, status: 'pulling', progress: 40 },
      { id: 'c', name: 'C', os: 'alpine', arch: 'amd64', size_bytes: 3, status: 'available', progress: 0 },
    ];
    fetchMock.mockResolvedValue(fakeResponse({ body: JSON.stringify(raw) }));
    const catalog = await listCatalog();
    expect(catalog.map(i => i.state)).toEqual(['cached', 'pulling', 'idle']);
  });
});

describe('consoleWebSocketURL', () => {
  it('builds a ws:// URL from the current page origin for http', () => {
    expect(consoleWebSocketURL('my-vm')).toBe('ws://localhost:3000/vms/my-vm/console');
  });

  it('encodes the VM id', () => {
    expect(consoleWebSocketURL('vm one')).toBe('ws://localhost:3000/vms/vm%20one/console');
  });
});

describe('getImageStatus', () => {
  it('maps done to cached and surfaces backend errors', async () => {
    fetchMock.mockResolvedValue(fakeResponse({ body: JSON.stringify({ id: 'c1', progress: 100, done: true, error: '' }) }));
    await expect(getImageStatus('c1')).resolves.toEqual({ state: 'cached', progress: 100 });

    fetchMock.mockResolvedValue(
      fakeResponse({ body: JSON.stringify({ id: 'c1', progress: 10, done: false, error: 'checksum mismatch' }) }),
    );
    await expect(getImageStatus('c1')).rejects.toThrow('checksum mismatch');
  });
});

describe('listServiceCatalog', () => {
  // Older daemons sent "params": null for single-node Kubernetes.
  it('turns the lists an older daemon sent as null into empty ones', async () => {
    const raw = [{ id: 'kubernetes', name: 'Kubernetes', sizes: [], params: null, ports: [6443], outputs: null, files: null }];
    fetchMock.mockResolvedValue(fakeResponse({ body: JSON.stringify(raw) }));
    const [k8s] = await listServiceCatalog();
    expect(k8s.params).toEqual([]);
    expect(k8s.ports).toEqual([6443]);
    expect(k8s.outputs).toEqual([]);
    expect(k8s.files).toEqual([]);
  });
});
