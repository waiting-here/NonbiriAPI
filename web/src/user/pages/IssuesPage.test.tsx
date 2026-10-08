import { act, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useLocation } from 'react-router';
import { installJsonFetchFixtures, renderWithProviders } from '../../../test/unit/support';
import { normalizeUserAuthority, operationsKeys } from '../features/operations/data';
import { IssuesPage } from './IssuesPage';

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.removeItem('nonbiri:user:issues-page-size:v1');
});

function session(id = '7') {
  return {
    user: {
      id,
      username: 'member',
      avatar: null,
      avatar_url: null,
      guild_nick: null,
      guild_avatar_url: null,
      lang: 'en',
      is_banned: false,
      banned_until: null,
      charity_suspended_until: null,
      endpoint_limit: null,
      effective_endpoint_limit: '5',
      rpm_limit: null,
      effective_rpm_limit: '60',
      concurrency_limit: null,
      effective_concurrency_limit: '2',
      balance: '0',
      game_balance: '0',
      donation_credit: '0',
      effective_level: 1,
      level_display_name: 'Lv1',
      game_profile_public: false,
      charity_profile_public: false,
      automatic_restrictions: [],
      created_at: 1_800_000_000,
      updated_at: 1_800_000_000,
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
    },
  };
}

function issue(suffix: string, overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: `iss_${suffix.padEnd(22, 'A').slice(0, 22)}`,
    state: 'current',
    source: 'model_discovery',
    resource_kind: 'endpoint_key',
    summary_code: 'discovery_failed',
    safe_detail: `Issue ${suffix}`,
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
  projection_incomplete = false,
) {
  return { data, next_cursor: null, projection_incomplete, pagination };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((settle) => {
    resolve = settle;
  });
  return { promise, resolve };
}

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location-search">{location.search}</output>;
}

describe('user issues page', () => {
  it.each(['en', 'zh'] as const)(
    'translates known safe reasons and keeps unknown text in %s',
    async (locale) => {
      const details = [
        'auth',
        'rate_limit',
        'timeout',
        'protocol',
        'transport',
        'interrupted',
        'new upstream reason',
      ];
      installJsonFetchFixtures([
        { method: 'GET', path: '/api/session', body: session() },
        {
          method: 'GET',
          path: '/api/issues?state=current&page=1&page_size=20',
          body: page(details.map((safe_detail, index) => issue(String(index), { safe_detail }))),
        },
      ]);
      await renderWithProviders(<IssuesPage />, { station: 'user', role: 'user', locale });
      const labels =
        locale === 'zh'
          ? [
              '服务密钥验证失败',
              '服务请求过于频繁',
              '服务响应超时',
              '服务返回了无法识别的回复',
              '无法连接到服务',
              '服务请求已中断',
            ]
          : [
              'The service rejected the key.',
              'The service received too many requests.',
              'The service took too long to respond.',
              'The service returned an unreadable response.',
              'The service could not be reached.',
              'The service request was interrupted.',
            ];
      for (const label of labels) expect(await screen.findByText(label)).toBeVisible();
      expect(screen.getByText('new upstream reason')).toBeVisible();
    },
  );

  it('shows the session authorization failure without requesting private issues', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: {}, status: 401 },
    ]);

    await renderWithProviders(<IssuesPage />, { station: 'user', role: 'user' });

    expect(await screen.findByRole('heading', { name: 'Sign-in required' })).toBeVisible();
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('renders server paging, preserves the selected size, and resets to page one', async () => {
    const firstPage = Array.from({ length: 20 }, (_, index) => issue(String(index + 1)));
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session() },
      {
        method: 'GET',
        path: '/api/issues?state=current&page=1&page_size=20',
        body: page(firstPage, { page: '1', page_size: 20, total_items: '21', total_pages: '2' }),
      },
      {
        method: 'GET',
        path: '/api/issues?state=current&page=2&page_size=20',
        body: page([issue('21')], {
          page: '2',
          page_size: 20,
          total_items: '21',
          total_pages: '2',
        }),
      },
      {
        method: 'GET',
        path: '/api/issues?state=current&page=1&page_size=50',
        body: page([...firstPage, issue('21')], {
          page: '1',
          page_size: 50,
          total_items: '21',
          total_pages: '1',
        }),
      },
    ]);
    const rendered = await renderWithProviders(<IssuesPage />, {
      station: 'user',
      role: 'user',
    });

    expect(await screen.findByText('Issue 1')).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'Items per page' })).toHaveValue('20');
    await rendered.user.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByText('Issue 21')).toBeVisible();

    await rendered.user.selectOptions(
      screen.getByRole('combobox', { name: 'Items per page' }),
      '50',
    );
    await waitFor(() =>
      expect(screen.getByRole('combobox', { name: 'Items per page' })).toHaveValue('50'),
    );
    expect(screen.getByText('Issue 1')).toBeVisible();
    expect(window.localStorage.getItem('nonbiri:user:issues-page-size:v1')).toBe('50');
    expect(fetchMock.mock.calls.map(([input]) => String(input))).not.toContain(
      '/api/issues?cursor=',
    );
    expect(
      fetchMock.mock.calls
        .map(([input]) => String(input))
        .some((input) => input.includes('limit=')),
    ).toBe(false);
  });

  it('keeps pagination metadata and the projection notice for an empty result', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session() },
      {
        method: 'GET',
        path: '/api/issues?state=current&page=1&page_size=20',
        body: page([], { page: '1', page_size: 20, total_items: '0', total_pages: '1' }, true),
      },
    ]);

    await renderWithProviders(<IssuesPage />, { station: 'user', role: 'user' });

    expect(await screen.findByRole('heading', { name: 'No issues returned' })).toBeVisible();
    expect(screen.getByText('Updating the list…')).toBeVisible();
    expect(screen.queryByRole('navigation', { name: 'Pagination' })).not.toBeInTheDocument();
    expect(screen.queryByRole('combobox', { name: 'Items per page' })).not.toBeInTheDocument();
  });

  it('switches between current and closed issue collections and keeps removed links unavailable', async () => {
    const closed = issue('closed', {
      state: 'closed',
      closed_at: 1_800_000_002,
    });
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session() },
      {
        method: 'GET',
        path: '/api/issues?state=current&page=1&page_size=20',
        body: page([issue('current')]),
      },
      {
        method: 'GET',
        path: '/api/issues?state=closed&page=1&page_size=20',
        body: page([closed]),
      },
    ]);
    const rendered = await renderWithProviders(
      <>
        <IssuesPage />
        <LocationProbe />
      </>,
      { station: 'user', role: 'user' },
    );

    expect(await screen.findByText('Issue current')).toBeVisible();
    await rendered.user.click(screen.getByRole('tab', { name: 'Closed history' }));
    expect(await screen.findByText('Issue closed')).toBeVisible();
    const search = new URLSearchParams(screen.getByTestId('location-search').textContent ?? '');
    expect(search.get('state')).toBe('closed');
    expect(search.get('page')).toBe('1');
    expect(search.get('page_size')).toBe('20');
    await waitFor(() => expect(screen.queryByText('Issue current')).not.toBeInTheDocument());
    expect(
      screen.getByText('The related service or key was deleted. This issue is closed.'),
    ).toBeVisible();
    expect(screen.queryByRole('link', { name: 'Open current resource' })).not.toBeInTheDocument();
  });

  it('does not label a retained closed page as an unresolved issue count while switching collections', async () => {
    const current = deferred<Response>();
    const requests: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input) => {
        const path = String(input);
        requests.push(path);
        if (path === '/api/session') return jsonResponse(session());
        if (path === '/api/issues?state=closed&page=1&page_size=20')
          return jsonResponse(page([issue('old', { state: 'closed', closed_at: 1800000002 })]));
        if (path === '/api/issues?state=current&page=1&page_size=20') return current.promise;
        throw new Error(`Unexpected request ${path}`);
      }),
    );
    const view = await renderWithProviders(<IssuesPage />, {
      station: 'user',
      role: 'user',
      route: '/issues?state=closed',
    });
    await screen.findByText('Issue old');
    await view.user.click(screen.getByRole('tab', { name: 'Current' }));
    expect(view.container.querySelector('.records-tabs .nb-badge')).toBeNull();
    current.resolve(jsonResponse(page([issue('new')])));
    await screen.findByText('Issue new');
    expect(view.container.querySelector('.records-tabs .nb-badge')).toHaveTextContent('1');
    expect(requests).toEqual([
      '/api/session',
      '/api/issues?state=closed&page=1&page_size=20',
      '/api/issues?state=current&page=1&page_size=20',
    ]);
  });

  it('restores the selected state, page, and size from the URL', async () => {
    const closed = issue('closed-url', {
      state: 'closed',
      closed_at: 1_800_000_002,
    });
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session() },
      {
        method: 'GET',
        path: '/api/issues?state=closed&page=2&page_size=50',
        body: page([closed], { page: '2', page_size: 50, total_items: '51', total_pages: '2' }),
      },
    ]);

    await renderWithProviders(
      <>
        <IssuesPage />
        <LocationProbe />
      </>,
      {
        station: 'user',
        role: 'user',
        route: '/issues?state=closed&page=2&page_size=50',
      },
    );

    expect(await screen.findByText('Issue closed-url')).toBeVisible();
    expect(screen.getByRole('tab', { name: 'Closed history' })).toHaveAttribute(
      'aria-selected',
      'true',
    );
    expect(screen.getByRole('combobox', { name: 'Items per page' })).toHaveValue('50');
    expect(screen.getByTestId('location-search')).toHaveTextContent(
      '?state=closed&page=2&page_size=50',
    );
  });

  it('renders an issue authorization failure as an error instead of a permanent loading state', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session() },
      {
        method: 'GET',
        path: '/api/issues?state=current&page=1&page_size=20',
        body: {},
        status: 401,
      },
    ]);

    await renderWithProviders(<IssuesPage />, { station: 'user', role: 'user' });

    expect(await screen.findByRole('heading', { name: 'Sign-in required' })).toBeVisible();
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
  });

  it('keeps the old page visible while a new page is busy and accepts only the newer response', async () => {
    const nextPage = deferred<Response>();
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (target.pathname === '/api/session') return Promise.resolve(jsonResponse(session()));
      if (target.pathname === '/api/issues' && target.searchParams.get('page') === '1') {
        return Promise.resolve(
          jsonResponse(
            page(
              [
                issue('first'),
                ...Array.from({ length: 19 }, (_, index) => issue(`first-${index}`)),
              ],
              {
                page: '1',
                page_size: 20,
                total_items: '21',
                total_pages: '2',
              },
            ),
          ),
        );
      }
      if (target.pathname === '/api/issues' && target.searchParams.get('page') === '2') {
        return nextPage.promise;
      }
      throw new Error(`Unexpected request: ${target.pathname}${target.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<IssuesPage />, { station: 'user', role: 'user' });

    expect(await screen.findByText('Issue first')).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Next' }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([input]) => String(input).includes('page=2'))).toBe(true),
    );
    expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
    expect(screen.getByRole('combobox', { name: 'Items per page' })).toBeDisabled();
    expect(document.querySelector('[aria-busy="true"]')).toBeInTheDocument();

    await act(async () => {
      nextPage.resolve(
        jsonResponse(
          page([issue('second')], {
            page: '2',
            page_size: 20,
            total_items: '21',
            total_pages: '2',
          }),
        ),
      );
      await nextPage.promise;
    });
    expect(await screen.findByText('Issue second')).toBeVisible();
    expect(screen.queryByText('Issue first')).not.toBeInTheDocument();
  });

  it('drops an old account response after the account boundary changes', async () => {
    const oldPage = deferred<Response>();
    let currentAccount = 'A';
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (target.pathname === '/api/session') return Promise.resolve(jsonResponse(session('7')));
      if (target.pathname === '/api/issues') {
        return currentAccount === 'A'
          ? oldPage.promise
          : Promise.resolve(jsonResponse(page([issue('account-b')])));
      }
      throw new Error(`Unexpected request: ${target.pathname}${target.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<IssuesPage />, { station: 'user', role: 'user' });

    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/api/issues'))).toBe(
        true,
      ),
    );
    currentAccount = 'B';
    rendered.queryClient.setQueryData(operationsKeys.session, normalizeUserAuthority(session('8')));
    expect(await screen.findByText('Issue account-b')).toBeVisible();

    await act(async () => {
      oldPage.resolve(jsonResponse(page([issue('account-a')])));
      await oldPage.promise;
    });
    await waitFor(() => expect(screen.queryByText('Issue account-a')).not.toBeInTheDocument());
    expect(screen.getByText('Issue account-b')).toBeVisible();
  });
});
