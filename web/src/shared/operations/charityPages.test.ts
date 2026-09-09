import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  getManagedBindingCandidatesPage,
  normalizeCharityBindingCandidatesPage,
  type CharityBindingCandidatePageFilters,
} from './charityPages';
import { disposeTestProviders } from '../../../test/unit/support';

function candidate(overrides: Record<string, unknown> = {}) {
  return {
    donation_key_id: '21',
    donation_id: '7',
    source: {
      connector_type: 'openai-compatible',
      canonical_base_url: 'https://safe.example/v1',
      display_head: 'head',
      display_tail: 'tail',
    },
    upstream_model_id: 'model-a',
    source_types: ['automatic'],
    ...overrides,
  };
}

function page(data: unknown[], pageSize: 10 | 20 | 50 | 100, totalItems: string, pageNumber = '1') {
  const total = BigInt(totalItems);
  const pages = total === 0n ? 1n : (total - 1n) / BigInt(pageSize) + 1n;
  return {
    data,
    next_cursor: null,
    pagination: {
      page: pageNumber,
      page_size: pageSize,
      total_items: totalItems,
      total_pages: pages.toString(),
    },
  };
}

describe('numbered charity binding candidates', () => {
  it('accepts an unfiltered page without manufacturing empty identity filters', async () => {
    const fetchMock = vi.fn<typeof fetch>(
      async () =>
        new Response(JSON.stringify(page([candidate()], 20, '1')), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        }),
    );
    vi.stubGlobal('fetch', fetchMock);
    await expect(
      getManagedBindingCandidatesPage('admin', '31', {}, '1', 20),
    ).resolves.toMatchObject({ data: [{ donation_id: '7', donation_key_id: '21' }] });
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      '/admin/api/charity-models/31/binding-candidates?page=1&page_size=20',
    );
  });

  it.each([10, 20, 50, 100] as const)('accepts the server window at size %s', (pageSize) => {
    const value = page([candidate()], pageSize, '1');
    expect(
      normalizeCharityBindingCandidatesPage(
        value,
        'admin',
        { donation_id: '7', donation_key_id: '21' },
        '1',
        pageSize,
      ),
    ).toMatchObject({ data: [{ donation_id: '7', donation_key_id: '21' }], next_cursor: null });
  });

  it('clamps a requested page to an empty-safe final page and preserves opaque filters', async () => {
    const fetchMock = vi.fn<typeof fetch>(
      async () =>
        new Response(
          JSON.stringify(
            page(
              [
                candidate({ upstream_model_id: 'model-z' }),
                candidate({ upstream_model_id: 'model-y' }),
              ],
              10,
              '22',
              '3',
            ),
          ),
          { status: 200, headers: { 'content-type': 'application/json' } },
        ),
    );
    vi.stubGlobal('fetch', fetchMock);
    const result = await getManagedBindingCandidatesPage(
      'steward',
      '31',
      { donation_id: '7', donation_key_id: '21', source: 'automatic', q: 'model z' },
      '999',
      10,
    );
    expect(result.pagination).toMatchObject({ page: '3', page_size: 10, total_items: '22' });
    const requested = new URL(String(fetchMock.mock.calls[0]?.[0]), 'https://example.test');
    expect(requested.pathname).toBe('/api/steward/charity-models/31/binding-candidates');
    expect(Object.fromEntries(requested.searchParams)).toEqual({
      donation_id: '7',
      donation_key_id: '21',
      source: 'automatic',
      q: 'model z',
      page: '999',
      page_size: '10',
    });
  });

  it('rejects cursor-shaped responses and rows outside a requested source filter', () => {
    expect(() =>
      normalizeCharityBindingCandidatesPage(
        { ...page([candidate()], 20, '1'), next_cursor: 'legacy' },
        'admin',
        {},
        '1',
        20,
      ),
    ).toThrow();
    expect(() =>
      normalizeCharityBindingCandidatesPage(
        page([candidate({ source_types: ['manual'] })], 20, '1'),
        'admin',
        { source: 'automatic' },
        '1',
        20,
      ),
    ).toThrow();
  });

  it.each([
    ['model id', '0', {}, '1', 20],
    ['model id range', '9223372036854775808', {}, '1', 20],
    ['filters object', '31', null, '1', 20],
    ['donation id filter', '31', { donation_id: '0' }, '1', 20],
    ['donation key filter', '31', { donation_key_id: 'not-an-id' }, '1', 20],
    ['source filter', '31', { source: 'other' }, '1', 20],
    ['candidate search control', '31', { q: '\u0001' }, '1', 20],
    ['candidate search c1', '31', { q: '\u0080' }, '1', 20],
    ['candidate search surrogate', '31', { q: '\ud800' }, '1', 20],
    ['candidate search scalar limit', '31', { q: '😀'.repeat(513) }, '1', 20],
    ['unknown filter', '31', { extra: true }, '1', 20],
    ['page', '31', {}, '0', 20],
    ['page size', '31', {}, '1', 15],
  ] as const)(
    'rejects invalid %s before a request',
    async (_label, modelId, filters, pageNumber, pageSize) => {
      const fetchMock = vi.fn<typeof fetch>();
      vi.stubGlobal('fetch', fetchMock);
      await expect(
        Promise.resolve().then(() =>
          getManagedBindingCandidatesPage(
            'admin',
            modelId,
            filters as CharityBindingCandidatePageFilters,
            pageNumber,
            pageSize as 10 | 20 | 50 | 100,
          ),
        ),
      ).rejects.toMatchObject({ code: 'invalid_request', status: 400 });
      expect(fetchMock).not.toHaveBeenCalled();
    },
  );

  it('keeps malformed server responses as invalid responses', async () => {
    const fetchMock = vi.fn<typeof fetch>(
      async () =>
        new Response(JSON.stringify({ data: [], next_cursor: null }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        }),
    );
    vi.stubGlobal('fetch', fetchMock);
    await expect(getManagedBindingCandidatesPage('admin', '31', {}, '1', 20)).rejects.toMatchObject(
      { code: 'invalid_response', status: 200 },
    );
  });
});

afterEach(async () => {
  await disposeTestProviders();
  vi.unstubAllGlobals();
});
