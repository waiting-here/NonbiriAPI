import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PAGE_SIZES, type PageSize } from '@shared/operations/pageNumbers';
import {
  getBindingCandidatesPage,
  getCatalogPage,
  listEndpointKeysPage,
  listEndpointsPage,
  listModelsPage,
} from './pageApi';
import type { PageWindow } from './pageTypes';
import type { CandidateFilters } from './types';

function fixture(path: string): Record<string, unknown> {
  return JSON.parse(readFileSync(resolve(process.cwd(), '..', path), 'utf8')) as Record<
    string,
    unknown
  >;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function totalPages(total: bigint, pageSize: PageSize): string {
  return (total === 0n ? 1n : (total - 1n) / BigInt(pageSize) + 1n).toString();
}

function numbered(
  data: readonly unknown[],
  options: {
    page?: string;
    pageSize?: PageSize;
    totalItems?: bigint;
    nextCursor?: unknown;
  } = {},
): Record<string, unknown> {
  const pageSize = options.pageSize ?? 20;
  const totalItems = options.totalItems ?? BigInt(data.length);
  return {
    data,
    next_cursor: options.nextCursor ?? null,
    pagination: {
      page: options.page ?? '1',
      page_size: pageSize,
      total_items: totalItems.toString(),
      total_pages: totalPages(totalItems, pageSize),
    },
  };
}

function catalogPage(
  automaticEntries: readonly unknown[],
  manualEntries: readonly unknown[],
  options: {
    page?: string;
    pageSize?: PageSize;
    totalItems?: bigint;
    nextCursor?: unknown;
  } = {},
): Record<string, unknown> {
  const pageSize = options.pageSize ?? 20;
  const totalItems = options.totalItems ?? BigInt(automaticEntries.length + manualEntries.length);
  return {
    evidence: {
      state: 'unknown',
      revision: '1',
      result: null,
      safe_class: 'none',
      observed_at: null,
      count: null,
    },
    automatic_entries: automaticEntries,
    manual_entries: manualEntries,
    next_cursor: options.nextCursor ?? null,
    pagination: {
      page: options.page ?? '1',
      page_size: pageSize,
      total_items: totalItems.toString(),
      total_pages: totalPages(totalItems, pageSize),
    },
  };
}

const endpoint = fixture('internal/resources/testdata/endpoint.json');
const endpointKey = fixture('internal/resources/testdata/endpoint_key.json');
const candidatePage = fixture('internal/resources/testdata/binding_candidates.json');
const candidate = (candidatePage.data as unknown[])[0] as Record<string, unknown>;
const catalogEntry = {
  id: '41',
  source_type: 'automatic',
  upstream_model_id: 'Vendor/Auto',
  provider: 'Vendor',
  source_revision: '1',
  pair_revision: '1',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_000,
};
const model = {
  id: '31',
  provider: 'Vendor',
  model: 'Exact',
  full_name: 'Vendor/Exact',
  route_strategy: 'ordered',
  silent_retry: false,
  flatten_tool_calls: false,
  revision: '1',
  binding_revision: '0',
  binding_count: '0',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_000,
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('numbered core resource API', () => {
  it('preserves literal source and key searches and rejects invalid text before transport', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(numbered([])));
    vi.stubGlobal('fetch', fetchMock);
    const search = ' Example%_😀 ';
    await listEndpointsPage({ page: '1', pageSize: 20 }, undefined, search);
    await listEndpointKeysPage('11', { page: '1', pageSize: 20 }, undefined, search);
    for (const [input] of fetchMock.mock.calls) {
      const params = new URL(String(input), 'https://example.test').searchParams;
      expect(params.get('q')).toBe(search.trim());
      expect(params.has('cursor')).toBe(false);
    }
    await listEndpointsPage({ page: '1', pageSize: 20 }, undefined, '😀'.repeat(128));
    fetchMock.mockClear();
    for (const invalid of ['a'.repeat(129), '😀'.repeat(129), '\u0001', '\u0080', '\ud800']) {
      await expect(
        listEndpointsPage({ page: '1', pageSize: 20 }, undefined, invalid),
      ).rejects.toMatchObject({ code: 'invalid_request' });
      await expect(
        listEndpointKeysPage('11', { page: '1', pageSize: 20 }, undefined, invalid),
      ).rejects.toMatchObject({ code: 'invalid_request' });
    }
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('requires an explicit donation eligibility whenever a key carries a browse summary', async () => {
    const browse = {
      model_count: '0',
      binding_count: '0',
      available_binding_count: '0',
      preview: [],
      discovery: {
        state: 'unknown',
        revision: '1',
        result: null,
        safe_class: 'none',
        observed_at: null,
        count: null,
      },
    };
    const fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', fetchMock);
    for (const eligibility of ['eligible', 'already_donated', 'security_processing']) {
      fetchMock.mockResolvedValueOnce(
        jsonResponse(
          numbered([{ ...endpointKey, browse: { ...browse, donation_eligibility: eligibility } }]),
        ),
      );
      await expect(listEndpointKeysPage('11', { page: '1', pageSize: 20 })).resolves.toMatchObject({
        data: [{ browse: { donation_eligibility: eligibility } }],
      });
    }
    for (const eligibility of [undefined, null, 'unknown', true]) {
      fetchMock.mockResolvedValueOnce(
        jsonResponse(
          numbered([{ ...endpointKey, browse: { ...browse, donation_eligibility: eligibility } }]),
        ),
      );
      await expect(listEndpointKeysPage('11', { page: '1', pageSize: 20 })).rejects.toMatchObject({
        code: 'invalid_response',
      });
    }
  });

  it('uses the page paths and decodes all five resource collections', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse(numbered([endpoint])))
      .mockResolvedValueOnce(jsonResponse(numbered([endpointKey])))
      .mockResolvedValueOnce(jsonResponse(numbered([model])))
      .mockResolvedValueOnce(jsonResponse(catalogPage([catalogEntry], [])))
      .mockResolvedValueOnce(jsonResponse(numbered([candidate])));
    vi.stubGlobal('fetch', fetchMock);

    await expect(listEndpointsPage({ page: '1', pageSize: 20 })).resolves.toMatchObject({
      data: [{ id: '11' }],
      next_cursor: null,
      pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
    });
    await expect(listEndpointKeysPage('11', { page: '1', pageSize: 20 })).resolves.toMatchObject({
      data: [{ id: '21', endpoint_id: '11' }],
    });
    await expect(listModelsPage({ page: '1', pageSize: 20 })).resolves.toMatchObject({
      data: [{ id: '31' }],
    });
    await expect(
      getCatalogPage('11', '21', { page: '1', pageSize: 20 }, 'automatic'),
    ).resolves.toMatchObject({
      automatic_entries: [{ id: '41', source_type: 'automatic' }],
      manual_entries: [],
      pagination: { page: '1', page_size: 20 },
    });
    await expect(
      getBindingCandidatesPage(
        '31',
        { endpointId: '11', keyId: '21', source: 'manual', query: 'Vendor' },
        { page: '1', pageSize: 20 },
      ),
    ).resolves.toMatchObject({ data: [{ endpoint_key_id: '21' }] });

    expect(fetchMock.mock.calls.map(([input]) => input)).toEqual([
      '/api/endpoints?page=1&page_size=20',
      '/api/endpoints/11/keys?page=1&page_size=20',
      '/api/models?page=1&page_size=20',
      '/api/endpoints/11/keys/21/models?page=1&page_size=20&source=automatic',
      '/api/models/31/binding-candidates?endpoint_id=11&key_id=21&source=manual&q=Vendor&page=1&page_size=20',
    ]);
  });

  it('supports every page size, empty sets, and server-side last-page clamping', async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input) => {
      const url = new URL(String(input), 'https://example.test');
      const page = url.searchParams.get('page');
      const pageSize = Number(url.searchParams.get('page_size')) as PageSize;
      if (page === '2147483647') {
        return jsonResponse(numbered([model], { page: '2', pageSize, totalItems: 21n }));
      }
      return jsonResponse(numbered([], { page: '1', pageSize, totalItems: 0n }));
    });
    vi.stubGlobal('fetch', fetchMock);

    for (const pageSize of PAGE_SIZES) {
      await expect(listModelsPage({ page: '1', pageSize })).resolves.toMatchObject({
        data: [],
        pagination: { page: '1', page_size: pageSize, total_items: '0', total_pages: '1' },
      });
    }
    await expect(listModelsPage({ page: '2147483647', pageSize: 20 })).resolves.toMatchObject({
      data: [{ id: '31' }],
      pagination: { page: '2', page_size: 20, total_items: '21', total_pages: '2' },
    });
    expect(fetchMock.mock.calls.at(-1)?.[0]).toBe('/api/models?page=2147483647&page_size=20');
  });

  it('keeps large decimal counts as strings while accepting the maximum request page', async () => {
    const data = Array.from({ length: 10 }, (_, index) => ({
      ...endpoint,
      id: String(index + 1),
    }));
    const totalItems = 9_223_372_036_854_775_807n;
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse(
        numbered(data, {
          page: '2147483647',
          pageSize: 10,
          totalItems,
        }),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);

    const result = await listEndpointsPage({ page: '2147483647', pageSize: 10 });
    expect(result.pagination).toEqual({
      page: '2147483647',
      page_size: 10,
      total_items: totalItems.toString(),
      total_pages: totalPages(totalItems, 10),
    });
    expect(result.data).toHaveLength(10);
  });

  it('rejects malformed page windows and candidate cursor or limit injection before fetch', async () => {
    const fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', fetchMock);
    const invalidWindows: unknown[] = [
      { page: '0', pageSize: 20 },
      { page: '01', pageSize: 20 },
      { page: '-1', pageSize: 20 },
      { page: '1.0', pageSize: 20 },
      { page: '2147483648', pageSize: 20 },
      { page: 1, pageSize: 20 },
      { page: '1', pageSize: 15 },
      { page: '1' },
      { page: '1', pageSize: 20, extra: true },
    ];
    for (const window of invalidWindows) {
      await expect(listModelsPage(window as PageWindow)).rejects.toMatchObject({
        code: 'invalid_request',
      });
    }
    await expect(
      getBindingCandidatesPage(
        '31',
        { cursor: 'forged' } as unknown as Omit<CandidateFilters, 'cursor' | 'limit'>,
        { page: '1', pageSize: 20 },
      ),
    ).rejects.toMatchObject({ code: 'invalid_request' });
    await expect(
      getBindingCandidatesPage(
        '31',
        { limit: 20 } as unknown as Omit<CandidateFilters, 'cursor' | 'limit'>,
        { page: '1', pageSize: 20 },
      ),
    ).rejects.toMatchObject({ code: 'invalid_request' });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('rejects closed response shapes, bad pagination, duplicates, and oversized pages', async () => {
    const fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', fetchMock);
    const request: PageWindow = { page: '1', pageSize: 20 };
    const expectRejected = async (body: unknown) => {
      fetchMock.mockReset();
      fetchMock.mockResolvedValueOnce(jsonResponse(body));
      await expect(listModelsPage(request)).rejects.toMatchObject({ code: 'invalid_response' });
    };

    await expectRejected({ ...numbered([model]), extra: true });
    await expectRejected(numbered([model], { nextCursor: 'cursor' }));
    const validPagination = numbered([model]).pagination as Record<string, unknown>;
    await expectRejected({
      ...numbered([model]),
      pagination: { ...validPagination, page_size: 10 },
    });
    await expectRejected(numbered([model], { page: '1', pageSize: 20, totalItems: 21n }));
    await expectRejected(numbered([model, model], { totalItems: 2n }));
    await expectRejected({
      ...numbered([]),
      pagination: {
        page: '1',
        page_size: 20,
        total_items: '9223372036854775808',
        total_pages: '1',
      },
    });
    const oversized = Array.from({ length: 101 }, (_, index) => ({
      ...model,
      id: String(index + 1),
    }));
    await expectRejected(numbered(oversized, { totalItems: 101n }));
  });

  it('rejects foreign endpoint keys, foreign candidate keys, and duplicate candidate pairs', async () => {
    const fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', fetchMock);

    fetchMock.mockResolvedValueOnce(
      jsonResponse(numbered([{ ...endpointKey, endpoint_id: '99' }])),
    );
    await expect(listEndpointKeysPage('11', { page: '1', pageSize: 20 })).rejects.toMatchObject({
      code: 'invalid_response',
    });

    fetchMock.mockResolvedValueOnce(
      jsonResponse(numbered([{ ...candidate, endpoint_key_id: '99' }])),
    );
    await expect(
      getBindingCandidatesPage('31', { keyId: '21' }, { page: '1', pageSize: 20 }),
    ).rejects.toMatchObject({ code: 'invalid_response' });

    fetchMock.mockResolvedValueOnce(
      jsonResponse(numbered([candidate, candidate], { totalItems: 2n })),
    );
    await expect(
      getBindingCandidatesPage('31', {}, { page: '1', pageSize: 20 }),
    ).rejects.toMatchObject({ code: 'invalid_response' });
  });

  it('requires source-filtered catalog pages to leave the other projection empty', async () => {
    const manualEntry = { ...catalogEntry, id: '42', source_type: 'manual' };
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse(catalogPage([catalogEntry], [manualEntry], { totalItems: 2n })),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(
      getCatalogPage('11', '21', { page: '1', pageSize: 20 }, 'automatic'),
    ).rejects.toMatchObject({ code: 'invalid_response' });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('passes the abort signal through the shared request boundary', async () => {
    const controller = new AbortController();
    const abortError = new DOMException('aborted', 'AbortError');
    const fetchMock = vi.fn<typeof fetch>(async (_input, init) => {
      expect(init?.signal).toBe(controller.signal);
      throw abortError;
    });
    vi.stubGlobal('fetch', fetchMock);
    controller.abort();

    await expect(listModelsPage({ page: '1', pageSize: 20 }, controller.signal)).rejects.toBe(
      abortError,
    );
  });
});
