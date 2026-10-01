import { afterEach, describe, expect, it, vi } from 'vitest';
import { resourceStatus, readResourceResult, type ResourceStatus } from './resourceOperation';

afterEach(() => vi.unstubAllGlobals());
describe('Resource operation recovery', () => {
  it('queries only the original key without starting a new write identity', async () => {
    const fetcher = vi.fn(
      async () => new Response(JSON.stringify({ status: 'not_recorded' }), { status: 200 }),
    );
    vi.stubGlobal('fetch', fetcher);
    const key = 'original_operation_0000001';
    expect(await resourceStatus(key)).toEqual({ status: 'not_recorded' });
    const [path, init] = fetcher.mock.calls[0] as unknown as [string, RequestInit];
    expect(path).toBe('/api/resource-operation-status');
    expect(init.method).toBe('POST');
    expect(JSON.parse(init.body as string)).toEqual({ operation_key: key });
    expect(new Headers(init.headers).has('Idempotency-Key')).toBe(false);
    expect(init.cache).toBe('no-store');
  });
  it('does not turn missing or expired receipts into success or issue resource reads', async () => {
    const fetcher = vi.fn();
    vi.stubGlobal('fetch', fetcher);
    const intent = { kind: 'model' as const, row: 'x', input: { provider: 'mine', model: 'chat' } };
    expect(
      await readResourceResult(intent, { status: 'not_recorded' }, new AbortController().signal),
    ).toBeNull();
    expect(
      await readResourceResult(intent, { status: 'expired' }, new AbortController().signal),
    ).toBeNull();
    expect(fetcher).not.toHaveBeenCalled();
  });
  it('rejects a receipt for another selected service or key before reading its resources', async () => {
    const fetcher = vi.fn();
    vi.stubGlobal('fetch', fetcher);
    const intent = { kind: 'refresh' as const, endpointId: '1', keyId: '2' };
    const status: ResourceStatus = {
      status: 'recorded',
      stage: 'catalog_refresh',
      result: { endpoint_id: '3', endpoint_key_id: '2', operation_id: 'op_AAAAAAAAAAAAAAAAAAAAAA' },
    };
    await expect(
      readResourceResult(intent, status, new AbortController().signal),
    ).rejects.toThrow();
    expect(fetcher).not.toHaveBeenCalled();
  });
});
