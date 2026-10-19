import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  deletePriceTemplate,
  getPriceTemplates,
  normalizePriceTemplate,
  savePriceTemplate,
} from './charityPriceTemplates';

const template = {
  id: '3',
  name: 'Reference prices',
  description: 'A complete price snapshot',
  enabled: true,
  revision: '4',
  created_at: 1,
  updated_at: 2,
  pricing: { mode: 'per_request', user_price: '3.5', donor_reward: '1.75' },
  token_reserve_credits: null,
  discount: { enabled: false, percent: 0, start_at: null, end_at: null },
};
const response = (value: unknown) =>
  new Response(JSON.stringify(value), { headers: { 'content-type': 'application/json' } });
afterEach(() => vi.unstubAllGlobals());

describe('charity price template contract', () => {
  it('reads bounded searchable pages with an explicit enabled state and complete price snapshots', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () =>
      response({
        data: [template],
        next_cursor: null,
        pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);
    const page = await getPriceTemplates('steward', 'Reference', 'enabled', '1', 20);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      '/api/steward/charity-price-templates?q=Reference&state=enabled&page=1&page_size=20',
    );
    expect(page.data[0]).toEqual(template);
  });

  it('writes complete replacement prices and metadata with template revision, then deletes by the same scope', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => response(template));
    vi.stubGlobal('fetch', fetchMock);
    const input = {
      name: 'Renamed',
      description: '',
      enabled: false,
      pricing: template.pricing as {
        mode: 'per_request';
        user_price: string;
        donor_reward: string;
      },
      token_reserve_credits: null,
      discount: template.discount,
    };
    await savePriceTemplate('admin', { id: '3', expected_revision: '4', ...input }, 'original-key');
    const [path, options] = fetchMock.mock.calls[0]!;
    expect(path).toBe('/admin/api/charity-price-templates/3');
    expect(options?.method).toBe('PUT');
    expect(JSON.parse(String(options?.body))).toEqual({ expected_revision: '4', ...input });
    expect(new Headers(options?.headers).get('Idempotency-Key')).toBe('original-key');
    await deletePriceTemplate('admin', '3', '4', 'delete-key');
    expect(fetchMock.mock.calls[1]?.[1]?.method).toBe('DELETE');
    expect(JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body))).toEqual({
      expected_revision: '4',
    });
  });

  it('requires an explicit reserve field and keeps model identity out of the normalized template DTO', () => {
    const missing: Record<string, unknown> = { ...template };
    delete missing.token_reserve_credits;
    expect(() => normalizePriceTemplate(missing)).toThrow();
    expect(
      normalizePriceTemplate({ ...template, provider: 'not-a-template-field' }),
    ).not.toHaveProperty('provider');
  });
});
