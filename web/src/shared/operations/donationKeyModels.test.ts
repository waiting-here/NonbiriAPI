import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  getDonationKeyModels,
  getDonationKeyModelBindings,
  addDonationManualModels,
  removeDonationManualModel,
} from './donationKeyModels';

const model = {
  model_id: '21',
  full_name: '[公益]Vendor/Shared',
  enabled: false,
  binding_count: '2',
  available_binding_count: '0',
};
function response(data: unknown[]) {
  return new Response(
    JSON.stringify({
      data,
      next_cursor: null,
      pagination: { page: '1', page_size: 20, total_items: String(data.length), total_pages: '1' },
    }),
    { status: 200, headers: { 'content-type': 'application/json' } },
  );
}
afterEach(() => vi.unstubAllGlobals());
describe('donated key inverse pages', () => {
  it('keeps linked models and separately paginated candidate sources on a selected steward scope', async () => {
    const candidate = {
      upstream_model_id: 'manual name',
      display_name: 'manual name',
      source: 'manual',
      verified: false,
      manual_entry_id: '31',
      manual_entry_revision: '1',
    };
    const payload = await response([model]).json();
    const fetcher = vi
      .fn()
      .mockResolvedValue(
        new Response(
          JSON.stringify({
            ...payload,
            candidates: [candidate],
            candidates_pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
            manual_catalog_revision: '5',
          }),
          { status: 200, headers: { 'content-type': 'application/json' } },
        ),
      );
    vi.stubGlobal('fetch', fetcher);
    const result = await getDonationKeyModels(
      'steward',
      '7',
      '9',
      '1',
      20,
      undefined,
      '21',
      'manual name',
    );
    expect(result.data).toEqual([model]);
    expect(result.candidates?.data).toEqual([candidate]);
    expect(result.manual_catalog_revision).toBe('5');
    const url = new URL(fetcher.mock.calls[0][0], 'https://example.test');
    expect(Object.fromEntries(url.searchParams)).toEqual({
      page: '1',
      page_size: '20',
      q: 'manual name',
      charity_model_id: '21',
    });
  });

  it('uses the same selected model scope and catalog revision for manual candidate writes', async () => {
    const fetcher = vi
      .fn()
      .mockImplementation(
        () =>
          new Response(JSON.stringify({ entries: [], manual_catalog_revision: '6' }), {
            status: 200,
            headers: { 'content-type': 'application/json' },
          }),
      );
    vi.stubGlobal('fetch', fetcher);
    const controller = new AbortController();
    const key = 'abcdefghijklmnopqrstuv';
    await addDonationManualModels(
      'steward',
      '7',
      '9',
      ['manual name'],
      '5',
      key,
      controller.signal,
      '21',
    );
    await removeDonationManualModel('steward', '7', '9', '31', '6', key, controller.signal, '21');
    expect(fetcher.mock.calls.map(([url]) => url)).toEqual([
      '/api/steward/donations/7/keys/9/models/manual?charity_model_id=21',
      '/api/steward/donations/7/keys/9/models/manual/31?charity_model_id=21',
    ]);
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({
      entries: ['manual name'],
      expected_manual_catalog_revision: '5',
    });
    expect(JSON.parse(fetcher.mock.calls[1][1].body)).toEqual({
      expected_manual_catalog_revision: '6',
    });
    expect(fetcher.mock.calls[0][1].signal).toBe(controller.signal);
    expect(new Headers(fetcher.mock.calls[0][1].headers).get('Idempotency-Key')).toBe(key);
  });
  it.each(['admin', 'steward'] as const)(
    'requests only the %s session route and accepts disabled relations',
    async (role) => {
      const fetcher = vi.fn().mockResolvedValue(response([model]));
      vi.stubGlobal('fetch', fetcher);
      const controller = new AbortController();
      await expect(
        getDonationKeyModels(role, '7', '9', '99', 20, controller.signal),
      ).resolves.toMatchObject({ data: [model], pagination: { page: '1' } });
      const [url, options] = fetcher.mock.calls[0];
      expect(url).toBe(
        `${role === 'admin' ? '/admin/api' : '/api/steward'}/donations/7/keys/9/models?page=99&page_size=20`,
      );
      expect(options.signal).toBe(controller.signal);
    },
  );
  it('rejects duplicate models, unsafe additions, and impossible availability counts', async () => {
    for (const rows of [
      [model, model],
      [{ ...model, owner: 'private' }],
      [{ ...model, available_binding_count: '3' }],
    ]) {
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response(rows)));
      await expect(getDonationKeyModels('steward', '7', '9', '1', 20)).rejects.toMatchObject({
        code: 'invalid_response',
      });
    }
  });
  it('validates binding identity and state without exposing source secrets', async () => {
    const binding = {
      binding_id: '4',
      upstream_model_id: 'shared-upstream',
      ord: 1,
      state: 'expired',
    };
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response([binding])));
    await expect(
      getDonationKeyModelBindings('admin', '7', '9', '21', '1', 20),
    ).resolves.toMatchObject({ data: [binding] });
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(response([{ ...binding, endpoint_key_id: 'secret' }])),
    );
    await expect(
      getDonationKeyModelBindings('steward', '7', '9', '21', '1', 20),
    ).rejects.toMatchObject({ code: 'invalid_response' });
  });
});
