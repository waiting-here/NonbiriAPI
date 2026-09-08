import { afterEach, describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures } from '../../../../test/unit/support';
import { getIssuePage, issuePageKeys, normalizeIssuePageResponse } from './issuePage';

afterEach(() => {
  vi.unstubAllGlobals();
});

const issueID = (suffix: string): string => `iss_${suffix.padEnd(22, 'A').slice(0, 22)}`;

function issue(suffix = 'A', overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: issueID(suffix),
    state: 'current',
    source: 'model_discovery',
    resource_kind: 'endpoint_key',
    summary_code: 'discovery_failed',
    safe_detail: '',
    deep_link: null,
    first_seen_at: 1_800_000_000,
    last_seen_at: 1_800_000_001,
    count: '1',
    closed_at: null,
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
  projection_incomplete = false,
) {
  return { data, next_cursor, projection_incomplete, pagination };
}

describe('user issue page contract', () => {
  it('preserves the domain projection and accepts an exact page window', () => {
    expect(
      normalizeIssuePageResponse(
        page(
          [issue()],
          {
            page: '2',
            page_size: 20,
            total_items: '21',
            total_pages: '2',
          },
          null,
          true,
        ),
        'current',
        '2',
        20,
      ),
    ).toMatchObject({
      data: [issue()],
      next_cursor: null,
      projection_incomplete: true,
      pagination: { page: '2', page_size: 20, total_items: '21', total_pages: '2' },
    });
  });

  it('accepts an empty canonical page and a server-clamped oversized page', () => {
    expect(
      normalizeIssuePageResponse(
        page([], { page: '1', page_size: 20, total_items: '0', total_pages: '1' }),
        'current',
        '1',
        20,
      ),
    ).toMatchObject({ data: [], pagination: { page: '1', total_pages: '1' } });

    const closed = issue('C', { state: 'closed', closed_at: 1_800_000_002 });
    expect(
      normalizeIssuePageResponse(
        page([closed], { page: '2', page_size: 20, total_items: '21', total_pages: '2' }),
        'closed',
        '2147483647',
        20,
      ),
    ).toMatchObject({ pagination: { page: '2', total_items: '21', total_pages: '2' } });
  });

  it.each([
    ['an unknown envelope field', { ...page([issue()]), extra: true }],
    ['a non-null cursor', page([issue()], undefined, 'cursor')],
    ['a mismatched issue state', page([issue('C', { state: 'closed', closed_at: 1_800_000_002 })])],
    [
      'duplicate issue identities',
      page([issue(), issue()], { page: '1', page_size: 20, total_items: '2', total_pages: '1' }),
    ],
    [
      'a row count outside the declared window',
      page([], { page: '1', page_size: 20, total_items: '1', total_pages: '1' }),
    ],
    [
      'metadata for a different requested size',
      page([issue()], { page: '1', page_size: 50, total_items: '1', total_pages: '1' }),
    ],
    [
      'an unknown pagination field',
      {
        ...page([issue()]),
        pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1', extra: 1 },
      },
    ],
  ])('rejects %s', (_label, value) => {
    expect(() => normalizeIssuePageResponse(value, 'current', '1', 20)).toThrow();
  });
});

describe('user issue page requests', () => {
  it('sends only page parameters and forwards AbortSignal', async () => {
    const controller = new AbortController();
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/api/issues?state=closed&page=2&page_size=50',
        body: page([issue('C', { state: 'closed', closed_at: 1_800_000_002 })], {
          page: '2',
          page_size: 50,
          total_items: '51',
          total_pages: '2',
        }),
      },
    ]);

    const result = await getIssuePage('closed', '2', 50, controller.signal);

    expect(result.pagination.page).toBe('2');
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/issues?state=closed&page=2&page_size=50',
      expect.objectContaining({ signal: controller.signal }),
    );
    expect(fetchMock.mock.calls[0]?.[0]).not.toContain('cursor=');
    expect(fetchMock.mock.calls[0]?.[0]).not.toContain('limit=');
  });

  it.each([
    ['01', 20],
    ['2147483648', 20],
    ['1', 30],
  ])(
    'rejects invalid request parameters before the network call: %s/%s',
    async (pageNumber, size) => {
      const fetchMock = installJsonFetchFixtures([]);
      await expect(getIssuePage('current', pageNumber, size as never)).rejects.toMatchObject({
        code: 'invalid_request',
        status: 400,
      });
      expect(fetchMock).not.toHaveBeenCalled();
    },
  );

  it('keeps the account and page dimensions in the query key', () => {
    expect(issuePageKeys.page('7', 'closed', '3', 100)).toEqual([
      'user',
      'operations',
      'issues-page',
      '7',
      'closed',
      '3',
      100,
    ]);
  });
});
