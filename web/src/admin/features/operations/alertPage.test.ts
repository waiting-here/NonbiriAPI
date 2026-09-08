import { afterEach, describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures } from '../../../../test/unit/support';
import {
  adminAlertPageKeys,
  getAdminAlertPage,
  normalizeAdminAlertPageResponse,
} from './alertPage';

afterEach(() => {
  vi.unstubAllGlobals();
});

function alert(
  id = '1',
  resolved = false,
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  return {
    id,
    kind: 'fetch_failed',
    message: `Alert ${id}`,
    ref: null,
    subject_user_id: null,
    created_at: 1_800_000_000,
    resolved,
    resolved_at: resolved ? 1_800_000_001 : null,
    ...overrides,
  };
}

function page(
  data: unknown[],
  pagination: Record<string, unknown> = {
    page: '1',
    page_size: 20,
    total_items: String(data.length),
    total_pages: '1',
  },
  next_cursor: unknown = null,
) {
  return { data, next_cursor, pagination };
}

describe('administrator alert page contract', () => {
  it('preserves normalized alert fields and accepts a complete page window', () => {
    expect(
      normalizeAdminAlertPageResponse(
        page([alert('1', true, { ref: '<safe text>', subject_user_id: '7' })]),
        'true',
        '1',
        20,
      ),
    ).toMatchObject({
      data: [
        {
          id: '1',
          message: 'Alert 1',
          ref: '<safe text>',
          subject_user_id: '7',
          resolved: true,
          resolved_at: 1_800_000_001,
        },
      ],
      next_cursor: null,
      pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
    });
  });

  it('accepts the canonical empty page and clamps an oversized request to the last page', () => {
    expect(
      normalizeAdminAlertPageResponse(
        page([], { page: '1', page_size: 20, total_items: '0', total_pages: '1' }),
        'all',
        '1',
        20,
      ),
    ).toMatchObject({ data: [], pagination: { page: '1', total_pages: '1' } });

    expect(
      normalizeAdminAlertPageResponse(
        page(
          [alert('2', true)],
          { page: '2', page_size: 20, total_items: '21', total_pages: '2' },
        ),
        'true',
        '2147483647',
        20,
      ),
    ).toMatchObject({ pagination: { page: '2', total_items: '21', total_pages: '2' } });
  });

  it.each([
    ['an unknown envelope field', { ...page([alert()]), extra: true }],
    ['a non-null cursor', page([alert()], undefined, 'cursor')],
    ['a row from a different resolution filter', page([alert('2', true)])],
    [
      'duplicate alert identities',
      page([alert(), alert()], { page: '1', page_size: 20, total_items: '2', total_pages: '1' }),
    ],
    [
      'a row count outside the declared window',
      page([], { page: '1', page_size: 20, total_items: '1', total_pages: '1' }),
    ],
    [
      'metadata for a different requested size',
      page([alert()], { page: '1', page_size: 50, total_items: '1', total_pages: '1' }),
    ],
    [
      'an unknown pagination field',
      {
        ...page([alert()]),
        pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1', extra: 1 },
      },
    ],
    ['an unknown alert field', page([{ ...alert(), private_detail: 'secret' }])],
  ])('rejects %s', (_label, value) => {
    expect(() => normalizeAdminAlertPageResponse(value, 'false', '1', 20)).toThrow();
  });
});

describe('administrator alert page requests', () => {
  it.each([
    ['false', '/admin/api/alerts?resolved=false&page=2&page_size=50'],
    ['true', '/admin/api/alerts?resolved=true&page=2&page_size=50'],
    ['all', '/admin/api/alerts?page=2&page_size=50'],
  ] as const)('sends the %s filter with page-only parameters', async (resolved, path) => {
    const controller = new AbortController();
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path,
        body: page(
          [alert('2', resolved === 'true')],
          { page: '2', page_size: 50, total_items: '51', total_pages: '2' },
        ),
      },
    ]);

    const result = await getAdminAlertPage(resolved, '2', 50, controller.signal);

    expect(result.pagination.page).toBe('2');
    expect(fetchMock).toHaveBeenCalledWith(path, expect.objectContaining({ signal: controller.signal }));
    expect(fetchMock.mock.calls[0]?.[0]).not.toContain('cursor=');
    expect(fetchMock.mock.calls[0]?.[0]).not.toContain('limit=');
  });

  it.each([
    ['01', 20, 'false'],
    ['2147483648', 20, 'false'],
    ['1', 30, 'false'],
    ['1', 20, 'unknown'],
  ] as const)('rejects invalid request parameters before the network call: %s/%s/%s', async (pageNumber, size, resolved) => {
    const fetchMock = installJsonFetchFixtures([]);
    await expect(getAdminAlertPage(resolved as never, pageNumber, size as never)).rejects.toMatchObject({
      code: 'invalid_request',
      status: 400,
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('keeps the account, filter, page, and size dimensions in the query key', () => {
    expect(adminAlertPageKeys.page('admin:root', 'true', '3', 100)).toEqual([
      'admin',
      'operations',
      'alerts',
      'admin:root',
      'true',
      '3',
      100,
    ]);
  });
});
