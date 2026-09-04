import { describe, expect, it, vi } from 'vitest';
import {
  disableAdminMaintenance,
  enableMaintenance,
  getMaintenanceState,
  normalizeMaintenanceState,
} from './maintenance';

function jsonResponse(value: unknown): Response {
  return new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } });
}

describe('maintenance wire', () => {
  it('strictly decodes the singleton authority state', () => {
    expect(normalizeMaintenanceState({ enabled: true, revision: '2' })).toEqual({ enabled: true, revision: '2' });
    expect(() => normalizeMaintenanceState({ enabled: false, revision: '02' })).toThrow();
    expect(() => normalizeMaintenanceState({ enabled: false, revision: '2', event_id: 'hidden' })).toThrow();
  });

  it('reads the role-specific authority route without a mutation identity', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse({ enabled: false, revision: '7' }));
    vi.stubGlobal('fetch', fetchMock);
    await expect(getMaintenanceState('steward')).resolves.toEqual({ enabled: false, revision: '7' });
    const [path, init] = fetchMock.mock.calls[0] ?? [];
    expect(path).toBe('/api/steward/maintenance');
    expect(init?.method).toBeUndefined();
    expect(new Headers(init?.headers).has('Idempotency-Key')).toBe(false);
  });

  it('sends the closed confirmed enable body and supplied identity', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse({ enabled: true, revision: '8' }));
    vi.stubGlobal('fetch', fetchMock);
    await enableMaintenance('admin', '7', 'planned window', 'idem-enable');
    const [path, init] = fetchMock.mock.calls[0] ?? [];
    expect(path).toBe('/admin/api/maintenance/enable');
    expect(init?.method).toBe('POST');
    expect(new Headers(init?.headers).get('Idempotency-Key')).toBe('idem-enable');
    expect(JSON.parse(String(init?.body))).toEqual({ expected_revision: '7', reason: 'planned window', confirmation: true });
  });

  it('omits the enable-only confirmation field from admin disable', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse({ enabled: false, revision: '9' }));
    vi.stubGlobal('fetch', fetchMock);
    await disableAdminMaintenance('8', 'window complete', 'idem-disable');
    const [path, init] = fetchMock.mock.calls[0] ?? [];
    expect(path).toBe('/admin/api/maintenance/disable');
    expect(JSON.parse(String(init?.body))).toEqual({ expected_revision: '8', reason: 'window complete' });
  });
});
