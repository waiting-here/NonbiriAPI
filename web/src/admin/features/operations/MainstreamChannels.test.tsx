import { act, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useLocation, useNavigate } from 'react-router';
import { installJsonFetchFixtures, renderWithProviders } from '../../../../test/unit/support';
import { adminKeys } from '../../data';
import { MainstreamChannelsPanel } from './MainstreamChannels';
import type { AdminMainstreamChannel } from './channels';

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.removeItem('nonbiri:admin:mainstream-channels-page-size:v1');
});

const activeID = `mch_${'A'.repeat(22)}`;
const retiredID = `mch_${'Q'.repeat(22)}`;

function generatedID(index: number): string {
  return `mch_${index.toString(36).padStart(21, 'A')}A`;
}

function channel(
  id: string,
  name: string,
  state: 'active' | 'retired' = 'active',
): AdminMainstreamChannel {
  return {
    id,
    name,
    category: 'subscription',
    connector_type: 'openai-compatible',
    base_url: 'https://api.example.test/v1',
    enabled: state === 'active',
    state,
    revision: state === 'active' ? '3' : '4',
    created_at: 1_800_000_000,
    updated_at: 1_800_000_001,
    retired_at: state === 'active' ? null : 1_800_000_002,
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

function session(username = 'admin') {
  return { admin: { username } };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

function LocationProbe() {
  const location = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <output data-testid="location-search">{location.search}</output>
      <button type="button" data-testid="back" onClick={() => navigate(-1)}>
        back
      </button>
    </>
  );
}

describe('administrator mainstream channels page', () => {
  it('uses URL page mode, remembers size, and resets page on a state change', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/mainstream-channels?state=active&page=1&page_size=20',
        body: page(
          [
            channel(activeID, 'Active first'),
            ...Array.from({ length: 19 }, (_, index) =>
              channel(generatedID(index + 1), `Active other ${index + 1}`),
            ),
          ],
          {
            page: '1',
            page_size: 20,
            total_items: '21',
            total_pages: '2',
          },
        ),
      },
      {
        method: 'GET',
        path: '/admin/api/mainstream-channels?state=active&page=2&page_size=20',
        body: page([channel(activeID, 'Active second')], {
          page: '2',
          page_size: 20,
          total_items: '21',
          total_pages: '2',
        }),
      },
      {
        method: 'GET',
        path: '/admin/api/mainstream-channels?state=retired&page=1&page_size=20',
        body: page([channel(retiredID, 'Retired one', 'retired')]),
      },
      {
        method: 'GET',
        path: '/admin/api/mainstream-channels?state=retired&page=1&page_size=50',
        body: page([channel(retiredID, 'Retired one', 'retired')], {
          page: '1',
          page_size: 50,
          total_items: '1',
          total_pages: '1',
        }),
      },
    ]);

    const rendered = await renderWithProviders(
      <>
        <MainstreamChannelsPanel />
        <LocationProbe />
      </>,
      { station: 'admin', role: 'admin' },
    );

    expect(await screen.findByText('Active first')).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'Items per page' })).toHaveValue('20');
    expect(screen.getByText('Page 1 of 2 · 21 items')).toBeVisible();
    await waitFor(() => {
      const params = new URLSearchParams(screen.getByTestId('location-search').textContent ?? '');
      expect(params.get('state')).toBe('active');
    });
    await rendered.user.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByText('Active second')).toBeVisible();

    await rendered.user.selectOptions(
      screen.getByRole('combobox', { name: 'Channel state' }),
      'retired',
    );
    expect(await screen.findByText('Retired one')).toBeVisible();
    await waitFor(() => expect(screen.queryByText('Active second')).not.toBeInTheDocument());
    let params = new URLSearchParams(screen.getByTestId('location-search').textContent ?? '');
    expect(params.get('state')).toBe('retired');
    expect(params.get('page')).toBe('1');
    expect(params.get('page_size')).toBe('20');

    await rendered.user.selectOptions(
      screen.getByRole('combobox', { name: 'Items per page' }),
      '50',
    );
    await waitFor(() =>
      expect(screen.getByRole('combobox', { name: 'Items per page' })).toHaveValue('50'),
    );
    expect(window.localStorage.getItem('nonbiri:admin:mainstream-channels-page-size:v1')).toBe(
      '50',
    );
    params = new URLSearchParams(screen.getByTestId('location-search').textContent ?? '');
    expect(params.get('state')).toBe('retired');
    expect(params.get('page')).toBe('1');
    expect(params.get('page_size')).toBe('50');
    expect(fetchMock.mock.calls.map(([input]) => String(input))).not.toContain(
      '/admin/api/mainstream-channels?state=active&cursor=',
    );
  });

  it('restores filter and page after browser back navigation', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/mainstream-channels?state=active&page=2&page_size=50',
        body: page([channel(activeID, 'Active page two')], {
          page: '2',
          page_size: 50,
          total_items: '51',
          total_pages: '2',
        }),
      },
      {
        method: 'GET',
        path: '/admin/api/mainstream-channels?state=retired&page=1&page_size=50',
        body: page([channel(retiredID, 'Retired page one', 'retired')], {
          page: '1',
          page_size: 50,
          total_items: '1',
          total_pages: '1',
        }),
      },
    ]);

    const rendered = await renderWithProviders(
      <>
        <MainstreamChannelsPanel />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/mainstream-channels?state=active&page=2&page_size=50',
      },
    );

    expect(await screen.findByText('Active page two')).toBeVisible();
    await rendered.user.selectOptions(
      screen.getByRole('combobox', { name: 'Channel state' }),
      'retired',
    );
    expect(await screen.findByText('Retired page one')).toBeVisible();
    await rendered.user.click(screen.getByTestId('back'));
    expect(await screen.findByText('Active page two')).toBeVisible();
    expect(screen.getByTestId('location-search')).toHaveTextContent(
      '?state=active&page=2&page_size=50',
    );
    expect(screen.getByRole('combobox', { name: 'Channel state' })).toHaveValue('active');
  });

  it('keeps the edit flow on the current page and supports save and cancel', async () => {
    let current = channel(activeID, 'Original channel');
    const requests: { method: string; body: string | null; headers: Headers }[] = [];
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      const method = String(
        init?.method ?? (input instanceof Request ? input.method : 'GET'),
      ).toUpperCase();
      if (target.pathname === '/admin/api/session') return jsonResponse(session());
      if (target.pathname === '/admin/api/mainstream-channels' && method === 'GET') {
        return jsonResponse(page([current]));
      }
      if (target.pathname === `/admin/api/mainstream-channels/${activeID}` && method === 'GET') {
        return jsonResponse(current);
      }
      if (target.pathname === `/admin/api/mainstream-channels/${activeID}` && method === 'PATCH') {
        requests.push({
          method,
          body: typeof init?.body === 'string' ? init.body : null,
          headers: new Headers(init?.headers),
        });
        current = { ...current, name: 'Updated channel', revision: '4', updated_at: 1_800_000_003 };
        return jsonResponse(current);
      }
      throw new Error(`Unexpected request: ${method} ${target.pathname}${target.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderWithProviders(<MainstreamChannelsPanel />, {
      station: 'admin',
      role: 'admin',
    });

    expect(await screen.findByText('Original channel')).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'View' }));
    expect(await screen.findByRole('heading', { name: 'Channel authority' })).toBeVisible();

    const nameInputs = screen.getAllByLabelText('Channel name');
    await rendered.user.clear(nameInputs[1]);
    await rendered.user.type(nameInputs[1], 'Updated channel');
    await rendered.user.click(screen.getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(requests).toHaveLength(1));
    expect(JSON.parse(requests[0]?.body ?? 'null')).toEqual({
      expected_revision: '3',
      name: 'Updated channel',
    });
    expect(requests[0]?.headers.get('Idempotency-Key')).toMatch(/^[A-Za-z0-9_-]{22}$/);
    expect(await screen.findByText('Updated channel')).toBeVisible();

    await rendered.user.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() =>
      expect(screen.queryByRole('heading', { name: 'Channel authority' })).not.toBeInTheDocument(),
    );
  });

  it('does not request private channels before an admin session is authorized', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: {}, status: 401 },
    ]);

    await renderWithProviders(<MainstreamChannelsPanel />, { station: 'admin', role: 'admin' });

    expect(await screen.findByRole('heading', { name: 'Sign-in required' })).toBeVisible();
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('disables creation while the admin session is still being confirmed', async () => {
    let resolveSession!: (response: Response) => void;
    const sessionResponse = new Promise<Response>((resolve) => {
      resolveSession = resolve;
    });
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (target.pathname === '/admin/api/session') return sessionResponse;
      if (target.pathname === '/admin/api/mainstream-channels') {
        return Promise.resolve(jsonResponse(page([channel(activeID, 'Authorized channel')])));
      }
      throw new Error(`Unexpected request: ${target.pathname}${target.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderWithProviders(<MainstreamChannelsPanel />, {
      station: 'admin',
      role: 'admin',
    });

    expect(screen.getByLabelText('Channel name')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Create channel' })).toBeDisabled();
    expect(fetchMock).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolveSession(jsonResponse(session()));
      await sessionResponse;
    });
    expect(await screen.findByText('Authorized channel')).toBeVisible();
    expect(screen.getByLabelText('Channel name')).not.toBeDisabled();
    expect(screen.getByRole('button', { name: 'Create channel' })).not.toBeDisabled();
    expect(rendered.queryClient.getQueryData(adminKeys.session)).toEqual(session());
  });

  it.each([401, 403])('closes channel write entries after page %s', async (status) => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/mainstream-channels?state=active&page=1&page_size=20',
        body: {},
        status,
      },
    ]);

    await renderWithProviders(<MainstreamChannelsPanel />, { station: 'admin', role: 'admin' });

    expect(
      await screen.findByRole('heading', {
        name: status === 401 ? 'Sign-in required' : 'Something went wrong',
      }),
    ).toBeVisible();
    expect(screen.getByLabelText('Channel name')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Create channel' })).toBeDisabled();
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it.each([401, 403])('closes all channel writes after a mutation %s', async (status) => {
    const current = channel(activeID, 'Mutation authority');
    const fetchMock = vi.fn((input: string | URL | Request, init?: RequestInit) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      const method = String(
        init?.method ?? (input instanceof Request ? input.method : 'GET'),
      ).toUpperCase();
      if (target.pathname === '/admin/api/session') return Promise.resolve(jsonResponse(session()));
      if (target.pathname === '/admin/api/mainstream-channels' && method === 'GET') {
        return Promise.resolve(jsonResponse(page([current])));
      }
      if (target.pathname === `/admin/api/mainstream-channels/${activeID}` && method === 'GET') {
        return Promise.resolve(jsonResponse(current));
      }
      if (target.pathname === `/admin/api/mainstream-channels/${activeID}` && method === 'PATCH') {
        return Promise.resolve(jsonResponse({}, status));
      }
      throw new Error(`Unexpected request: ${method} ${target.pathname}${target.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderWithProviders(<MainstreamChannelsPanel />, {
      station: 'admin',
      role: 'admin',
    });

    expect(await screen.findByText('Mutation authority')).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'View' }));
    expect(await screen.findByRole('heading', { name: 'Channel authority' })).toBeVisible();
    const editName = screen.getAllByLabelText('Channel name')[1];
    await rendered.user.clear(editName);
    await rendered.user.type(editName, 'Mutation failure');
    await rendered.user.click(screen.getByRole('button', { name: 'Save changes' }));
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(([input, request]) => {
          const target = new URL(
            input instanceof Request ? input.url : String(input),
            window.location.origin,
          );
          return (
            target.pathname === `/admin/api/mainstream-channels/${activeID}` &&
            request?.method === 'PATCH'
          );
        }),
      ).toBe(true),
    );

    expect(
      await screen.findByRole('heading', {
        name: status === 401 ? 'Sign-in required' : 'Something went wrong',
      }),
    ).toBeVisible();
    await waitFor(() =>
      expect(screen.queryByRole('heading', { name: 'Channel authority' })).not.toBeInTheDocument(),
    );
    expect(screen.getByLabelText('Channel name')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Create channel' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Retire' })).not.toBeInTheDocument();
  });

  it('clears the open detail and create draft when the admin account changes', async () => {
    let adminAccount = 'one';
    let holdLateDetail = false;
    let resolveLateDetail!: (response: Response) => void;
    const lateDetail = new Promise<Response>((resolve) => {
      resolveLateDetail = resolve;
    });
    const accountOne = channel(activeID, 'Account one');
    const accountTwo = channel(generatedID(2), 'Account two');
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (target.pathname === '/admin/api/session') {
        return Promise.resolve(jsonResponse(session('one')));
      }
      if (
        target.pathname === '/admin/api/mainstream-channels' &&
        target.search === '?state=active&page=1&page_size=20'
      ) {
        return Promise.resolve(
          jsonResponse(page([adminAccount === 'one' ? accountOne : accountTwo])),
        );
      }
      if (target.pathname === `/admin/api/mainstream-channels/${activeID}`) {
        if (adminAccount === 'one' && holdLateDetail) return lateDetail;
        return Promise.resolve(jsonResponse(adminAccount === 'one' ? accountOne : accountTwo));
      }
      throw new Error(`Unexpected request: ${target.pathname}${target.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderWithProviders(<MainstreamChannelsPanel />, {
      station: 'admin',
      role: 'admin',
    });

    expect(await screen.findByText('Account one')).toBeVisible();
    const createName = screen.getByLabelText('Channel name');
    await rendered.user.type(createName, 'Account one draft');
    await rendered.user.click(screen.getByRole('button', { name: 'View' }));
    expect(await screen.findByRole('heading', { name: 'Channel authority' })).toBeVisible();
    expect(screen.getAllByLabelText('Channel name')).toHaveLength(2);

    holdLateDetail = true;
    void rendered.queryClient.invalidateQueries({
      queryKey: ['admin', 'operations', 'mainstream-channels', 'detail', 'one', activeID],
    });
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.filter(([input]) =>
          String(input).includes(`/admin/api/mainstream-channels/${activeID}`),
        ),
      ).toHaveLength(2),
    );

    adminAccount = 'two';
    rendered.queryClient.setQueryData(adminKeys.session, { admin: { username: 'two' } });
    expect(await screen.findByText('Account two')).toBeVisible();
    await waitFor(() => {
      expect(screen.queryByRole('heading', { name: 'Channel authority' })).not.toBeInTheDocument();
      expect(screen.getByLabelText('Channel name')).toHaveValue('');
    });
    expect(screen.queryByRole('button', { name: 'Save changes' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Retire' })).not.toBeInTheDocument();

    await act(async () => {
      resolveLateDetail(jsonResponse({ ...accountOne, name: 'Late account one' }));
      await lateDetail;
    });
    expect(screen.queryByText('Late account one')).not.toBeInTheDocument();
    expect(screen.getByText('Account two')).toBeVisible();
  });

  it('drops a late edit result after the admin session is invalidated', async () => {
    let resolvePatch!: (response: Response) => void;
    const patchResponse = new Promise<Response>((resolve) => {
      resolvePatch = resolve;
    });
    const current = channel(activeID, 'Before invalidation');
    const fetchMock = vi.fn((input: string | URL | Request, init?: RequestInit) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      const method = String(
        init?.method ?? (input instanceof Request ? input.method : 'GET'),
      ).toUpperCase();
      if (target.pathname === '/admin/api/session')
        return Promise.resolve(jsonResponse(session('one')));
      if (target.pathname === '/admin/api/mainstream-channels' && method === 'GET') {
        return Promise.resolve(jsonResponse(page([current])));
      }
      if (target.pathname === `/admin/api/mainstream-channels/${activeID}` && method === 'GET') {
        return Promise.resolve(jsonResponse(current));
      }
      if (target.pathname === `/admin/api/mainstream-channels/${activeID}` && method === 'PATCH') {
        return patchResponse;
      }
      throw new Error(`Unexpected request: ${method} ${target.pathname}${target.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderWithProviders(<MainstreamChannelsPanel />, {
      station: 'admin',
      role: 'admin',
    });

    expect(await screen.findByText('Before invalidation')).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'View' }));
    expect(await screen.findByRole('heading', { name: 'Channel authority' })).toBeVisible();
    const editName = screen.getAllByLabelText('Channel name')[1];
    await rendered.user.clear(editName);
    await rendered.user.type(editName, 'Late edit');
    await rendered.user.click(screen.getByRole('button', { name: 'Save changes' }));
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(([input, request]) => {
          const target = new URL(
            input instanceof Request ? input.url : String(input),
            window.location.origin,
          );
          return (
            target.pathname === `/admin/api/mainstream-channels/${activeID}` &&
            request?.method === 'PATCH'
          );
        }),
      ).toBe(true),
    );

    rendered.queryClient.setQueryData(adminKeys.session, null);
    await waitFor(() => {
      expect(screen.queryByRole('heading', { name: 'Channel authority' })).not.toBeInTheDocument();
      expect(screen.getByLabelText('Channel name')).toBeDisabled();
      expect(screen.getByRole('button', { name: 'Create channel' })).toBeDisabled();
    });

    await act(async () => {
      resolvePatch(jsonResponse({ ...current, name: 'Late edit', revision: '4' }));
      await patchResponse;
    });
    expect(screen.queryByText('Late edit')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Create channel' })).toBeDisabled();
  });

  it('keeps the old page visible and disables row controls while a new page is busy', async () => {
    let resolveNext!: (response: Response) => void;
    const nextPage = new Promise<Response>((resolve) => {
      resolveNext = resolve;
    });
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (target.pathname === '/admin/api/session') {
        return Promise.resolve(jsonResponse(session()));
      }
      if (
        target.pathname === '/admin/api/mainstream-channels' &&
        target.searchParams.get('page') === '1'
      ) {
        return Promise.resolve(
          jsonResponse(
            page(
              [
                channel(activeID, 'Busy first'),
                ...Array.from({ length: 19 }, (_, index) =>
                  channel(generatedID(index + 1), `Busy other ${index + 1}`),
                ),
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
      if (
        target.pathname === '/admin/api/mainstream-channels' &&
        target.searchParams.get('page') === '2'
      ) {
        return nextPage;
      }
      throw new Error(`Unexpected request: ${target.pathname}${target.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderWithProviders(<MainstreamChannelsPanel />, {
      station: 'admin',
      role: 'admin',
    });

    expect(await screen.findByText('Busy first')).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Next' }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([input]) => String(input).includes('page=2'))).toBe(true),
    );
    expect(screen.getByText('Busy first')).toBeVisible();
    expect(document.querySelector('[aria-busy="true"]')).toBeInTheDocument();
    expect(
      screen
        .getAllByRole('button', { name: 'View' })
        .every((button) => (button as HTMLButtonElement).disabled),
    ).toBe(true);
    expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
    expect(screen.getByRole('combobox', { name: 'Items per page' })).toBeDisabled();

    await act(async () => {
      resolveNext(
        jsonResponse(
          page([channel(activeID, 'Busy second')], {
            page: '2',
            page_size: 20,
            total_items: '21',
            total_pages: '2',
          }),
        ),
      );
      await nextPage;
    });
    expect(await screen.findByText('Busy second')).toBeVisible();
    expect(screen.queryByText('Busy first')).not.toBeInTheDocument();
  });

  it('drops an old channel page after the admin account changes', async () => {
    let resolveOld!: (response: Response) => void;
    const oldPage = new Promise<Response>((resolve) => {
      resolveOld = resolve;
    });
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (target.pathname === '/admin/api/session')
        return Promise.resolve(jsonResponse(session('one')));
      if (target.pathname === '/admin/api/mainstream-channels') {
        return adminAccount === 'one'
          ? oldPage
          : Promise.resolve(jsonResponse(page([channel(retiredID, 'Account two')])));
      }
      throw new Error(`Unexpected request: ${target.pathname}${target.search}`);
    });
    let adminAccount = 'one';
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderWithProviders(<MainstreamChannelsPanel />, {
      station: 'admin',
      role: 'admin',
    });

    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(([input]) =>
          String(input).includes('/admin/api/mainstream-channels'),
        ),
      ).toBe(true),
    );
    adminAccount = 'two';
    rendered.queryClient.setQueryData(adminKeys.session, { admin: { username: 'two' } });
    expect(await screen.findByText('Account two')).toBeVisible();

    await act(async () => {
      resolveOld(jsonResponse(page([channel(activeID, 'Account one')])));
      await oldPage;
    });
    await waitFor(() => expect(screen.queryByText('Account one')).not.toBeInTheDocument());
    expect(screen.getByText('Account two')).toBeVisible();
  });
});
