import { afterEach, describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures } from '../../../../test/unit/support';
import {
  adminMainstreamChannelPageKeys,
  getAdminMainstreamChannelPage,
  normalizeAdminMainstreamChannelPage,
} from './channelPage';
import type { AdminMainstreamChannel } from './channels';

afterEach(() => {
  vi.unstubAllGlobals();
});

const activeID = `mch_${'A'.repeat(22)}`;
const retiredID = `mch_${'Q'.repeat(22)}`;

const active: AdminMainstreamChannel = {
  id: activeID,
  name: 'Example channel',
  category: 'subscription',
  connector_type: 'openai-compatible',
  base_url: 'https://api.example.test/v1',
  enabled: true,
  state: 'active',
  revision: '3',
  created_at: 1_800_000_000,
  updated_at: 1_800_000_001,
  retired_at: null,
};

const retired: AdminMainstreamChannel = {
  ...active,
  id: retiredID,
  name: 'Retired channel',
  enabled: false,
  state: 'retired',
  revision: '4',
  updated_at: 1_800_000_002,
  retired_at: 1_800_000_003,
};

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

describe('administrator mainstream channel page wire', () => {
  it('preserves the channel projection and accepts a matching page window', () => {
    expect(
      normalizeAdminMainstreamChannelPage(
        page([active], { page: '2', page_size: 20, total_items: '21', total_pages: '2' }),
        'active',
        '2',
        20,
      ),
    ).toEqual({
      data: [active],
      next_cursor: null,
      pagination: { page: '2', page_size: 20, total_items: '21', total_pages: '2' },
    });
  });

  it('accepts all-state rows and a server-clamped oversized page', () => {
    expect(
      normalizeAdminMainstreamChannelPage(
        page([active, retired], {
          page: '1',
          page_size: 50,
          total_items: '2',
          total_pages: '1',
        }),
        'all',
        '1',
        50,
      ).data,
    ).toEqual([active, retired]);
    expect(
      normalizeAdminMainstreamChannelPage(
        page([retired], {
          page: '2',
          page_size: 20,
          total_items: '21',
          total_pages: '2',
        }),
        'retired',
        '2147483647',
        20,
      ).pagination.page,
    ).toBe('2');
  });

  it.each([
    ['an unknown envelope field', { ...page([active]), extra: true }],
    ['a non-null cursor', page([active], undefined, 'cursor')],
    ['a row from a different state', page([retired])],
    [
      'duplicate channel identities',
      page([active, active], {
        page: '1',
        page_size: 20,
        total_items: '2',
        total_pages: '1',
      }),
    ],
    [
      'a row count outside the declared window',
      page([], {
        page: '1',
        page_size: 20,
        total_items: '1',
        total_pages: '1',
      }),
    ],
    [
      'metadata for a different requested size',
      page([active], {
        page: '1',
        page_size: 50,
        total_items: '1',
        total_pages: '1',
      }),
    ],
    [
      'an unknown pagination field',
      {
        ...page([active]),
        pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1', extra: 1 },
      },
    ],
  ])('rejects %s', (_label, value) => {
    expect(() => normalizeAdminMainstreamChannelPage(value, 'active', '1', 20)).toThrow();
  });
});

describe('administrator mainstream channel page requests', () => {
  it('sends only page parameters and forwards AbortSignal', async () => {
    const controller = new AbortController();
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/admin/api/mainstream-channels?state=retired&page=2&page_size=50',
        body: page([retired], {
          page: '2',
          page_size: 50,
          total_items: '51',
          total_pages: '2',
        }),
      },
    ]);

    const result = await getAdminMainstreamChannelPage('retired', '2', 50, controller.signal);

    expect(result.pagination.page).toBe('2');
    expect(fetchMock).toHaveBeenCalledWith(
      '/admin/api/mainstream-channels?state=retired&page=2&page_size=50',
      expect.objectContaining({ signal: controller.signal }),
    );
    expect(String(fetchMock.mock.calls[0]?.[0])).not.toContain('cursor=');
    expect(String(fetchMock.mock.calls[0]?.[0])).not.toContain('limit=');
  });

  it.each([10, 20, 50, 100] as const)('accepts page size %s', async (pageSize) => {
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: `/admin/api/mainstream-channels?state=active&page=1&page_size=${pageSize}`,
        body: page([active], {
          page: '1',
          page_size: pageSize,
          total_items: '1',
          total_pages: '1',
        }),
      },
    ]);

    const result = await getAdminMainstreamChannelPage('active', '1', pageSize);

    expect(result.pagination.page_size).toBe(pageSize);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it.each([
    ['unknown state', 'invalid', '1', 20],
    ['non-canonical page', 'active', '01', 20],
    ['oversized page', 'active', '2147483648', 20],
    ['unsupported page size', 'active', '1', 30],
  ])('rejects %s before the network call', async (_label, state, pageNumber, size) => {
    const fetchMock = installJsonFetchFixtures([]);
    await expect(
      getAdminMainstreamChannelPage(state as never, pageNumber, size as never),
    ).rejects.toMatchObject({ code: 'invalid_request', status: 400 });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('keeps the admin account, filter, page, and size in a root-compatible query key', () => {
    expect(adminMainstreamChannelPageKeys.page('root', 'retired', '3', 100)).toEqual([
      'admin',
      'operations',
      'mainstream-channels',
      'page',
      'root',
      'retired',
      '3',
      100,
    ]);
  });
});
