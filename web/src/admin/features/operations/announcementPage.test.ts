import { afterEach, describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures } from '../../../../test/unit/support';
import {
  getAdminAnnouncements,
  getAdminAnnouncementsPage,
  normalizeAdminAnnouncementPage,
} from './announcements';

afterEach(() => {
  vi.unstubAllGlobals();
});

const announcementID = (suffix: string): string => `ann_${suffix.padEnd(22, 'A').slice(0, 22)}`;

function announcement(suffix = 'A') {
  return {
    id: announcementID(suffix),
    state: 'draft',
    revision: '1',
    draft: { zh: { title: '公告', body: '正文' }, en: null },
    published: null,
    severity: 'info',
    pinned: false,
    dismissible: true,
    expires_at: null,
    withdrawn_at: null,
    created_at: 1_800_000_000,
    updated_at: 1_800_000_001,
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

describe('administrator announcement page contract', () => {
  it('accepts exact windows and the canonical empty response', () => {
    expect(
      normalizeAdminAnnouncementPage(
        page([announcement()], { page: '2', page_size: 20, total_items: '21', total_pages: '2' }),
      ),
    ).toMatchObject({
      data: [announcement()],
      next_cursor: null,
      pagination: { page: '2', page_size: 20, total_items: '21', total_pages: '2' },
    });
    expect(
      normalizeAdminAnnouncementPage(
        page([], { page: '1', page_size: 100, total_items: '0', total_pages: '1' }),
      ),
    ).toMatchObject({ data: [], pagination: { page_size: 100, total_pages: '1' } });
  });

  it.each([
    ['a cursor in a page response', page([announcement()], undefined, 'next')],
    [
      'an item count that does not match the window',
      page([], { page: '1', page_size: 20, total_items: '1', total_pages: '1' }),
    ],
    [
      'duplicate announcement IDs',
      page([announcement('A'), announcement('A')], {
        page: '1',
        page_size: 20,
        total_items: '2',
        total_pages: '1',
      }),
    ],
    ['an extra envelope field', { ...page([announcement()]), extra: true }],
  ])('rejects %s', (_label, value) => {
    expect(() => normalizeAdminAnnouncementPage(value)).toThrow();
  });
});

describe('administrator announcement page request', () => {
  it.each([
    ['wrong size', { page: '1', page_size: 10, total_items: '1', total_pages: '1' }],
    ['wrong page', { page: '2', page_size: 20, total_items: '21', total_pages: '2' }],
  ])(
    'rejects internally consistent metadata for a different request: %s',
    async (_label, metadata) => {
      installJsonFetchFixtures([
        {
          method: 'GET',
          path: '/admin/api/announcements?page=1&page_size=20',
          body: page([announcement()], metadata),
        },
      ]);
      await expect(getAdminAnnouncementsPage('', '', '1', 20)).rejects.toMatchObject({
        code: 'invalid_response',
      });
    },
  );

  it('sends filters, page parameters, and AbortSignal without cursor or limit', async () => {
    const controller = new AbortController();
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/admin/api/announcements?state=published&severity=warning&page=2&page_size=50',
        body: page([announcement()], {
          page: '2',
          page_size: 50,
          total_items: '51',
          total_pages: '2',
        }),
      },
    ]);

    const result = await getAdminAnnouncementsPage(
      'published',
      'warning',
      '2',
      50,
      controller.signal,
    );

    expect(result.pagination.page).toBe('2');
    expect(fetchMock).toHaveBeenCalledWith(
      '/admin/api/announcements?state=published&severity=warning&page=2&page_size=50',
      expect.objectContaining({ signal: controller.signal }),
    );
  });

  it('accepts the server page after an oversized request is clamped', async () => {
    installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/admin/api/announcements?page=2147483647&page_size=20',
        body: page([announcement()], {
          page: '2',
          page_size: 20,
          total_items: '21',
          total_pages: '2',
        }),
      },
    ]);

    await expect(getAdminAnnouncementsPage('', '', '2147483647', 20)).resolves.toMatchObject({
      pagination: { page: '2', total_items: '21', total_pages: '2' },
    });
  });

  it('keeps the cursor client function available for existing callers', async () => {
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/admin/api/announcements?cursor=next&limit=50',
        body: { data: [], next_cursor: null },
      },
    ]);

    await expect(getAdminAnnouncements('', '', 'next')).resolves.toEqual({
      data: [],
      next_cursor: null,
    });
    expect(fetchMock).toHaveBeenCalledWith(
      '/admin/api/announcements?cursor=next&limit=50',
      expect.objectContaining({ cache: 'no-store', credentials: 'same-origin' }),
    );
  });

  it.each([
    ['01', 20],
    ['2147483648', 20],
    ['1', 30],
  ])('rejects invalid page input before requesting: %s/%s', async (pageNumber, pageSize) => {
    const fetchMock = installJsonFetchFixtures([]);
    await expect(
      getAdminAnnouncementsPage('', '', pageNumber, pageSize as never),
    ).rejects.toMatchObject({ code: 'invalid_request', status: 400 });
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
