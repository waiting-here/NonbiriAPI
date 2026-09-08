import { act, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useLocation, useNavigate } from 'react-router';
import { installJsonFetchFixtures, renderWithProviders } from '../../../test/unit/support';
import { adminKeys } from '../data';
import { AlertsPage } from './AlertsPage';

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

    expect(await screen.findByText('This action is not allowed for the current account.')).toBeVisible();
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
    expect(screen.getByText('Page 2 of 2 · 51 items')).toBeVisible();
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
        body: page([alert('21')], { page: '2', page_size: 20, total_items: '21', total_pages: '2' }),
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
    expect(screen.getByText('Page 2 of 2 · 21 items')).toBeVisible();
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
    await rendered.user.click(screen.getByRole('button', { name: 'Resolve' }));
    await waitFor(() => expect(resolvedOnServer).toBe(true));
    await waitFor(() => expect(listCalls).toBeGreaterThanOrEqual(2));

    const postCalls = fetchMock.mock.calls.filter(([input, init]) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      return target.pathname === '/admin/api/alerts/41/resolve' && (init?.method ?? 'GET') === 'POST';
    });
    expect(postCalls).toHaveLength(1);
    expect(postCalls[0]?.[0]).toBe('/admin/api/alerts/41/resolve');
    expect(JSON.parse(String(postCalls[0]?.[1]?.body))).toEqual({ resolved: true });

    expect(await screen.findByText('Alert 20')).toBeVisible();
    await waitFor(() => expect(screen.queryByText('Alert 41')).not.toBeInTheDocument());
    expect(screen.getByText('Page 2 of 2 · 40 items')).toBeVisible();
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

  it.each([401, 403] as const)('clears old rows after a resolve mutation returns %s', async (status) => {
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
    await rendered.user.click(screen.getByRole('button', { name: 'Resolve' }));
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
  });

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
          jsonResponse(page(firstRows, { page: '1', page_size: 20, total_items: '21', total_pages: '2' })),
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
    expect(screen.getAllByRole('button', { name: 'Resolve' })[0]).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
    expect(screen.getByRole('combobox', { name: 'Items per page' })).toBeDisabled();

    await act(async () => {
      nextPage.resolve(
        jsonResponse(page([alert('21')], { page: '2', page_size: 20, total_items: '21', total_pages: '2' })),
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
      if (target.pathname === '/admin/api/session') return Promise.resolve(jsonResponse(session('A')));
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
      expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/admin/api/alerts'))).toBe(
        true,
      ),
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
