import { afterEach, describe, expect, it, vi } from 'vitest';
import { getCharityCatalog, normalizeCharityCatalog } from './catalog';
import { installJsonFetchFixtures } from '../../../../test/unit/support';

afterEach(() => {
  vi.unstubAllGlobals();
});

const SERVER_NOW = 1_800_000_000;

function catalogModel(overrides: Record<string, unknown> = {}) {
  return {
    id: '1',
    provider: 'provider',
    model: 'model',
    full_name: '[公益]provider/model',
    pricing: {
      mode: 'per_request',
      user_price_milli: '3000',
      discounted_user_price_milli: '2400',
      user_prices_milli: null,
      discounted_user_prices_milli: null,
    },
    discount: {
      enabled: true,
      percent: 80,
      start_at: SERVER_NOW - 10,
      end_at: SERVER_NOW + 100,
    },
    public_description: '<b>plain</b>\nsecond line',
    enabled: true,
    allowed_levels: [1, 3, 5],
    level_allowed: true,
    availability: 'available',
    ...overrides,
  };
}

function catalogPage(
  models: unknown[] = [catalogModel()],
  pagination: Record<string, unknown> = {
    page: '1',
    page_size: 20,
    total_items: '1',
    total_pages: '1',
  },
) {
  return {
    models,
    pagination,
    donation_intake: 'open',
    server_now: SERVER_NOW,
  };
}

describe('charity catalog normalizer', () => {
  it('accepts the complete public projection and keeps plain text and exact prices', () => {
    const result = normalizeCharityCatalog(catalogPage());
    expect(result).toMatchObject({
      donationIntake: 'open',
      serverNow: SERVER_NOW,
      pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
    });
    expect(result.models[0]).toMatchObject({
      id: '1',
      fullName: '[公益]provider/model',
      publicDescription: '<b>plain</b>\nsecond line',
      enabled: true,
      allowedLevels: [1, 3, 5],
      levelAllowed: true,
      availability: 'available',
      pricing: { userPriceMilli: '3000', discountedUserPriceMilli: '2400' },
    });
  });

  it('accepts an empty page while retaining the canonical one-page metadata', () => {
    expect(
      normalizeCharityCatalog(
        catalogPage([], { page: '1', page_size: 50, total_items: '0', total_pages: '1' }),
      ),
    ).toMatchObject({ models: [], pagination: { page: '1', page_size: 50 } });
  });

  it('rejects private or malformed model fields and inconsistent availability', () => {
    expect(() =>
      normalizeCharityCatalog(catalogPage([catalogModel({ source: 'private' })])),
    ).toThrow();
    expect(() =>
      normalizeCharityCatalog(catalogPage([catalogModel({ allowed_levels: [3, 1] })])),
    ).toThrow();
    expect(() =>
      normalizeCharityCatalog(catalogPage([catalogModel({ allowed_levels: [1, 1] })])),
    ).toThrow();
    expect(() =>
      normalizeCharityCatalog(catalogPage([catalogModel({ availability: 'unknown' })])),
    ).toThrow();
    expect(() =>
      normalizeCharityCatalog(
        catalogPage([catalogModel({ availability: 'available', enabled: false })]),
      ),
    ).toThrow();
    expect(() =>
      normalizeCharityCatalog(
        catalogPage([catalogModel({ availability: 'level_denied', level_allowed: true })]),
      ),
    ).toThrow();
  });

  it('enforces the public-description text boundary while allowing LF and TAB', () => {
    expect(
      normalizeCharityCatalog(
        catalogPage([catalogModel({ public_description: 'line 1\nline 2\t' })]),
      ).models[0].publicDescription,
    ).toBe('line 1\nline 2\t');
    expect(() =>
      normalizeCharityCatalog(catalogPage([catalogModel({ public_description: 'bad\r' })])),
    ).toThrow();
    expect(() =>
      normalizeCharityCatalog(catalogPage([catalogModel({ public_description: '\u0080' })])),
    ).toThrow();
    expect(() =>
      normalizeCharityCatalog(
        catalogPage([catalogModel({ public_description: 'x'.repeat(1_025) })]),
      ),
    ).toThrow();
    expect(() =>
      normalizeCharityCatalog(
        catalogPage([catalogModel({ public_description: '界'.repeat(1_367) })]),
      ),
    ).toThrow();
  });

  it('rejects a page whose item count does not match the server window', () => {
    expect(() =>
      normalizeCharityCatalog(
        catalogPage([catalogModel()], {
          page: '2',
          page_size: 20,
          total_items: '20',
          total_pages: '1',
        }),
      ),
    ).toThrow();
    expect(() =>
      normalizeCharityCatalog(
        catalogPage([catalogModel()], {
          page: '2',
          page_size: 20,
          total_items: '22',
          total_pages: '2',
        }),
      ),
    ).toThrow();
  });
});

describe('charity catalog request', () => {
  it('sends page, search, and access filters in the catalog query', async () => {
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/api/charity/models?view=catalog&page=2&page_size=50&q=public&allowed_for_me=false',
        body: catalogPage(
          [catalogModel({ id: '2', model: 'other', full_name: '[公益]provider/other' })],
          {
            page: '2',
            page_size: 50,
            total_items: '51',
            total_pages: '2',
          },
        ),
      },
    ]);
    const result = await getCharityCatalog({
      page: '2',
      pageSize: 50,
      query: 'public',
      allowedForMe: 'false',
    });
    expect(result.pagination.page).toBe('2');
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/charity/models?view=catalog&page=2&page_size=50&q=public&allowed_for_me=false',
      expect.objectContaining({ signal: undefined }),
    );
  });

  it('rejects invalid search bounds before making a request', async () => {
    const fetchMock = installJsonFetchFixtures([]);
    await expect(
      getCharityCatalog({ page: '1', pageSize: 20, query: '界'.repeat(129), allowedForMe: 'all' }),
    ).rejects.toMatchObject({ code: 'invalid_request', status: 400 });
    await expect(
      getCharityCatalog({ page: '1', pageSize: 20, query: 'bad\u0000query', allowedForMe: 'all' }),
    ).rejects.toMatchObject({ code: 'invalid_request', status: 400 });
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
