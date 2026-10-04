import { act, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useLocation, useNavigate } from 'react-router';
import { installJsonFetchFixtures, renderWithProviders } from '../../../test/unit/support';
import { adminKeys } from '../data';
import { AlertsPage, validAlertReturnTo } from './AlertsPage';
import deletionSnapshots from '../../../test/fixtures/account-deletion-alerts.json';

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.removeItem('nonbiri:admin:alerts-page-size:v1');
});

function session(username = 'root') {
  return { admin: { username } };
}

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
) {
  return { data, next_cursor: null, pagination };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location-search">{location.search}</output>;
}

function BackProbe() {
  const navigate = useNavigate();
  return (
    <button type="button" onClick={() => navigate(-1)}>
      Back
    </button>
  );
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((settle) => {
    resolve = settle;
  });
  return { promise, resolve };
}

describe('administrator alerts page', () => {
  it('rejects off-site and ambiguous return destinations', () => {
    expect(validAlertReturnTo('https://evil.invalid')).toBeNull();
    expect(validAlertReturnTo('//evil.invalid')).toBeNull();
    expect(validAlertReturnTo('/users\\evil')).toBeNull();
    expect(validAlertReturnTo('/users?deleted=9&page=4&page_size=50')).toBe(
      '/users?deleted=9&page=4&page_size=50',
    );
  });

  it('opens an exact donation alert and links logs by endpoint key, then returns to the prior page', async () => {
    const donationAlert = alert('7', false, {
      kind: 'donation_failure_disabled',
      ref: 'donation-key:71:generation:2:fold:3',
    });
    installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/alerts?resolved=false&page=1&page_size=20',
        body: page([donationAlert]),
      },
      {
        method: 'GET',
        path: '/admin/api/alerts/7',
        body: {
          alert: donationAlert,
          context_version: 1,
          occurred_facts: [
            { key: 'failure_streak', value: '3' },
            { key: 'failure_threshold', value: '3' },
          ],
          targets: [
            { kind: 'donation', id: '2', available: true, status: 'approved' },
            { kind: 'donation_key', id: '71', available: true, status: 'failure_disabled' },
            { kind: 'endpoint_key', id: '83', available: true, status: 'enabled' },
          ],
          current_state: [],
          related_logs: {
            endpoint_key_id: '83',
            from: 1799999100,
            to: 1800000300,
            available: true,
          },
          resolution_kind: '',
        },
      },
    ]);
    await renderWithProviders(<AlertsPage />, {
      station: 'admin',
      role: 'admin',
      locale: 'en',
      route:
        '/alerts?resolved=false&page=1&page_size=20&alert_id=7&return_to=%2Fusers%3Fdeleted%3D9%26page%3D4%26page_size%3D50',
    });
    expect(await screen.findByRole('heading', { name: 'Alert details #7' })).toBeVisible();
    expect(screen.getByRole('link', { name: 'Return to previous page' })).toHaveAttribute(
      'href',
      '/users?deleted=9&page=4&page_size=50',
    );
    expect(
      await screen.findByRole('link', {
        name: 'From 15 minutes before to 5 minutes after this alert',
      }),
    ).toHaveAttribute('href', '/logs?endpoint_key_id=83&from=1799999100&to=1800000300');
    expect(screen.queryByRole('link', { name: /endpoint key.*71/i })).toBeNull();
  });

  it('confirms the administrator session before requesting private alerts', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: {}, status: 401 },
    ]);

    await renderWithProviders(<AlertsPage />, { station: 'admin', role: 'admin' });

    expect(await screen.findByRole('heading', { name: 'Sign-in required' })).toBeVisible();
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('turns an alert authorization failure into a closed error state', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/alerts?resolved=false&page=1&page_size=20',
        status: 403,
        body: { error: { code: 'forbidden', message: 'denied' } },
      },
    ]);

    await renderWithProviders(<AlertsPage />, {
      station: 'admin',
      role: 'admin',
      route: '/alerts?resolved=false&page=1&page_size=20',
    });

    expect(
      await screen.findByText('This action is not allowed for the current account.'),
    ).toBeVisible();
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('restores filter, page, and size from the URL and renders canonical metadata', async () => {
    const resolved = alert('7', true, { message: '<plain alert>' });
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/alerts?resolved=true&page=2&page_size=50',
        body: page([resolved], { page: '2', page_size: 50, total_items: '51', total_pages: '2' }),
      },
    ]);

    await renderWithProviders(
      <>
        <AlertsPage />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/alerts?resolved=true&page=2&page_size=50',
      },
    );

    expect(await screen.findByText('<plain alert>')).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'Resolution status' })).toHaveValue('true');
    expect(screen.getByRole('combobox', { name: 'Items per page' })).toHaveValue('50');
    expect(screen.getByText('51 items')).toBeVisible();
    expect(screen.getByTestId('location-search')).toHaveTextContent(
      '?resolved=true&page=2&page_size=50',
    );
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual([
      '/admin/api/session',
      '/admin/api/alerts?resolved=true&page=2&page_size=50',
    ]);
  });

  it('writes a filter change as one URL update with page one and the current size', async () => {
    const openRows = [alert('1')];
    const closedRows = [alert('2', true)];
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/alerts?resolved=false&page=3&page_size=50',
        body: page(openRows, { page: '3', page_size: 50, total_items: '101', total_pages: '3' }),
      },
      {
        method: 'GET',
        path: '/admin/api/alerts?resolved=true&page=1&page_size=50',
        body: page(closedRows, { page: '1', page_size: 50, total_items: '1', total_pages: '1' }),
      },
    ]);

    const rendered = await renderWithProviders(
      <>
        <AlertsPage />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/alerts?resolved=false&page=3&page_size=50',
      },
    );

    expect(await screen.findByText('Alert 1')).toBeVisible();
    await rendered.user.selectOptions(
      screen.getByRole('combobox', { name: 'Resolution status' }),
      'true',
    );
    expect(await screen.findByText('Alert 2')).toBeVisible();
    const search = new URLSearchParams(screen.getByTestId('location-search').textContent ?? '');
    expect(search.get('resolved')).toBe('true');
    expect(search.get('page')).toBe('1');
    expect(search.get('page_size')).toBe('50');
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      '/admin/api/alerts?resolved=true&page=1&page_size=50',
    );
    expect(screen.queryByText('Alert 1')).not.toBeInTheDocument();
  });

  it('canonicalizes the default filter and presents a server-clamped last page', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/alerts?resolved=false&page=999&page_size=20',
        body: page([alert('21')], {
          page: '2',
          page_size: 20,
          total_items: '21',
          total_pages: '2',
        }),
      },
    ]);

    await renderWithProviders(
      <>
        <AlertsPage />
        <LocationProbe />
      </>,
      { station: 'admin', role: 'admin', route: '/alerts?page=999&page_size=20' },
    );

    expect(await screen.findByText('Alert 21')).toBeVisible();
    expect(screen.getByText('21 items')).toBeVisible();
    expect(screen.getByText('That page is no longer available. Showing page 2.')).toBeVisible();
    const search = new URLSearchParams(screen.getByTestId('location-search').textContent ?? '');
    expect(search.get('resolved')).toBe('false');
    expect(search.get('page')).toBe('999');
  });

  it('resolves the only unresolved last-page row and refetches the reduced collection', async () => {
    let resolvedOnServer = false;
    let listCalls = 0;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      const method = init?.method ?? (input instanceof Request ? input.method : 'GET');
      if (target.pathname === '/admin/api/session' && method === 'GET') {
        return jsonResponse(session());
      }
      if (target.pathname === '/admin/api/alerts/41/resolve' && method === 'POST') {
        resolvedOnServer = true;
        return jsonResponse(alert('41', true));
      }
      if (target.pathname === '/admin/api/alerts' && method === 'GET') {
        listCalls += 1;
        if (!resolvedOnServer) {
          return jsonResponse(
            page([alert('41')], { page: '3', page_size: 20, total_items: '41', total_pages: '3' }),
          );
        }
        return jsonResponse(
          page(
            Array.from({ length: 20 }, (_, index) => alert(String(index + 20))),
            { page: '2', page_size: 20, total_items: '40', total_pages: '2' },
          ),
        );
      }
      throw new Error(`Unexpected request: ${method} ${target.pathname}${target.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderWithProviders(<AlertsPage />, {
      station: 'admin',
      role: 'admin',
      route: '/alerts?resolved=false&page=3&page_size=20',
    });

    expect(await screen.findByText('Alert 41')).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Alert actions' }));
    await rendered.user.click(screen.getByRole('menuitem', { name: 'Resolve' }));
    await waitFor(() => expect(resolvedOnServer).toBe(true));
    await waitFor(() => expect(listCalls).toBeGreaterThanOrEqual(2));

    const postCalls = fetchMock.mock.calls.filter(([input, init]) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      return (
        target.pathname === '/admin/api/alerts/41/resolve' && (init?.method ?? 'GET') === 'POST'
      );
    });
    expect(postCalls).toHaveLength(1);
    expect(postCalls[0]?.[0]).toBe('/admin/api/alerts/41/resolve');
    expect(JSON.parse(String(postCalls[0]?.[1]?.body))).toEqual({ resolved: true });

    expect(await screen.findByText('Alert 20')).toBeVisible();
    await waitFor(() => expect(screen.queryByText('Alert 41')).not.toBeInTheDocument());
    expect(screen.getByText('40 items')).toBeVisible();
    expect(screen.getByText('That page is no longer available. Showing page 2.')).toBeVisible();
    const listRequests = fetchMock.mock.calls
      .filter(([input, init]) => {
        const target = new URL(
          input instanceof Request ? input.url : String(input),
          window.location.origin,
        );
        return target.pathname === '/admin/api/alerts' && (init?.method ?? 'GET') === 'GET';
      })
      .map(([input]) => String(input));
    expect(listRequests).toEqual([
      '/admin/api/alerts?resolved=false&page=3&page_size=20',
      '/admin/api/alerts?resolved=false&page=3&page_size=20',
    ]);
  });

  it.each([401, 403] as const)(
    'clears old rows after a resolve mutation returns %s',
    async (status) => {
      const fetchMock = installJsonFetchFixtures([
        { method: 'GET', path: '/admin/api/session', body: session() },
        {
          method: 'GET',
          path: '/admin/api/alerts?resolved=false&page=1&page_size=20',
          body: page([alert('1')]),
        },
        {
          method: 'POST',
          path: '/admin/api/alerts/1/resolve',
          status,
          body: {
            error: {
              code: status === 401 ? 'unauthorized' : 'forbidden',
              message: 'mutation denied',
            },
          },
        },
      ]);

      const rendered = await renderWithProviders(<AlertsPage />, {
        station: 'admin',
        role: 'admin',
        route: '/alerts?resolved=false&page=1&page_size=20',
      });

      expect(await screen.findByText('Alert 1')).toBeVisible();
      await rendered.user.click(screen.getByRole('button', { name: 'Alert actions' }));
      await rendered.user.click(screen.getByRole('menuitem', { name: 'Resolve' }));
      await waitFor(() => expect(screen.queryByText('Alert 1')).not.toBeInTheDocument());
      expect(screen.queryByRole('button', { name: 'Resolve' })).not.toBeInTheDocument();
      expect(
        screen.getByText(
          status === 401
            ? 'Your session is not active. Sign in to continue.'
            : 'This action is not allowed for the current account.',
        ),
      ).toBeVisible();
      expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual([
        '/admin/api/session',
        '/admin/api/alerts?resolved=false&page=1&page_size=20',
        '/admin/api/alerts/1/resolve',
      ]);
    },
  );

  it('keeps the same account rows visible while a new page is busy, but disables row actions', async () => {
    const nextPage = deferred<Response>();
    const firstRows = [
      alert('1'),
      ...Array.from({ length: 19 }, (_, index) => alert(String(index + 2))),
    ];
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (target.pathname === '/admin/api/session') return Promise.resolve(jsonResponse(session()));
      if (target.pathname === '/admin/api/alerts' && target.searchParams.get('page') === '1') {
        return Promise.resolve(
          jsonResponse(
            page(firstRows, { page: '1', page_size: 20, total_items: '21', total_pages: '2' }),
          ),
        );
      }
      if (target.pathname === '/admin/api/alerts' && target.searchParams.get('page') === '2') {
        return nextPage.promise;
      }
      throw new Error(`Unexpected request: ${target.pathname}${target.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderWithProviders(<AlertsPage />, {
      station: 'admin',
      role: 'admin',
      route: '/alerts?resolved=false&page=1&page_size=20',
    });

    expect(await screen.findByText('Alert 1')).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Next' }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([input]) => String(input).includes('page=2'))).toBe(true),
    );
    expect(screen.getByText('Alert 1')).toBeVisible();
    expect(document.querySelector('[aria-busy="true"]')).toBeInTheDocument();
    await rendered.user.click(screen.getAllByRole('button', { name: 'Alert actions' })[0]);
    expect(screen.getAllByRole('menuitem', { name: 'Resolve' })[0]).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
    expect(screen.getByRole('combobox', { name: 'Items per page' })).toBeDisabled();

    await act(async () => {
      nextPage.resolve(
        jsonResponse(
          page([alert('21')], { page: '2', page_size: 20, total_items: '21', total_pages: '2' }),
        ),
      );
      await nextPage.promise;
    });
    expect(await screen.findByText('Alert 21')).toBeVisible();
    expect(screen.queryByText('Alert 1')).not.toBeInTheDocument();
  });

  it('restores a previous URL collection on browser back', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/alerts?resolved=false&page=1&page_size=20',
        body: page([alert('1', false, { message: 'Alert open' })]),
      },
      {
        method: 'GET',
        path: '/admin/api/alerts?resolved=true&page=1&page_size=20',
        body: page([alert('2', true, { message: 'Alert closed' })]),
      },
    ]);

    const rendered = await renderWithProviders(
      <>
        <AlertsPage />
        <BackProbe />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/alerts?resolved=false&page=1&page_size=20',
      },
    );

    expect(await screen.findByText('Alert open')).toBeVisible();
    await rendered.user.selectOptions(
      screen.getByRole('combobox', { name: 'Resolution status' }),
      'true',
    );
    expect(await screen.findByText('Alert closed')).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Back' }));
    expect(await screen.findByText('Alert open')).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'Resolution status' })).toHaveValue('false');
    expect(screen.getByTestId('location-search')).toHaveTextContent(
      '?resolved=false&page=1&page_size=20',
    );
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      '/admin/api/alerts?resolved=false&page=1&page_size=20',
    );
  });

  it('does not let a late old-account response replace the current account page', async () => {
    const oldPage = deferred<Response>();
    let currentAccount = 'A';
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (target.pathname === '/admin/api/session')
        return Promise.resolve(jsonResponse(session('A')));
      if (target.pathname === '/admin/api/alerts') {
        return currentAccount === 'A'
          ? oldPage.promise
          : Promise.resolve(jsonResponse(page([alert('2', false, { message: 'Alert B' })])));
      }
      throw new Error(`Unexpected request: ${target.pathname}${target.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<AlertsPage />, {
      station: 'admin',
      role: 'admin',
      route: '/alerts?resolved=false&page=1&page_size=20',
    });

    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(([input]) => String(input).includes('/admin/api/alerts')),
      ).toBe(true),
    );
    currentAccount = 'B';
    rendered.queryClient.setQueryData(adminKeys.session, session('B'));
    expect(await screen.findByText('Alert B')).toBeVisible();

    await act(async () => {
      oldPage.resolve(jsonResponse(page([alert('1', false, { message: 'Alert A' })])));
      await oldPage.promise;
    });
    await waitFor(() => expect(screen.queryByText('Alert A')).not.toBeInTheDocument());
    expect(screen.getByText('Alert B')).toBeVisible();
  });
});

it.each([
  ['issue_projection_incomplete', 'issue', 'iss_' + 'A'.repeat(22), 'current'],
  ['fishing_retry_exhausted', 'fishing_batch', 'fb_' + 'A'.repeat(22), 'reserved'],
  ['rps_terminal_retrying', 'rps_session', 'rps_' + 'A'.repeat(22), 'terminal_processing'],
])(
  'opens only the exact %s target diagnostic after refresh',
  async (alertKind, targetKind, id, state) => {
    const row = alert('77', false, { kind: alertKind, ref: id });
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/alerts?resolved=false&page=1&page_size=20',
        body: page([row]),
      },
      {
        method: 'GET',
        path: '/admin/api/alerts/77',
        body: {
          alert: row,
          context_version: 1,
          occurred_facts: [],
          targets: [{ kind: targetKind, id, available: true, status: state }],
          current_state: [],
          related_logs: null,
          resolution_kind: '',
        },
      },
      {
        method: 'GET',
        path: `/admin/api/alerts/targets/${targetKind}/${id}`,
        body: {
          kind: targetKind,
          id,
          facts: [{ key: 'state', value: state }],
          related_issue_ids: [],
        },
      },
    ]);
    const rendered = await renderWithProviders(
      <>
        <AlertsPage />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        locale: 'en',
        route: `/alerts?resolved=false&alert_id=77&target_kind=${targetKind}&target_id=${id}`,
      },
    );
    const heading = await screen.findByRole('heading', {
      name: new RegExp(`Exact object diagnostic.*${id}`),
    });
    expect(heading).toBeVisible();
    expect(await within(heading.closest('.card')!).findByText(state)).toBeVisible();
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      `/admin/api/alerts/targets/${targetKind}/${id}`,
    );
    await rendered.user.click(screen.getByRole('button', { name: 'Close diagnostic' }));
    expect(screen.queryByRole('heading', { name: /Exact object diagnostic/ })).toBeNull();
    expect(screen.getByTestId('location-search')).not.toHaveTextContent('target_id=');
    const target = screen.getByText(new RegExp(`#${id}`)).closest('li');
    expect(target).not.toBeNull();
    await rendered.user.click(within(target!).getByRole('button', { name: 'Details' }));
    expect(
      await screen.findByRole('heading', { name: new RegExp(`Exact object diagnostic.*${id}`) }),
    ).toBeVisible();
  },
);

it('shows only real retained issue references for an incomplete projection alert and keeps the user link', async () => {
  const issueID = 'iss_' + 'A'.repeat(22);
  const row = alert('78', false, {
    kind: 'issue_projection_incomplete',
    ref: '',
    subject_user_id: '42',
  });
  const fetchMock = installJsonFetchFixtures([
    { method: 'GET', path: '/admin/api/session', body: session() },
    {
      method: 'GET',
      path: '/admin/api/alerts?resolved=false&page=1&page_size=20',
      body: page([row]),
    },
    {
      method: 'GET',
      path: '/admin/api/alerts/78',
      body: {
        alert: row,
        context_version: 1,
        occurred_facts: [],
        targets: [{ kind: 'user', id: '42', available: true, status: 'active' }],
        current_state: [],
        related_logs: null,
        resolution_kind: '',
      },
    },
    {
      method: 'GET',
      path: '/admin/api/alerts/targets/issue_user/42',
      body: {
        kind: 'issue_user',
        id: '42',
        facts: [
          { key: 'projection_phase', value: 'checkpointed' },
          { key: 'retained_issue_count', value: '1' },
        ],
        related_issue_ids: [issueID],
      },
    },
  ]);
  const rendered = await renderWithProviders(<AlertsPage />, {
    station: 'admin',
    role: 'admin',
    locale: 'en',
    route: '/alerts?resolved=false&alert_id=78&target_kind=issue_user&target_id=42',
  });
  expect(await screen.findByRole('link', { name: 'Details' })).toHaveAttribute(
    'href',
    '/users?user=42',
  );
  expect(await screen.findByText(issueID)).toBeVisible();
  expect(screen.getByText('Saved rebuild checkpoint')).toBeVisible();
  expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
    '/admin/api/alerts/targets/issue_user/42',
  );
  await rendered.user.click(screen.getByRole('button', { name: 'Close diagnostic' }));
  expect(screen.queryByText(issueID)).toBeNull();
  await rendered.user.click(screen.getByRole('button', { name: 'Issue projection' }));
  expect(await screen.findByText(issueID)).toBeVisible();
});

it('does not reinterpret a normal user target as issue projection from crafted URL parameters', async () => {
  const row = alert('79', false, { kind: 'fetch_failed', subject_user_id: '42' });
  const fetchMock = installJsonFetchFixtures([
    { method: 'GET', path: '/admin/api/session', body: session() },
    {
      method: 'GET',
      path: '/admin/api/alerts?resolved=false&page=1&page_size=20',
      body: page([row]),
    },
    {
      method: 'GET',
      path: '/admin/api/alerts/79',
      body: {
        alert: row,
        context_version: 1,
        occurred_facts: [],
        targets: [{ kind: 'user', id: '42', available: true, status: 'active' }],
        current_state: [],
        related_logs: null,
        resolution_kind: '',
      },
    },
  ]);
  await renderWithProviders(<AlertsPage />, {
    station: 'admin',
    role: 'admin',
    locale: 'en',
    route: '/alerts?resolved=false&alert_id=79&target_kind=issue_user&target_id=42',
  });
  expect(await screen.findByRole('link', { name: 'Details' })).toHaveAttribute(
    'href',
    '/users?user=42',
  );
  expect(screen.queryByRole('button', { name: 'Issue projection' })).toBeNull();
  expect(screen.queryByRole('heading', { name: /Exact object diagnostic/ })).toBeNull();
  expect(fetchMock.mock.calls.map(([input]) => String(input))).not.toContain(
    '/admin/api/alerts/targets/issue_user/42',
  );
});

it('does not open a stale user target from a crafted projection diagnostic URL', async () => {
  const row = alert('80', false, {
    kind: 'issue_projection_incomplete',
    ref: '',
    subject_user_id: '42',
  });
  const fetchMock = installJsonFetchFixtures([
    { method: 'GET', path: '/admin/api/session', body: session() },
    {
      method: 'GET',
      path: '/admin/api/alerts?resolved=false&page=1&page_size=20',
      body: page([row]),
    },
    {
      method: 'GET',
      path: '/admin/api/alerts/80',
      body: {
        alert: row,
        context_version: 1,
        occurred_facts: [],
        targets: [
          {
            kind: 'user',
            id: '42',
            available: false,
            status: 'missing',
            unavailable_reason: 'deleted',
          },
        ],
        current_state: [],
        related_logs: null,
        resolution_kind: '',
      },
    },
  ]);
  await renderWithProviders(<AlertsPage />, {
    station: 'admin',
    role: 'admin',
    locale: 'en',
    route: '/alerts?resolved=false&alert_id=80&target_kind=issue_user&target_id=42',
  });
  expect(await screen.findByText(/User #42/)).toBeVisible();
  expect(screen.queryByRole('button', { name: 'Issue projection' })).toBeNull();
  expect(screen.queryByRole('heading', { name: /Exact object diagnostic/ })).toBeNull();
  expect(fetchMock.mock.calls.map(([input]) => String(input))).not.toContain(
    '/admin/api/alerts/targets/issue_user/42',
  );
});

it('filters deletion alerts and resolves only selected unresolved rows', async () => {
  let bulkBody: unknown;
  let done = false;
  const snapshot = {
    user_id: '99',
    discord_id: '123456789012345678',
    general_balance: '-2.5',
    game_balance: '1',
    donation_credit: '0',
    sketch_paper: '0',
    sketch_brush: '0',
  };
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const target = new URL(
      input instanceof Request ? input.url : String(input),
      window.location.origin,
    );
    if (target.pathname === '/admin/api/session') return jsonResponse(session());
    if (target.pathname === '/admin/api/alerts/resolve') {
      bulkBody = JSON.parse(String(init?.body));
      done = true;
      return jsonResponse({ resolved_count: 1 });
    }
    if (target.pathname === '/admin/api/alerts') {
      expect(target.searchParams.get('kind')).toBe('account_deleted');
      return jsonResponse(
        page(
          done ? [] : [alert('7', false, { kind: 'account_deleted', account_deletion: snapshot })],
        ),
      );
    }
    throw new Error('Unexpected request');
  });
  vi.stubGlobal('fetch', fetchMock);
  const rendered = await renderWithProviders(<AlertsPage />, {
    station: 'admin',
    role: 'admin',
    route: '/alerts?resolved=false&kind=account_deleted',
  });
  await rendered.user.click(
    await screen.findByText(rendered.i18n.t('admin.alerts.presentation.deletionSnapshot'), {
      selector: 'strong',
    }),
  );
  expect(await screen.findByText('123456789012345678')).toBeVisible();
  expect(screen.getByText('-2.5')).toBeVisible();
  await waitFor(() =>
    expect(screen.getByRole('checkbox', { name: 'Select alert 7' })).toBeEnabled(),
  );
  await rendered.user.click(screen.getByRole('checkbox', { name: 'Select alert 7' }));
  await rendered.user.click(screen.getByRole('button', { name: 'Resolve selected (1)' }));
  await waitFor(() => expect(bulkBody).toEqual({ ids: ['7'] }));
  await waitFor(() => expect(screen.queryByText('123456789012345678')).not.toBeInTheDocument());
});

it('renders current deletion snapshots with all filters and opens both retained details', async () => {
  const rows = [
    alert('9', false),
    alert('8', true, { kind: 'account_deleted', account_deletion: deletionSnapshots.v2 }),
    alert('7', false, { kind: 'account_deleted', account_deletion: deletionSnapshots.v1 }),
  ];
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const target = new URL(
      input instanceof Request ? input.url : String(input),
      window.location.origin,
    );
    if (target.pathname === '/admin/api/session') return jsonResponse(session());
    if (target.pathname === '/admin/api/alerts') {
      const resolved = target.searchParams.get('resolved');
      const kind = target.searchParams.get('kind');
      return jsonResponse(
        page(
          rows.filter(
            (row) =>
              (resolved === null || row.resolved === (resolved === 'true')) &&
              (kind === null || row.kind === kind),
          ),
        ),
      );
    }
    const selected = rows.find((row) => target.pathname === `/admin/api/alerts/${row.id}`);
    if (selected)
      return jsonResponse({
        alert: selected,
        context_version: 0,
        occurred_facts: [],
        targets: [
          { kind: 'deleted_account', id: selected.id, available: true, status: 'retained' },
        ],
        current_state: [],
        related_logs: null,
        resolution_kind: 'legacy',
      });
    throw new Error(`Unexpected request: ${target.pathname}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  const rendered = await renderWithProviders(<AlertsPage />, {
    station: 'admin',
    role: 'admin',
    route: '/alerts?resolved=all',
  });
  for (const summary of await screen.findAllByText(
    rendered.i18n.t('admin.alerts.presentation.deletionSnapshot'),
    { selector: 'strong' },
  ))
    await rendered.user.click(summary);
  expect(await screen.findByText(deletionSnapshots.v1.discord_id)).toBeVisible();
  expect(screen.getByText(deletionSnapshots.v2.discord_id)).toBeVisible();
  expect(screen.getByText('Alert 9')).toBeVisible();
  expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
    '/admin/api/alerts?page=1&page_size=20',
  );
  await rendered.user.selectOptions(
    screen.getByRole('combobox', { name: 'Alert type' }),
    'account_deleted',
  );
  await waitFor(() => expect(screen.queryByText('Alert 9')).not.toBeInTheDocument());
  for (const [id, snapshot] of [
    ['7', deletionSnapshots.v1],
    ['8', deletionSnapshots.v2],
  ] as const) {
    const row = screen.getByText(snapshot.discord_id).closest('tr');
    await rendered.user.click(within(row!).getByRole('button', { name: 'Details' }));
    expect(await screen.findByRole('heading', { name: `Alert details #${id}` })).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Close details' }));
  }
  await rendered.user.selectOptions(
    screen.getByRole('combobox', { name: 'Resolution status' }),
    'true',
  );
  await waitFor(() =>
    expect(screen.queryByText(deletionSnapshots.v1.discord_id)).not.toBeInTheDocument(),
  );
  expect(screen.getByText(deletionSnapshots.v2.discord_id)).toBeVisible();
  await rendered.user.selectOptions(
    screen.getByRole('combobox', { name: 'Resolution status' }),
    'false',
  );
  for (const summary of await screen.findAllByText(
    rendered.i18n.t('admin.alerts.presentation.deletionSnapshot'),
    { selector: 'strong' },
  ))
    await rendered.user.click(summary);
  expect(await screen.findByText(deletionSnapshots.v1.discord_id)).toBeVisible();
  await waitFor(() =>
    expect(screen.queryByText(deletionSnapshots.v2.discord_id)).not.toBeInTheDocument(),
  );
});
