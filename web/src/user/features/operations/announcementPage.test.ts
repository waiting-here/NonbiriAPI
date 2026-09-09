import { afterEach, describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures } from '../../../../test/unit/support';
import { getAnnouncementsPage, normalizeAnnouncementPage } from './data';

afterEach(() => {
  vi.unstubAllGlobals();
});

const announcementID = (suffix: string): string => `ann_${suffix.padEnd(22, 'A').slice(0, 22)}`;
const epoch = `b1e_${'A'.repeat(22)}`;

function summary(suffix = 'A', overrides: Record<string, unknown> = {}) {
  return {
    epoch,
    id: announcementID(suffix),
    revision: '1',
    severity: 'info',
    pinned: false,
    dismissible: true,
    published_at: 1_800_000_000,
    expires_at: null,
    effective_language: 'en',
    fallback_from: null,
    title: `Announcement ${suffix}`,
    excerpt: `Summary ${suffix}`,
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

describe('user announcement page contract', () => {
  it('accepts exact server windows, including an empty canonical page', () => {
    expect(
      normalizeAnnouncementPage(
        page([summary()], { page: '2', page_size: 20, total_items: '21', total_pages: '2' }),
      ),
    ).toMatchObject({
      data: [summary()],
      next_cursor: null,
      pagination: { page: '2', page_size: 20, total_items: '21', total_pages: '2' },
    });
    expect(
      normalizeAnnouncementPage(
        page([], { page: '1', page_size: 100, total_items: '0', total_pages: '1' }),
      ),
    ).toMatchObject({ data: [], pagination: { page_size: 100, total_pages: '1' } });
  });

  it.each([
    ['a cursor in a page response', page([summary()], undefined, 'next')],
    [
      'an item count that does not match the window',
      page([], { page: '1', page_size: 20, total_items: '1', total_pages: '1' }),
    ],
    [
      'duplicate announcement IDs',
      page([summary('A'), summary('A')], {
        page: '1',
        page_size: 20,
        total_items: '2',
        total_pages: '1',
      }),
    ],
    ['an extra envelope field', { ...page([summary()]), extra: true }],
  ])('rejects %s', (_label, value) => {
    expect(() => normalizeAnnouncementPage(value)).toThrow();
  });
});

describe('user announcement page request', () => {
  it.each([
    ['wrong size', { page: '1', page_size: 10, total_items: '1', total_pages: '1' }],
    ['wrong page', { page: '2', page_size: 20, total_items: '21', total_pages: '2' }],
  ])(
    'rejects internally consistent metadata for a different request: %s',
    async (_label, metadata) => {
      installJsonFetchFixtures([
        {
          method: 'GET',
          path: '/api/announcements?page=1&page_size=20',
          body: page([summary()], metadata),
        },
      ]);
      await expect(getAnnouncementsPage('1', 20)).rejects.toMatchObject({
        code: 'invalid_response',
      });
    },
  );

  it('sends only page parameters and forwards AbortSignal', async () => {
    const controller = new AbortController();
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/api/announcements?page=2&page_size=50',
        body: page([summary()], { page: '2', page_size: 50, total_items: '51', total_pages: '2' }),
      },
    ]);

    const result = await getAnnouncementsPage('2', 50, controller.signal);

    expect(result.pagination.page).toBe('2');
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/announcements?page=2&page_size=50',
      expect.objectContaining({ signal: controller.signal }),
    );
  });

  it('accepts the server page after an oversized request is clamped', async () => {
    installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/api/announcements?page=2147483647&page_size=20',
        body: page([summary()], { page: '2', page_size: 20, total_items: '21', total_pages: '2' }),
      },
    ]);

    await expect(getAnnouncementsPage('2147483647', 20)).resolves.toMatchObject({
      pagination: { page: '2', total_items: '21', total_pages: '2' },
    });
  });

  it.each([
    ['01', 20],
    ['2147483648', 20],
    ['1', 30],
  ])('rejects invalid page input before requesting: %s/%s', async (pageNumber, pageSize) => {
    const fetchMock = installJsonFetchFixtures([]);
    await expect(getAnnouncementsPage(pageNumber, pageSize as never)).rejects.toMatchObject({
      code: 'invalid_request',
      status: 400,
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
