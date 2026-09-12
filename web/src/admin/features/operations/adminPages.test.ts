import { describe, expect, it, vi } from 'vitest';
import {
  getAdminEndpointUsersPage,
  getAdminEndpointsPage,
  getAdminPoolsPage,
  getAdminUserDetail,
  getAdminUsersPage,
  normalizeAdminPageResponse,
} from './adminPages';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

function page<T>(data: T[], pageNumber: string, pageSize: number, totalItems = data.length) {
  return {
    data,
    next_cursor: null,
    pagination: {
      page: pageNumber,
      page_size: pageSize,
      total_items: String(totalItems),
      total_pages: String(Math.max(1, Math.ceil(totalItems / pageSize))),
    },
  };
}

const endpointUser = {
  user_id: '7',
  endpoint_count: '2',
  key_count: '3',
  enabled_count: '1',
};

const adminUser = {
  id: '7',
  discord_id: null,
  username: 'fixture-user',
  avatar_url: null,
  guild_nick: null,
  guild_avatar_url: null,
  is_admin: false,
  is_banned: false,
  banned_reason: '',
  banned_until: null,
  charity_suspended_until: null,
  endpoint_limit: null,
  effective_endpoint_limit: '4',
  rpm_limit: null,
  effective_rpm_limit: '60',
  concurrency_limit: null,
  effective_concurrency_limit: '5',
  lang: 'en',
  balance: '0',
  game_balance: '0',
  donation_credit: '0',
  level: { manual: null, automatic: 1, effective: 1, display_name: 'Lv1' },
  game_profile_public: false,
  revision: '1',
  usage: {
    total_requests: '0',
    total_uncached_input_tokens: '0',
    total_cache_write_input_tokens: '0',
    total_cache_read_input_tokens: '0',
    total_output_tokens: '0',
    total_prompt_tokens: '0',
    total_completion_tokens: '0',
    total_unknown_usage_requests: '0',
  },
  created_at: 1_700_000_000,
  updated_at: 1_700_000_001,
};

const pool = {
  id: `pol_${'A'.repeat(22)}`,
  pool_type: 'welfare',
  period_id: null,
  state: 'open',
  revision: '1',
  balance: '0',
  created_at: 1_700_000_000,
  closed_at: null,
};

describe('administrator page wire', () => {
  it('rejects invalid windows, filters and identities before making any request', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    const invalid = [
      () => getAdminUsersPage('', '', '0', 20),
      () => getAdminUsersPage('', '', '2147483648', 20),
      () => getAdminUsersPage('', '', '1', 30 as never),
      () => getAdminUsersPage('all' as never, '', '1', 20),
      () => getAdminUsersPage('', '界'.repeat(171), '1', 20),
      () => getAdminEndpointsPage('bad\nfilter', '1', 20),
      () => getAdminEndpointsPage('\ud800', '1', 20),
      () => getAdminEndpointUsersPage('', '1', 20),
      () => getAdminEndpointUsersPage('x'.repeat(4097), '1', 20),
      () => getAdminEndpointUsersPage('https://example.test/\udfff', '1', 20),
      () => getAdminPoolsPage('unknown', '', '1', 20),
      () => getAdminPoolsPage('', 'released', '1', 20),
      () => getAdminUserDetail('9223372036854775808'),
    ];
    for (const request of invalid) {
      await expect(request()).rejects.toMatchObject({ code: 'invalid_request', status: 400 });
    }
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('rejects mismatched detail identity, repeated rows and impossible counts', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse(adminUser)),
    );
    await expect(getAdminUserDetail('8')).rejects.toThrow(/identity/);
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse(page([adminUser, adminUser], '1', 20))),
    );
    await expect(getAdminUsersPage('', '', '1', 20)).rejects.toThrow(/duplicate/);
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse(page([{ ...endpointUser, enabled_count: '3' }], '1', 20))),
    );
    await expect(getAdminEndpointUsersPage('https://api.example.test', '1', 20)).rejects.toThrow(
      /counts/,
    );
  });

  it('keeps string pagination metadata and validates the requested window', () => {
    const result = normalizeAdminPageResponse(
      page([endpointUser], '1', 20, 1),
      'fixture page',
      (value) => value,
      '1',
      20,
    );
    expect(result.pagination).toEqual({
      page: '1',
      page_size: 20,
      total_items: '1',
      total_pages: '1',
    });
    expect(() =>
      normalizeAdminPageResponse(
        { ...page([endpointUser], '1', 20, 1), next_cursor: 'legacy' },
        'fixture page',
        (value) => value,
        '1',
        20,
      ),
    ).toThrow(/cursor/i);
    expect(() =>
      normalizeAdminPageResponse(page([], '1', 20, 1), 'fixture page', (value) => value, '1', 20),
    ).toThrow(/window/i);
    expect(() =>
      normalizeAdminPageResponse(
        page([endpointUser], '1', 20, 1),
        'fixture page',
        (value) => value,
        '0',
        20,
      ),
    ).toThrow(/requested page/i);
  });

  it('sends the users filters with page parameters and forwards AbortSignal', async () => {
    const controller = new AbortController();
    const fetchMock = vi.fn(async (input: string | URL, init?: RequestInit) => {
      const url = new URL(String(input), window.location.origin);
      expect(url.pathname).toBe('/admin/api/users');
      expect(url.searchParams.get('is_banned')).toBe('true');
      expect(url.searchParams.get('q')).toBe('fixture user');
      expect(url.searchParams.get('page')).toBe('2');
      expect(url.searchParams.get('page_size')).toBe('50');
      expect(url.searchParams.has('cursor')).toBe(false);
      expect(url.searchParams.has('limit')).toBe(false);
      expect(init?.signal).toBe(controller.signal);
      return jsonResponse(page([adminUser], '1', 50, 1));
    });
    vi.stubGlobal('fetch', fetchMock);

    const result = await getAdminUsersPage('true', 'fixture user', '2', 50, controller.signal);
    expect(result.data[0].username).toBe('fixture-user');
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it('preserves exact endpoint URLs and rejects an oversized preview', async () => {
    const baseURL = 'https://api.example.test/v1?region=eu%2Fwest';
    const overview = {
      base_url: baseURL,
      user_count: '1',
      endpoint_count: '2',
      key_count: '3',
      users: [endpointUser],
    };
    const fetchMock = vi.fn(async (input: string | URL) => {
      const url = new URL(String(input), window.location.origin);
      expect(url.pathname).toBe('/admin/api/overview/endpoints');
      expect(url.searchParams.get('q')).toBe('example');
      expect(url.searchParams.get('page')).toBe('1');
      expect(url.searchParams.get('page_size')).toBe('20');
      return jsonResponse(page([overview], '1', 20, 1));
    });
    vi.stubGlobal('fetch', fetchMock);
    const result = await getAdminEndpointsPage('example', '1', 20);
    expect(result.data[0].base_url).toBe(baseURL);

    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string | URL) => {
        const url = new URL(String(input), window.location.origin);
        expect(url.pathname).toBe('/admin/api/overview/endpoints');
        return jsonResponse(
          page(
            [
              {
                ...overview,
                users: [endpointUser, endpointUser, endpointUser, endpointUser],
              },
            ],
            '1',
            20,
            1,
          ),
        );
      }),
    );
    await expect(getAdminEndpointsPage('', '1', 20)).rejects.toThrow(/preview/i);

    const nestedFetch = vi.fn(async (input: string | URL) => {
      const url = new URL(String(input), window.location.origin);
      expect(url.pathname).toBe('/admin/api/overview/endpoints/users');
      expect(url.searchParams.get('base_url')).toBe(baseURL);
      expect(url.searchParams.get('page')).toBe('3');
      expect(url.searchParams.get('page_size')).toBe('10');
      expect(url.searchParams.has('cursor')).toBe(false);
      expect(url.searchParams.has('limit')).toBe(false);
      return jsonResponse(page([endpointUser], '1', 10, 1));
    });
    vi.stubGlobal('fetch', nestedFetch);
    const nested = await getAdminEndpointUsersPage(baseURL, '3', 10);
    expect(nested.data[0].user_id).toBe('7');
  });

  it('sends pool filters in page mode without cursor parameters', async () => {
    const fetchMock = vi.fn(async (input: string | URL) => {
      const url = new URL(String(input), window.location.origin);
      expect(url.pathname).toBe('/admin/api/pools');
      expect(url.searchParams.get('pool_type')).toBe('welfare');
      expect(url.searchParams.get('state')).toBe('open');
      expect(url.searchParams.get('page')).toBe('4');
      expect(url.searchParams.get('page_size')).toBe('100');
      expect(url.searchParams.has('cursor')).toBe(false);
      expect(url.searchParams.has('limit')).toBe(false);
      return jsonResponse(page([pool], '4', 100, 301));
    });
    vi.stubGlobal('fetch', fetchMock);

    const result = await getAdminPoolsPage('welfare', 'open', '4', 100);
    expect(result.data[0].id).toBe(pool.id);
  });
});
