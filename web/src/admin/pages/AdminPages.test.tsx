import { act, screen, waitFor } from '@testing-library/react';
import { useLocation } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import { ActivitiesPage } from './ActivitiesPage';
import { EndpointsPage } from './EndpointsPage';
import { UsersPage } from './UsersPage';
import { adminPageKeys } from '../features/operations/adminPages';
import { renderWithProviders } from '../../../test/unit/support';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
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

const adminUser = {
  id: '7',
  discord_id: null,
  username: 'alice',
  avatar_url: null,
  guild_nick: null,
  guild_avatar_url: null,
  is_admin: false,
  is_banned: true,
  banned_reason: 'fixture moderation',
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

const endpointUser = {
  user_id: '9',
  endpoint_count: '2',
  key_count: '1',
  enabled_count: '1',
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

function installFetch(handler: (url: URL, method: string, init?: RequestInit) => unknown): URL[] {
  const requests: URL[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const rawURL = input instanceof Request ? input.url : String(input);
      const url = new URL(rawURL, window.location.origin);
      requests.push(url);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      const response = await handler(url, method, init);
      return response instanceof Response ? response : jsonResponse(response);
    }),
  );
  return requests;
}

function LocationProbe() {
  return <output data-testid="location">{useLocation().search}</output>;
}

function ancillaryResponse(url: URL): unknown {
  if (url.pathname === '/admin/api/session') return { admin: { username: 'fixture-admin' } };
  if (url.pathname === '/admin/api/time-zones')
    return { version: 'go1.26.6-zoneinfo', zones: ['UTC'] };
  if (url.pathname === '/admin/api/activities/config')
    return {
      revision: '1',
      master_enabled: true,
      loan_enabled: false, loan_tiers: ['10000', '100000', '1000000'], loan_a: '0.9', loan_b: '1.3',
      welfare: { enabled: false, threshold: '1', cap: '2' },
      thursday: { enabled: false },
    };
  if (url.pathname === '/admin/api/activities/thursday') return { period: null };
  throw new Error(`Unexpected fixture request: ${url.pathname}${url.search}`);
}

describe('administrator paged operation pages', () => {
  it.each([
    ['users', '/admin/api/users', UsersPage],
    ['endpoints', '/admin/api/overview/endpoints', EndpointsPage],
    ['activities', '/admin/api/pools', ActivitiesPage],
  ] as const)(
    'keeps page-size controls usable for an empty %s list',
    async (route, path, Component) => {
      const requests = installFetch((url) => {
        if (url.pathname === path)
          return page([], '1', Number(url.searchParams.get('page_size')), 0);
        return ancillaryResponse(url);
      });
      const view = await renderWithProviders(<Component />, {
        station: 'admin',
        role: 'admin',
        route: `/${route}?page=99&page_size=20`,
      });
      const size = await screen.findByLabelText('Items per page');
      expect(size).toHaveValue('20');
      await view.user.selectOptions(size, '100');
      await waitFor(() =>
        expect(
          requests.some(
            (url) =>
              url.pathname === path &&
              url.searchParams.get('page') === '1' &&
              url.searchParams.get('page_size') === '100',
          ),
        ).toBe(true),
      );
    },
  );

  it('closes a previously loaded user editor when the list loses authority', async () => {
    let denied = false;
    installFetch((url) => {
      if (url.pathname === '/admin/api/users' || url.pathname === '/admin/api/session') {
        if (denied)
          return jsonResponse({ error: { code: 'forbidden', message: 'Access revoked' } }, 403);
        return url.pathname.endsWith('session')
          ? ancillaryResponse(url)
          : page([adminUser], '1', 20);
      }
      if (url.pathname === '/admin/api/users/7') return adminUser;
      return ancillaryResponse(url);
    });
    const view = await renderWithProviders(<UsersPage />, {
      station: 'admin',
      role: 'admin',
      route: '/users?user=7',
    });
    await screen.findByText(/Count limits/);
    denied = true;
    await act(async () => {
      await view.queryClient.refetchQueries({
        queryKey: adminPageKeys.users('fixture-admin', '', '', '1', 20),
        exact: true,
      });
    });
    await waitFor(() => expect(screen.queryByText(/Count limits/)).toBeNull());
    expect(screen.queryByRole('button', { name: 'Manage' })).toBeNull();
  });

  it('restores a users page and sends all URL filters to the page API', async () => {
    const requests = installFetch((url, method) => {
      if (method === 'GET' && url.pathname === '/admin/api/session') {
        return { admin: { username: 'fixture-admin' } };
      }
      if (method === 'GET' && url.pathname === '/admin/api/users') {
        expect(url.searchParams.get('is_banned')).toBe('true');
        expect(url.searchParams.get('q')).toBe('alice');
        expect(['999', '2']).toContain(url.searchParams.get('page'));
        expect(url.searchParams.get('page_size')).toBe('50');
        expect(url.searchParams.has('cursor')).toBe(false);
        expect(url.searchParams.has('limit')).toBe(false);
        return page([adminUser], '2', 50, 51);
      }
      if (method === 'GET' && url.pathname === '/admin/api/users/7') return adminUser;
      throw new Error(`Unexpected fixture request: ${method} ${url.pathname}${url.search}`);
    });

    const view = await renderWithProviders(
      <>
        <UsersPage />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/users?page=999&page_size=50&q=alice&is_banned=true',
      },
    );
    await screen.findByText('alice');
    expect(requests.some((url) => url.pathname === '/admin/api/users')).toBe(true);
    await view.user.click(screen.getByRole('button', { name: 'Manage' }));
    await screen.findByText(/Count limits/);
    await view.user.click(screen.getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByText(/Count limits/)).toBeNull());
    const restored = new URLSearchParams(screen.getByTestId('location').textContent ?? '');
    expect(restored.get('page')).toBe('2');
    expect(restored.get('page_size')).toBe('50');
    expect(restored.get('q')).toBe('alice');
    expect(restored.has('user')).toBe(false);
  });

  it('loads expanded endpoint users from the exact base URL page endpoint', async () => {
    const baseURL = 'https://api.example.test/v1?region=eu%2Fwest';
    installFetch((url, method) => {
      if (method === 'GET' && url.pathname === '/admin/api/session') {
        return { admin: { username: 'fixture-admin' } };
      }
      if (method === 'GET' && url.pathname === '/admin/api/overview/endpoints') {
        return page(
          [
            {
              base_url: baseURL,
              user_count: '1',
              endpoint_count: '2',
              key_count: '1',
              users: [endpointUser],
            },
          ],
          '1',
          20,
          1,
        );
      }
      if (method === 'GET' && url.pathname === '/admin/api/overview/endpoints/users') {
        expect(url.searchParams.get('base_url')).toBe(baseURL);
        expect(url.searchParams.get('page')).toBe('1');
        expect(url.searchParams.get('page_size')).toBe('20');
        expect(url.searchParams.has('cursor')).toBe(false);
        expect(url.searchParams.has('limit')).toBe(false);
        return page([endpointUser], '1', 20, 1);
      }
      throw new Error(`Unexpected fixture request: ${method} ${url.pathname}${url.search}`);
    });

    const view = await renderWithProviders(<EndpointsPage />, {
      station: 'admin',
      role: 'admin',
      route: '/endpoints?q=api&page=1&page_size=20',
    });
    await screen.findByText(baseURL);
    await view.user.click(screen.getByRole('button', { name: 'Show users' }));
    expect(await screen.findByText('9')).toBeVisible();
  });

  it('keeps pool filters and page size in URL page mode', async () => {
    const requests = installFetch((url, method) => {
      if (method === 'GET' && url.pathname === '/admin/api/session') {
        return { admin: { username: 'fixture-admin' } };
      }
      if (method === 'GET' && url.pathname === '/admin/api/time-zones') {
        return { version: 'go1.26.6-zoneinfo', zones: ['UTC'] };
      }
      if (method === 'GET' && url.pathname === '/admin/api/activities/config') {
        return {
          revision: '1',
          master_enabled: true,
          loan_enabled: false, loan_tiers: ['10000', '100000', '1000000'], loan_a: '0.9', loan_b: '1.3',
          welfare: { enabled: false, threshold: '1', cap: '2' },
          thursday: { enabled: false },
        };
      }
      if (method === 'GET' && url.pathname === '/admin/api/activities/thursday') {
        return { period: null };
      }
      if (method === 'GET' && url.pathname === '/admin/api/pools') {
        const size = Number(url.searchParams.get('page_size'));
        expect(url.searchParams.get('pool_type')).toBe('welfare');
        expect(url.searchParams.get('state')).toBe('open');
        expect(url.searchParams.get('page')).toBe(size === 100 ? '1' : '2');
        expect(url.searchParams.has('cursor')).toBe(false);
        expect(url.searchParams.has('limit')).toBe(false);
        return size === 100 ? page([pool], '1', 100, 1) : page([pool], '2', 10, 11);
      }
      throw new Error(`Unexpected fixture request: ${method} ${url.pathname}${url.search}`);
    });

    const view = await renderWithProviders(<ActivitiesPage />, {
      station: 'admin',
      role: 'admin',
      route: '/activities?pool_type=welfare&state=open&page=2&page_size=10',
    });
    await screen.findByText(/Welfare \/ Open/);
    const size = screen.getByLabelText('Items per page');
    await view.user.selectOptions(size, '100');
    await waitFor(() =>
      expect(
        requests.some(
          (url) =>
            url.pathname === '/admin/api/pools' && url.searchParams.get('page_size') === '100',
        ),
      ).toBe(true),
    );
  });
});
