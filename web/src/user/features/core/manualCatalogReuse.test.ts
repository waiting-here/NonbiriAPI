import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import { createOrReuseManualEntries } from './manualCatalogReuse';
import { executeResource } from './resourceOperation';
import { isOutcomeUnknown } from './request';

const identity = {
  idempotencyKey: 'manual_reuse_operation_0001',
  actionId: 'manual_reuse_action_0001',
};
const input = { upstream_model_id: 'Vendor/Exact', provider: 'New provider' };
const entry = (name = input.upstream_model_id, id = '41') => ({
  id,
  source_type: 'manual',
  upstream_model_id: name,
  provider: 'Original provider',
  source_revision: '1',
  pair_revision: '1',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_000,
});
function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}
function catalog(entries: unknown[], page = '1', total = entries.length) {
  return {
    evidence: {
      state: 'unknown',
      revision: '1',
      result: null,
      safe_class: 'none',
      observed_at: null,
      count: null,
    },
    automatic_entries: [],
    manual_entries: entries,
    next_cursor: null,
    pagination: {
      page,
      page_size: 100,
      total_items: String(total),
      total_pages: String(Math.max(1, Math.ceil(total / 100))),
    },
  };
}
const conflict = () => json({ error: { code: 'conflict', message: 'Concurrent change' } }, 409);
afterEach(() => vi.unstubAllGlobals());

describe('manual catalog reuse during model import', () => {
  it('finds the exact model after the first page under the selected endpoint and key', async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(
        json(
          catalog(
            Array.from({ length: 100 }, (_, i) => entry('other-' + i, String(i + 1))),
            '1',
            101,
          ),
        ),
      )
      .mockResolvedValueOnce(json(catalog([entry()], '2', 101)));
    vi.stubGlobal('fetch', fetcher);
    await createOrReuseManualEntries('11', '21', [input], identity);
    expect(fetcher.mock.calls.map(([url]) => url)).toEqual([
      '/api/endpoints/11/keys/21/models?page=1&page_size=100&source=manual',
      '/api/endpoints/11/keys/21/models?page=2&page_size=100&source=manual',
    ]);
    expect(fetcher.mock.calls.every(([, init]) => !init.method || init.method === 'GET')).toBe(
      true,
    );
  });

  it('reuses existing support through a new import operation after reopening', async () => {
    const numbered = catalog([entry()]);
    const view = {
      evidence: numbered.evidence,
      automatic_entries: [],
      manual_entries: [entry()],
      next_cursor: null,
    };
    const fetcher = vi.fn(async (url: string) =>
      json(url.includes('source=manual') ? numbered : view),
    );
    vi.stubGlobal('fetch', fetcher);
    const intent = {
      kind: 'manual' as const,
      reuseExisting: true as const,
      endpointId: '11',
      keyId: '21',
      entries: [input],
    };
    for (const key of [identity.idempotencyKey, 'reopened_manual_operation_0001']) {
      const result = await executeResource(intent, undefined, key, new AbortController().signal);
      expect(result).toMatchObject({ kind: 'manual', catalog: { manual_entries: [entry()] } });
    }
    expect(fetcher).toHaveBeenCalledTimes(4);
    expect(fetcher.mock.calls.every(([url]) => !url.endsWith('/manual'))).toBe(true);
  });

  it('creates missing support once, without changing an existing similar model', async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(json(catalog([entry(input.upstream_model_id + '-extra')])))
      .mockResolvedValueOnce(json({ entries: [{ ...entry(), provider: input.provider }] }, 201));
    vi.stubGlobal('fetch', fetcher);
    await createOrReuseManualEntries('11', '21', [input], identity);
    const [url, init] = fetcher.mock.calls[1] as [string, RequestInit];
    expect(url).toBe('/api/endpoints/11/keys/21/models/manual');
    expect(init.method).toBe('POST');
    expect(new Headers(init.headers).get('Idempotency-Key')).toBe(identity.idempotencyKey);
    expect(JSON.parse(init.body as string)).toEqual({ entries: [input] });
  });

  it('accepts a concurrent exact creation only after reading it from the server', async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(json(catalog([])))
      .mockResolvedValueOnce(conflict())
      .mockResolvedValueOnce(json(catalog([entry()])));
    vi.stubGlobal('fetch', fetcher);
    await createOrReuseManualEntries('11', '21', [input], identity);
    expect(fetcher).toHaveBeenCalledTimes(3);
    expect(fetcher.mock.calls.filter(([, init]) => init.method === 'POST')).toHaveLength(1);
  });

  it.each(['no match', 'lookup failed'])('preserves the conflict when %s', async (mode) => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(json(catalog([])))
      .mockResolvedValueOnce(conflict())
      .mockResolvedValueOnce(
        mode === 'no match'
          ? json(catalog([entry(input.upstream_model_id.toLowerCase())]))
          : json({ error: { code: 'forbidden', message: 'Lookup denied' } }, 403),
      );
    vi.stubGlobal('fetch', fetcher);
    await expect(createOrReuseManualEntries('11', '21', [input], identity)).rejects.toMatchObject({
      code: 'conflict',
      status: 409,
      message: 'Concurrent change',
    });
    expect(fetcher).toHaveBeenCalledTimes(3);
  });

  it('does not bypass an explicit creation failure', async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(json(catalog([])))
      .mockResolvedValueOnce(json({ error: { code: 'resource_locked', message: 'Locked' } }, 423));
    vi.stubGlobal('fetch', fetcher);
    await expect(createOrReuseManualEntries('11', '21', [input], identity)).rejects.toMatchObject({
      code: 'resource_locked',
      status: 423,
    });
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('keeps a lost write response unknown instead of inferring success from a catalog read', async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(json(catalog([])))
      .mockRejectedValueOnce(new TypeError('Response lost'));
    vi.stubGlobal('fetch', fetcher);
    let failure: unknown;
    try {
      await createOrReuseManualEntries('11', '21', [input], identity);
    } catch (error) {
      failure = error;
    }
    expect(isOutcomeUnknown(failure)).toBe(true);
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('enables reuse only for the import intent; ordinary manual creation still conflicts', async () => {
    const fetcher = vi.fn().mockResolvedValue(conflict());
    vi.stubGlobal('fetch', fetcher);
    await expect(
      executeResource(
        { kind: 'manual', endpointId: '11', keyId: '21', entries: [input] },
        undefined,
        identity.idempotencyKey,
        new AbortController().signal,
      ),
    ).rejects.toBeInstanceOf(ApiError);
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher.mock.calls[0]?.[0]).toBe('/api/endpoints/11/keys/21/models/manual');
  });
});
