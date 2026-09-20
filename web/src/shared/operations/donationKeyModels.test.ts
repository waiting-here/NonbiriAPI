import { afterEach, describe, expect, it, vi } from 'vitest';
import { getDonationKeyModels, getDonationKeyModelBindings } from './donationKeyModels';

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
