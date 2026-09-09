import { act, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useLocation, useNavigate } from 'react-router';
import { installJsonFetchFixtures, renderWithProviders } from '../../../../test/unit/support';
import { LegalHoldPanel } from './LegalHoldPanel';

const HOLD_ID = `lgh_${'A'.repeat(22)}`;
const OTHER_HOLD_ID = `lgh_${'B'.repeat(21)}A`;
const REPORT_ID = `rpc_${'B'.repeat(21)}A`;
const OTHER_REPORT_ID = `rpc_${'C'.repeat(21)}A`;

function session(username = 'root') {
  return { admin: { username } };
}

function hold(overrides: Record<string, unknown> = {}) {
  return {
    id: HOLD_ID,
    object_kind: 'report_case',
    object_ref: REPORT_ID,
    state: 'active',
    revision: '1',
    created_at: 1_800_000_000,
    expires_at: 1_800_086_400,
    ended_at: null,
    ...overrides,
  };
}

function page(data: unknown[], overrides: Record<string, unknown> = {}) {
  return {
    data,
    next_cursor: null,
    pagination: {
      page: '1',
      page_size: 20,
      total_items: String(data.length),
      total_pages: '1',
      ...overrides,
    },
  };
}

function detail(overrides: Record<string, unknown> = {}) {
  return {
    ...hold(),
    basis: 'retention basis',
    end_reason: null,
    ...overrides,
  };
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

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('administrator legal hold panel', () => {
  it.each(['elevation', 'release'] as const)(
    'ignores a late %s failure after the administrator changes',
    async (stage) => {
      const delayed = deferred<Response>();
      let currentAccount = 'A';
      const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
        const url = new URL(String(input), 'http://admin.test');
        if (url.pathname === '/admin/api/session') return jsonResponse(session(currentAccount));
        if (url.pathname === '/admin/api/legal-holds')
          return jsonResponse(
            page([hold({ object_ref: currentAccount === 'A' ? REPORT_ID : OTHER_REPORT_ID })]),
          );
        if (url.pathname === `/admin/api/legal-holds/${HOLD_ID}`) return jsonResponse(detail());
        if (url.pathname === '/admin/api/auth/elevate')
          return stage === 'elevation'
            ? delayed.promise
            : jsonResponse({
                token: 'elevated-token-12345678901234567890',
                expires_at: 1_800_000_300,
              });
        if (url.pathname.endsWith('/release') && init?.method === 'POST') return delayed.promise;
        throw new Error(`Unexpected request: ${url.pathname}`);
      });
      vi.stubGlobal('fetch', fetchMock);
      const rendered = await renderWithProviders(
        <>
          <LegalHoldPanel />
          <LocationProbe />
        </>,
        { station: 'admin', route: `/settings?hold_id=${HOLD_ID}` },
      );
      await screen.findByRole('heading', { name: 'Hold metadata' });
      await rendered.user.type(screen.getByLabelText('Release reason'), 'retire');
      await rendered.user.type(
        screen.getByLabelText('Administrator password (fresh elevation)'),
        'synthetic-test-password',
      );
      await rendered.user.click(
        screen.getByRole('checkbox', {
          name: 'Release is final; this object cannot receive another hold.',
        }),
      );
      await rendered.user.click(screen.getByRole('button', { name: 'Release hold' }));
      const confirmButtons = screen.getAllByRole('button', { name: 'Release hold' });
      await rendered.user.click(confirmButtons.at(-1)!);
      await waitFor(() =>
        expect(
          fetchMock.mock.calls.some(([input]) =>
            String(input).endsWith(stage === 'elevation' ? '/auth/elevate' : '/release'),
          ),
        ).toBe(true),
      );
      currentAccount = 'B';
      act(() => {
        rendered.queryClient.setQueryData(['admin', 'session'], session('B'));
      });
      expect(await screen.findByText(OTHER_REPORT_ID)).toBeVisible();
      await waitFor(() =>
        expect(screen.getByTestId('location-search')).not.toHaveTextContent('hold_id'),
      );
      await act(async () => {
        delayed.resolve(
          jsonResponse({ error: { code: 'forbidden', message: 'old session denied' } }, 403),
        );
        await delayed.promise;
      });
      expect(rendered.queryClient.getQueryData(['admin', 'session'])).toEqual(session('B'));
      expect(screen.getByText(OTHER_REPORT_ID)).toBeVisible();
      expect(screen.queryByDisplayValue('synthetic-test-password')).not.toBeInTheDocument();
      expect(
        fetchMock.mock.calls.filter(([input]) => String(input).endsWith('/release')),
      ).toHaveLength(stage === 'release' ? 1 : 0);
    },
  );
  it('waits for the real administrator session and restores hold filters, page, and size', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/legal-holds?state=active&object_kind=report_case&page=2&page_size=50',
        body: page([hold()], { page: '2', page_size: 50, total_items: '51', total_pages: '2' }),
      },
    ]);

    await renderWithProviders(
      <>
        <LegalHoldPanel />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/settings?hold_state=active&hold_kind=report_case&hold_page=2&hold_page_size=50',
      },
    );

    expect(await screen.findByText(REPORT_ID)).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'State' })).toHaveValue('active');
    expect(screen.getAllByRole('combobox', { name: 'Object kind' })[0]).toHaveValue('report_case');
    expect(screen.getByRole('combobox', { name: 'Items per page' })).toHaveValue('50');
    expect(screen.getByText('Page 2 of 2 · Total: 51')).toBeVisible();
    expect(screen.getByTestId('location-search')).toHaveTextContent(
      '?hold_state=active&hold_kind=report_case&hold_page=2&hold_page_size=50',
    );
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual([
      '/admin/api/session',
      '/admin/api/legal-holds?state=active&object_kind=report_case&page=2&page_size=50',
    ]);
  });

  it('resets the page for a filter change while preserving the current page size in the URL', async () => {
    const first = hold();
    const second = hold({
      state: 'expired',
      object_ref: OTHER_REPORT_ID,
      ended_at: 1_800_000_100,
    });
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/legal-holds?state=active&object_kind=report_case&page=3&page_size=50',
        body: page([first], { page: '3', page_size: 50, total_items: '101', total_pages: '3' }),
      },
      {
        method: 'GET',
        path: '/admin/api/legal-holds?state=expired&object_kind=report_case&page=1&page_size=50',
        body: page([second], { page: '1', page_size: 50, total_items: '1', total_pages: '1' }),
      },
    ]);

    const rendered = await renderWithProviders(
      <>
        <LegalHoldPanel />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/settings?hold_state=active&hold_kind=report_case&hold_page=3&hold_page_size=50',
      },
    );

    expect(await screen.findByText(REPORT_ID)).toBeVisible();
    await rendered.user.selectOptions(screen.getByRole('combobox', { name: 'State' }), 'expired');

    expect(await screen.findByText(OTHER_REPORT_ID)).toBeVisible();
    const search = new URLSearchParams(screen.getByTestId('location-search').textContent ?? '');
    expect(search.get('hold_state')).toBe('expired');
    expect(search.get('hold_kind')).toBe('report_case');
    expect(search.get('hold_page')).toBe('1');
    expect(search.get('hold_page_size')).toBe('50');
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      '/admin/api/legal-holds?state=expired&object_kind=report_case&page=1&page_size=50',
    );
    expect(screen.queryByText(REPORT_ID)).not.toBeInTheDocument();
  });

  it.each([
    {
      filter: 'hold_state' as const,
      value: 'released',
      queryName: 'state',
      nextObject: hold({
        state: 'released',
        object_ref: OTHER_REPORT_ID,
        ended_at: 1_800_000_100,
      }),
      nextReference: OTHER_REPORT_ID,
    },
    {
      filter: 'hold_kind' as const,
      value: 'donation',
      queryName: 'object_kind',
      nextObject: hold({ object_kind: 'donation', object_ref: '123' }),
      nextReference: '123',
    },
  ])(
    'does not retain rows while the $filter page is loading',
    async ({ filter, value, queryName, nextObject, nextReference }) => {
      const delayedPage = deferred<Response>();
      const fetchMock = vi.fn((input: string | URL | Request) => {
        const target = new URL(
          input instanceof Request ? input.url : String(input),
          window.location.origin,
        );
        if (target.pathname === '/admin/api/session') {
          return Promise.resolve(jsonResponse(session()));
        }
        if (target.pathname === '/admin/api/legal-holds') {
          if (target.searchParams.get(queryName) === value) return delayedPage.promise;
          return Promise.resolve(jsonResponse(page([hold()])));
        }
        throw new Error(`Unexpected request: ${target.pathname}${target.search}`);
      });
      vi.stubGlobal('fetch', fetchMock);

      const rendered = await renderWithProviders(<LegalHoldPanel />, {
        station: 'admin',
        role: 'admin',
        route: '/settings?hold_page=1&hold_page_size=20',
      });

      expect(await screen.findByText(REPORT_ID)).toBeVisible();
      await rendered.user.selectOptions(
        (filter === 'hold_state'
          ? screen.getByRole('combobox', { name: 'State' })
          : screen.getAllByRole('combobox', { name: 'Object kind' })[0])!,
        value,
      );
      await waitFor(() =>
        expect(
          fetchMock.mock.calls.some(
            ([input]) =>
              String(input).includes(`/admin/api/legal-holds?${queryName}=${value}`) ||
              String(input).includes(`&${queryName}=${value}`),
          ),
        ).toBe(true),
      );
      expect(screen.queryByText(REPORT_ID)).not.toBeInTheDocument();

      await act(async () => {
        delayedPage.resolve(jsonResponse(page([nextObject])));
        await delayedPage.promise;
      });
      expect(await screen.findByText(nextReference)).toBeVisible();
    },
  );

  it('uses the default filters and presents a server-clamped last page', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/legal-holds?page=999&page_size=20',
        body: page([hold()], { page: '2', page_size: 20, total_items: '21', total_pages: '2' }),
      },
    ]);

    await renderWithProviders(
      <>
        <LegalHoldPanel />
        <LocationProbe />
      </>,
      { station: 'admin', role: 'admin', route: '/settings?hold_page=999&hold_page_size=20' },
    );

    expect(await screen.findByText(REPORT_ID)).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'State' })).toHaveValue('');
    expect(screen.getAllByRole('combobox', { name: 'Object kind' })[0]).toHaveValue('');
    expect(screen.getByText('Page 2 of 2 · Total: 21')).toBeVisible();
    expect(screen.getByText('That page is no longer available. Showing page 2.')).toBeVisible();
    expect(screen.getByTestId('location-search')).toHaveTextContent(
      '?hold_page=999&hold_page_size=20',
    );
  });

  it('keeps the pager visible for an empty filtered collection', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/legal-holds?state=released&page=1&page_size=20',
        body: page([], { page: '1', page_size: 20, total_items: '0', total_pages: '1' }),
      },
    ]);

    await renderWithProviders(<LegalHoldPanel />, {
      station: 'admin',
      role: 'admin',
      route: '/settings?hold_state=released',
    });

    expect(await screen.findByText('No legal-hold metadata')).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'Items per page' })).toBeVisible();
    expect(screen.getByText('Page 1 of 1 · Total: 0')).toBeVisible();
  });

  it('restores the previous filter and page context on browser back', async () => {
    const active = hold({ object_ref: REPORT_ID });
    const released = hold({
      state: 'released',
      object_ref: OTHER_REPORT_ID,
      ended_at: 1_800_000_100,
    });
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/legal-holds?state=active&page=1&page_size=20',
        body: page([active]),
      },
      {
        method: 'GET',
        path: '/admin/api/legal-holds?state=released&page=1&page_size=20',
        body: page([released]),
      },
    ]);

    const rendered = await renderWithProviders(
      <>
        <LegalHoldPanel />
        <BackProbe />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/settings?hold_state=active&hold_page=1&hold_page_size=20',
      },
    );

    expect(await screen.findByText(REPORT_ID)).toBeVisible();
    await rendered.user.selectOptions(screen.getByRole('combobox', { name: 'State' }), 'released');
    expect(await screen.findByText(OTHER_REPORT_ID)).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Back' }));
    expect(await screen.findByText(REPORT_ID)).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'State' })).toHaveValue('active');
    expect(screen.getByTestId('location-search')).toHaveTextContent(
      '?hold_state=active&hold_page=1&hold_page_size=20',
    );
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      '/admin/api/legal-holds?state=active&page=1&page_size=20',
    );
  });

  it('stores detail selection in the URL and removes it when detail is closed', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/legal-holds?object_kind=report_case&page=1&page_size=20',
        body: page([hold()]),
      },
      {
        method: 'GET',
        path: `/admin/api/legal-holds/${HOLD_ID}`,
        body: detail(),
      },
    ]);

    const rendered = await renderWithProviders(
      <>
        <LegalHoldPanel />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/settings?hold_kind=report_case&hold_page=1&hold_page_size=20',
      },
    );

    await rendered.user.click(await screen.findByRole('button', { name: 'Metadata' }));
    expect(await screen.findByRole('heading', { name: 'Hold metadata' })).toBeVisible();
    expect(screen.getByTestId('location-search')).toHaveTextContent(
      `?hold_kind=report_case&hold_page=1&hold_page_size=20&hold_id=${HOLD_ID}`,
    );
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      `/admin/api/legal-holds/${HOLD_ID}`,
    );

    await rendered.user.click(screen.getByRole('button', { name: 'Close details' }));
    await waitFor(() =>
      expect(screen.queryByRole('heading', { name: 'Hold metadata' })).not.toBeInTheDocument(),
    );
    expect(screen.getByTestId('location-search')).toHaveTextContent(
      '?hold_kind=report_case&hold_page=1&hold_page_size=20',
    );
  });

  it('clears a stale detail selection after the server returns 404', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/legal-holds?page=1&page_size=20',
        body: page([]),
      },
      {
        method: 'GET',
        path: `/admin/api/legal-holds/${HOLD_ID}`,
        status: 404,
        body: { error: { code: 'not_found', message: 'hold removed' } },
      },
    ]);

    await renderWithProviders(
      <>
        <LegalHoldPanel />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: `/settings?hold_page=1&hold_page_size=20&hold_id=${HOLD_ID}`,
      },
    );

    await waitFor(() =>
      expect(screen.getByTestId('location-search')).toHaveTextContent(
        '?hold_page=1&hold_page_size=20',
      ),
    );
    expect(screen.queryByRole('heading', { name: 'Hold metadata' })).not.toBeInTheDocument();
  });

  it('rejects a detail response for a different hold and closes its write surface', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/legal-holds?page=1&page_size=20',
        body: page([hold()]),
      },
      {
        method: 'GET',
        path: `/admin/api/legal-holds/${HOLD_ID}`,
        body: detail({ id: OTHER_HOLD_ID }),
      },
    ]);

    const rendered = await renderWithProviders(
      <>
        <LegalHoldPanel />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: `/settings?hold_page=1&hold_page_size=20&hold_id=${HOLD_ID}`,
      },
    );

    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
        `/admin/api/legal-holds/${HOLD_ID}`,
      ),
    );
    await waitFor(() =>
      expect(screen.getByTestId('location-search')).toHaveTextContent(
        '?hold_page=1&hold_page_size=20',
      ),
    );
    expect(
      rendered.queryClient.getQueryState([
        'admin',
        'operations',
        'legal-hold',
        'admin:root',
        HOLD_ID,
      ])?.error,
    ).toMatchObject({ code: 'invalid_response' });
    expect(screen.queryByRole('heading', { name: 'Hold metadata' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Release reason')).not.toBeInTheDocument();
    expect(
      screen.queryByLabelText('Administrator password (fresh elevation)'),
    ).not.toBeInTheDocument();
  });

  it('does not request an invalid hold ID and removes it from the URL', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      { method: 'GET', path: '/admin/api/legal-holds?page=1&page_size=20', body: page([]) },
    ]);

    await renderWithProviders(
      <>
        <LegalHoldPanel />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/settings?hold_page=1&hold_page_size=20&hold_id=lgh_bad',
      },
    );

    expect(await screen.findByText('No legal-hold metadata')).toBeVisible();
    await waitFor(() =>
      expect(screen.getByTestId('location-search')).toHaveTextContent(
        '?hold_page=1&hold_page_size=20',
      ),
    );
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual([
      '/admin/api/session',
      '/admin/api/legal-holds?page=1&page_size=20',
    ]);
  });

  it('closes the private surface after a list authorization failure', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: session() },
      {
        method: 'GET',
        path: '/admin/api/legal-holds?page=1&page_size=20',
        status: 403,
        body: { error: { code: 'forbidden', message: 'denied' } },
      },
    ]);

    await renderWithProviders(<LegalHoldPanel />, {
      station: 'admin',
      role: 'admin',
      route: '/settings',
    });

    await waitFor(() =>
      expect(screen.getByText('This action is not allowed for the current account.')).toBeVisible(),
    );
    expect(screen.queryByRole('button', { name: 'Metadata' })).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual([
      '/admin/api/session',
      '/admin/api/legal-holds?page=1&page_size=20',
    ]);
  });

  it('clears the old page when a delayed page request loses authority', async () => {
    const delayedPage = deferred<Response>();
    const firstPage = Array.from({ length: 20 }, (_, index) =>
      hold({
        id: index === 0 ? HOLD_ID : `lgh_${String(index).padStart(21, '0')}A`,
        object_ref: index === 0 ? REPORT_ID : `rpc_${String(index).padStart(21, '0')}A`,
      }),
    );
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (target.pathname === '/admin/api/session') return Promise.resolve(jsonResponse(session()));
      if (target.pathname === '/admin/api/legal-holds' && target.searchParams.get('page') === '1') {
        return Promise.resolve(
          jsonResponse(
            page(firstPage, { page: '1', page_size: 20, total_items: '21', total_pages: '2' }),
          ),
        );
      }
      if (target.pathname === '/admin/api/legal-holds' && target.searchParams.get('page') === '2') {
        return delayedPage.promise;
      }
      throw new Error(`Unexpected request: ${target.pathname}${target.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderWithProviders(<LegalHoldPanel />, {
      station: 'admin',
      role: 'admin',
      route: '/settings?hold_page=1&hold_page_size=20',
    });

    expect(await screen.findByText(REPORT_ID)).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Next' }));
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(([input]) => String(input).includes('page=2&page_size=20')),
      ).toBe(true),
    );
    expect(screen.getByText(REPORT_ID)).toBeVisible();
    expect(screen.getAllByRole('button', { name: 'Metadata' })).toHaveLength(20);
    expect(
      screen
        .getAllByRole('button', { name: 'Metadata' })
        .every((button) => button.hasAttribute('disabled')),
    ).toBe(true);

    await act(async () => {
      delayedPage.resolve(
        jsonResponse({ error: { code: 'unauthorized', message: 'session ended' } }, 401),
      );
      await delayedPage.promise;
    });

    await waitFor(() =>
      expect(screen.getByRole('heading', { name: 'Sign-in required' })).toBeVisible(),
    );
    expect(screen.queryByText(REPORT_ID)).not.toBeInTheDocument();
  });

  it('does not let a delayed old-account response replace the current account page', async () => {
    const oldPage = deferred<Response>();
    let currentAccount = 'A';
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (target.pathname === '/admin/api/session') {
        return Promise.resolve(jsonResponse(session('A')));
      }
      if (target.pathname === '/admin/api/legal-holds') {
        return currentAccount === 'A'
          ? oldPage.promise
          : Promise.resolve(jsonResponse(page([hold({ object_ref: OTHER_REPORT_ID })])));
      }
      throw new Error(`Unexpected request: ${target.pathname}${target.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<LegalHoldPanel />, {
      station: 'admin',
      role: 'admin',
      route: '/settings?hold_page=1&hold_page_size=20',
    });

    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(([input]) => String(input).includes('/admin/api/legal-holds')),
      ).toBe(true),
    );
    currentAccount = 'B';
    rendered.queryClient.setQueryData(['admin', 'session'], session('B'));
    expect(await screen.findByText(OTHER_REPORT_ID)).toBeVisible();

    await act(async () => {
      oldPage.resolve(jsonResponse(page([hold({ object_ref: REPORT_ID })])));
      await oldPage.promise;
    });
    await waitFor(() => expect(screen.queryByText(REPORT_ID)).not.toBeInTheDocument());
    expect(screen.getByText(OTHER_REPORT_ID)).toBeVisible();
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      '/admin/api/legal-holds?page=1&page_size=20',
    );
  });

  it.each([401, 403] as const)(
    'clears old rows after release loses authority with %s',
    async (status) => {
      const fetchMock = installJsonFetchFixtures([
        { method: 'GET', path: '/admin/api/session', body: session() },
        {
          method: 'GET',
          path: '/admin/api/legal-holds?page=1&page_size=20',
          body: page([hold()]),
        },
        { method: 'GET', path: `/admin/api/legal-holds/${HOLD_ID}`, body: detail() },
        {
          method: 'POST',
          path: '/admin/api/auth/elevate',
          body: { token: 'elevated-token-12345678901234567890', expires_at: 1_800_000_300 },
        },
        {
          method: 'POST',
          path: `/admin/api/legal-holds/${HOLD_ID}/release`,
          status,
          body: {
            error: {
              code: status === 401 ? 'unauthorized' : 'forbidden',
              message: 'release denied',
            },
          },
        },
      ]);

      const rendered = await renderWithProviders(<LegalHoldPanel />, {
        station: 'admin',
        role: 'admin',
        route: `/settings?hold_page=1&hold_page_size=20&hold_id=${HOLD_ID}`,
      });

      expect(await screen.findByRole('heading', { name: 'Hold metadata' })).toBeVisible();
      await rendered.user.type(screen.getByLabelText('Release reason'), 'retire');
      await rendered.user.type(
        screen.getByLabelText('Administrator password (fresh elevation)'),
        'correct horse battery staple',
      );
      await rendered.user.click(
        screen.getByRole('checkbox', {
          name: 'Release is final; this object cannot receive another hold.',
        }),
      );
      await rendered.user.click(screen.getByRole('button', { name: 'Release hold' }));
      const confirmButtons = screen.getAllByRole('button', { name: 'Release hold' });
      await rendered.user.click(confirmButtons[confirmButtons.length - 1]!);

      expect(
        await screen.findByText(
          status === 401
            ? 'Your session is not active. Sign in to continue.'
            : 'This action is not allowed for the current account.',
        ),
      ).toBeVisible();
      expect(screen.queryByText(REPORT_ID)).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Metadata' })).not.toBeInTheDocument();
      expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual([
        '/admin/api/session',
        '/admin/api/legal-holds?page=1&page_size=20',
        `/admin/api/legal-holds/${HOLD_ID}`,
        '/admin/api/auth/elevate',
        `/admin/api/legal-holds/${HOLD_ID}/release`,
      ]);
    },
  );

  it('refreshes the current page and detail after an elevated release', async () => {
    let released = false;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      const method = init?.method ?? (input instanceof Request ? input.method : 'GET');
      if (target.pathname === '/admin/api/session' && method === 'GET') {
        return jsonResponse(session());
      }
      if (target.pathname === '/admin/api/legal-holds' && method === 'GET') {
        return jsonResponse(
          page(
            [hold(released ? { state: 'released', revision: '2', ended_at: 1_800_000_200 } : {})],
            { page: '2', page_size: 20, total_items: '21', total_pages: '2' },
          ),
        );
      }
      if (target.pathname === `/admin/api/legal-holds/${HOLD_ID}` && method === 'GET') {
        return jsonResponse(
          detail(
            released
              ? { state: 'released', revision: '2', ended_at: 1_800_000_200, end_reason: 'retire' }
              : {},
          ),
        );
      }
      if (target.pathname === '/admin/api/auth/elevate' && method === 'POST') {
        return jsonResponse({
          token: 'elevated-token-12345678901234567890',
          expires_at: 1_800_000_300,
        });
      }
      if (target.pathname === `/admin/api/legal-holds/${HOLD_ID}/release` && method === 'POST') {
        released = true;
        return jsonResponse(
          detail({
            state: 'released',
            revision: '2',
            ended_at: 1_800_000_200,
            end_reason: 'retire',
          }),
        );
      }
      throw new Error(`Unexpected request: ${method} ${target.pathname}${target.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderWithProviders(
      <>
        <LegalHoldPanel />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: `/settings?hold_kind=report_case&hold_page=2&hold_page_size=20&hold_id=${HOLD_ID}`,
      },
    );

    await screen.findByRole('heading', { name: 'Hold metadata' });
    await rendered.user.type(screen.getByLabelText('Release reason'), 'retire');
    await rendered.user.type(
      screen.getByLabelText('Administrator password (fresh elevation)'),
      'correct horse battery staple',
    );
    await rendered.user.click(
      screen.getByRole('checkbox', {
        name: 'Release is final; this object cannot receive another hold.',
      }),
    );
    await rendered.user.click(screen.getByRole('button', { name: 'Release hold' }));
    const confirmButtons = screen.getAllByRole('button', { name: 'Release hold' });
    await rendered.user.click(confirmButtons[confirmButtons.length - 1]!);

    await waitFor(() => expect(released).toBe(true));
    await waitFor(() => {
      expect(screen.getAllByText('Released').length).toBeGreaterThanOrEqual(1);
    });
    const releaseCall = fetchMock.mock.calls.find(([input, request]) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      return target.pathname.endsWith('/release') && (request?.method ?? 'GET') === 'POST';
    });
    expect(releaseCall).toBeDefined();
    expect(JSON.parse(String(releaseCall?.[1]?.body))).toEqual({
      expected_revision: '1',
      reason: 'retire',
      confirmation: true,
    });
    expect((releaseCall?.[1]?.headers as Headers).get('X-Elevated-Token')).toBe(
      'elevated-token-12345678901234567890',
    );
    expect(screen.getByTestId('location-search')).toHaveTextContent(
      `?hold_kind=report_case&hold_page=2&hold_page_size=20&hold_id=${HOLD_ID}`,
    );
  });
});
