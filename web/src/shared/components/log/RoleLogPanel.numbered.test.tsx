import { renderHistoricalLogs as renderWithProviders } from '../../../../test/unit/logSupport';
import { installNativeDialog } from '../../../../test/unit/nativeDialog';
import { act, screen, waitFor, within } from '@testing-library/react';
import { useLocation, useNavigate } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures, type JsonFetchFixture } from '../../../../test/unit/support';
import { RoleLogPanel } from './RoleLogPanel';

installNativeDialog();

const usage = {
  uncached_input_tokens: '0',
  cache_write_input_tokens: '0',
  cache_read_input_tokens: '0',
  output_tokens: '1',
  total_tokens: '1',
  usage_unknown: false,
  charge: '0',
};

function requestID(index: number): string {
  return `req_${String(index).padStart(21, '0')}Q`;
}

function commonRow(index: number, routeKind = 'openai_chat_completions') {
  return {
    id: requestID(index),
    route_kind: routeKind,
    phase: 'handler' as const,
    rejection_stage: null,
    rejection_reason: null,
    request_method: null,
    request_path: null,
    caller_result_class: 'success',
    caller_status: 200,
    caller_error_code: null,
    started_at: 1,
    completed_at: 2,
    usage,
  };
}

function adminRow(index: number) {
  return {
    ...commonRow(index),
    usage_total_mismatch: false,
    user_id: String(index + 1),
    origin_user_id: String(index + 1),
    origin_discord_id: '100000000000000101',
    origin_deleted: false,
    origin_unknown: false,
    history_record_id: null,
    caller_identity: null,
    attempt_count: '1',
  };
}

function userCharityRow(index: number) {
  return {
    ...commonRow(index, 'charity_chat_completions'),
    model: 'charity-model',
  };
}

function attempt(sequence: number) {
  return {
    attempt_seq: String(sequence),
    result_kind: 'response',
    endpoint_key_id: '2',
    endpoint_base_url: 'https://api.example.com/v1',
    connector_type: 'openai-compatible',
    upstream_model_id: 'upstream-model',
    status_code: 200,
    upstream_code: null,
    diag: null,
    usage,
    usage_total_mismatch: false,
    started_at: 1,
    completed_at: 2,
  };
}

function pagination(page = '1', pageSize = 20, totalItems = 1, totalPages = 1) {
  return {
    page,
    page_size: pageSize,
    total_items: String(totalItems),
    total_pages: String(totalPages),
  };
}

function listBody(data: unknown[], meta: ReturnType<typeof pagination>) {
  return { data, next_cursor: null, pagination: meta };
}

function detailBody(request: Record<string, unknown>, attempts: unknown[], meta = pagination()) {
  return {
    request,
    attempts: { data: attempts, next_cursor: null },
    attempt_pagination: meta,
  };
}

function timeZoneFixture(path: string): JsonFetchFixture {
  return {
    method: 'GET',
    path,
    body: { version: 'go1.26.6-zoneinfo', zones: ['UTC'] },
  };
}

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location">{location.search}</output>;
}

function NavigationProbe() {
  const navigate = useNavigate();
  return (
    <div>
      <button type="button" onClick={() => navigate('/logs?page=1&page_size=20')}>
        Go to first page
      </button>
      <button type="button" onClick={() => navigate(-1)}>
        Back
      </button>
    </div>
  );
}

function queryFromProbe(view: HTMLElement): URLSearchParams {
  return new URLSearchParams(view.querySelector('[data-testid="location"]')?.textContent ?? '');
}

describe('numbered role log panel', () => {
  it.each([
    ['synthetic', null, 'model discovery DNS lookup failed'],
    ['synthetic', 502, 'discovery outcome unavailable after restart'],
    ['response', 200, 'invalid upstream models response'],
  ] as const)(
    'opens %s discovery records without treating acceptance as fetch success',
    async (kind, status, diagnostic) => {
      const row = { ...adminRow(1), route_kind: 'model_discovery', caller_status: 202 };
      installJsonFetchFixtures([
        timeZoneFixture('/admin/api/time-zones'),
        {
          method: 'GET',
          path: '/admin/api/logs?page=1&page_size=20',
          body: listBody([row], pagination()),
        },
        {
          method: 'GET',
          path: '/admin/api/logs/' + row.id + '?attempt_page=1&attempt_page_size=20',
          body: detailBody(row, [
            { ...attempt(1), result_kind: kind, status_code: status, diag: diagnostic },
          ]),
        },
      ]);
      const view = await renderWithProviders(<RoleLogPanel accountId="viewer" role="admin" />, {
        station: 'admin',
        role: 'admin',
        route: '/logs',
      });
      expect(await screen.findByText('Task accepted', { selector: '.nb-badge' })).toBeVisible();
      await view.user.click(await screen.findByRole('button', { name: 'Details' }));
      const drawer = await screen.findByRole('dialog', { name: /Log details/ });
      const diagnosticText = await within(drawer).findByText(diagnostic);
      expect(diagnosticText).not.toBeVisible();
      await view.user.click(within(drawer).getByText('Diagnostic', { selector: 'strong' }));
      expect(diagnosticText).toBeVisible();
      expect(
        screen.getByText('Service call attempts', { selector: 'strong' }).closest('details'),
      ).toHaveAttribute('open');
      expect(screen.getByText(/The model refresh task was accepted \(202\)/)).toBeVisible();
      if (kind === 'synthetic') {
        expect(screen.getByText(/no confirmed upstream HTTP response in the log/)).toBeVisible();
        expect(
          screen.getByText('Upstream HTTP status', { selector: 'dt' }).nextElementSibling,
        ).toHaveTextContent('No HTTP response recorded');
        expect(
          screen.queryByText(/The status below is generated by the platform/),
        ).not.toBeInTheDocument();
      } else {
        expect(
          screen.getByText('Upstream HTTP status', { selector: 'dt' }).nextElementSibling,
        ).toHaveTextContent('200');
      }
    },
  );
  it.each(['bad', '0', '9223372036854775808', '2&endpoint_key_id=3'])(
    'never falls back to all logs for invalid key link %s',
    async (value) => {
      const fetchMock = installJsonFetchFixtures([timeZoneFixture('/admin/api/time-zones')]);
      await renderWithProviders(<RoleLogPanel accountId="viewer" role="admin" />, {
        station: 'admin',
        role: 'admin',
        route: '/logs?endpoint_key_id=' + value,
      });
      expect(await screen.findByRole('alert')).toHaveTextContent(
        'physical key ID filter is invalid',
      );
      expect(fetchMock.mock.calls.every(([url]) => !String(url).includes('/logs?'))).toBe(true);
      expect(screen.queryByRole('link', { name: 'Export CSV' })).not.toBeInTheDocument();
    },
  );
  it('restores a physical key link and marks only matching attempts', async () => {
    const row = { ...adminRow(1), attempt_count: '2' };
    installJsonFetchFixtures([
      timeZoneFixture('/admin/api/time-zones'),
      {
        method: 'GET',
        path: '/admin/api/logs?endpoint_key_id=2&page=1&page_size=20',
        body: listBody([row], pagination()),
      },
      {
        method: 'GET',
        path: `/admin/api/logs/${row.id}?attempt_page=1&attempt_page_size=20`,
        body: detailBody(
          row,
          [attempt(1), { ...attempt(2), endpoint_key_id: '3' }],
          pagination('1', 20, 2),
        ),
      },
    ]);
    const view = await renderWithProviders(
      <>
        <LocationProbe />
        <RoleLogPanel accountId="viewer" role="admin" />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/logs?endpoint_key_id=2',
      },
    );
    await view.user.click(await screen.findByRole('button', { name: 'Details' }));
    await view.user.click(await screen.findByText('Service call attempts', { selector: 'strong' }));
    expect(await screen.findByText('Matched this key')).toBeVisible();
    expect(screen.getAllByText('Matched this key')).toHaveLength(1);
    expect(queryFromProbe(view.container).get('endpoint_key_id')).toBe('2');
  });
  it('waits for a real account and purges content when that session is cleared', async () => {
    const fetchMock = installJsonFetchFixtures([
      timeZoneFixture('/admin/api/time-zones'),
      {
        method: 'GET',
        path: '/admin/api/logs?upstream_model=legacy-test-model&page=1&page_size=20',
        body: listBody([adminRow(7)], pagination()),
      },
    ]);
    const view = await renderWithProviders(
      <>
        <RoleLogPanel role="admin" />
        <LocationProbe />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/logs?upstream_model=legacy-test-model',
      },
    );
    expect(fetchMock).not.toHaveBeenCalled();
    const sessionFetch = vi.fn(async () => ({ admin: { username: 'page-reader' } }));
    await act(async () => {
      await view.queryClient.fetchQuery({
        queryKey: ['admin', 'session'],
        queryFn: sessionFetch,
        staleTime: 0,
      });
    });
    await screen.findByRole('button', { name: 'Details' });
    expect(queryFromProbe(view.container).has('upstream_model')).toBe(false);
    expect(screen.getByLabelText('Model used')).toHaveValue('legacy-test-model');
    await act(async () => {
      await view.queryClient.refetchQueries({
        queryKey: ['admin', 'session'],
        exact: true,
        type: 'all',
      });
    });
    expect(sessionFetch).toHaveBeenCalledTimes(2);
    await act(async () => {
      view.queryClient.setQueryData(['admin', 'session'], null);
    });
    expect(screen.queryByRole('button', { name: 'Details' })).toBeNull();
    expect(
      view.queryClient.getQueriesData({
        queryKey: ['admin', 'operations', 'logs', 'page', 'page-reader'],
      }),
    ).toEqual([]);
  });

  it('does not send malformed restored identifiers or times to the API', async () => {
    const fetchMock = installJsonFetchFixtures([
      timeZoneFixture('/admin/api/time-zones'),
      {
        method: 'GET',
        path: '/admin/api/logs?page=1&page_size=20',
        body: listBody([], pagination('1', 20, 0, 1)),
      },
    ]);
    await renderWithProviders(<RoleLogPanel accountId="viewer" role="admin" />, {
      station: 'admin',
      role: 'admin',
      route:
        '/logs?user_id=9223372036854775808&from=253402300800&to=1e3&status=999&endpoint_base_url=%00',
    });
    await screen.findByText('No log records', { exact: true });
    expect(
      fetchMock.mock.calls
        .map(([path]) => String(path))
        .filter((path) => path.startsWith('/admin/api/logs?')),
    ).toEqual(['/admin/api/logs?page=1&page_size=20']);
  });
  it('restores outer URL state, handles POP, clamps, and returns from detail with actual page', async () => {
    const firstPage = Array.from({ length: 20 }, (_, index) => adminRow(index));
    const clampedRow = adminRow(20);
    const fetchMock = installJsonFetchFixtures([
      timeZoneFixture('/admin/api/time-zones'),
      {
        method: 'GET',
        path: '/admin/api/logs?page=2147483647&page_size=20',
        body: listBody([clampedRow], pagination('2', 20, 21, 2)),
      },
      {
        method: 'GET',
        path: '/admin/api/logs?page=1&page_size=20',
        body: listBody(firstPage, pagination('1', 20, 21, 2)),
      },
      {
        method: 'GET',
        path: `/admin/api/logs/${requestID(20)}?attempt_page=1&attempt_page_size=20`,
        body: detailBody(
          { ...clampedRow, usage: { ...usage, charge: '123.45' } },
          [
            { ...attempt(1), usage: { ...usage, charge: '999.99' } },
            { ...attempt(2), endpoint_key_id: null },
          ],
          pagination('1', 20, 2, 1),
        ),
      },
    ]);

    const view = await renderWithProviders(
      <>
        <LocationProbe />
        <NavigationProbe />
        <RoleLogPanel accountId="viewer" role="admin" />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/logs?page=2147483647&page_size=20&anchor=keep',
      },
    );

    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
        '/admin/api/logs?page=2147483647&page_size=20',
      ),
    );
    await waitFor(() =>
      expect(screen.getByText('That page is no longer available. Showing page 2.')).toBeVisible(),
    );
    expect(screen.getByText('21 items')).toBeVisible();
    const listResult = screen.getByText('21 items').closest('.ops-stack');
    expect(listResult?.getAttribute('aria-busy')).toBe('false');
    expect(
      within(screen.getByRole('navigation', { name: 'Pagination' })).getByRole('combobox'),
    ).toHaveValue('20');

    await view.user.click(screen.getByRole('button', { name: 'Go to first page' }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
        '/admin/api/logs?page=1&page_size=20',
      ),
    );
    await view.user.click(screen.getByRole('button', { name: 'Back' }));
    await waitFor(() => expect(queryFromProbe(view.container).get('page')).toBe('2147483647'));
    expect(await screen.findByText('#21', { exact: true })).toBeVisible();

    await view.user.click(screen.getByRole('button', { name: 'Details' }));
    const dialog = await screen.findByRole('dialog', { name: /Log details/ });
    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
        `/admin/api/logs/${requestID(20)}?attempt_page=1&attempt_page_size=20`,
      ),
    );
    await view.user.click(
      await within(dialog).findByText('Service call attempts', { selector: 'strong' }),
    );
    await waitFor(() =>
      expect(within(dialog).getAllByText('upstream-model', { exact: true })).toHaveLength(2),
    );
    expect(within(dialog).getByText('Charge (credits)').parentElement).toHaveTextContent('123.45');
    expect(dialog).not.toHaveTextContent('999.99');
    const attemptItems = Array.from(dialog.querySelectorAll('.log-attempt')) as HTMLElement[];
    expect(attemptItems).toHaveLength(2);
    expect(within(attemptItems[0]!).getByText('2', { exact: true })).toBeVisible();
    expect(
      within(attemptItems[1]!).getByText('Endpoint key ID', { exact: true }).parentElement,
    ).toHaveTextContent('—');
    const detailQuery = queryFromProbe(view.container);
    expect(detailQuery.get('request_id')).toBe(requestID(20));
    expect(detailQuery.get('page')).toBe('2');
    expect(detailQuery.get('page_size')).toBe('20');
    expect(detailQuery.get('anchor')).toBe('keep');
    expect(detailQuery.has('attempt_page')).toBe(false);
    expect(within(dialog).getByRole('navigation', { name: 'Pagination' })).toBeVisible();
    expect(dialog.querySelector('[aria-busy]')?.getAttribute('aria-busy')).toBe('false');

    await view.user.click(within(dialog).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('region', { name: /Log details/ })).toBeNull());
    const returnedQuery = queryFromProbe(view.container);
    expect(returnedQuery.get('request_id')).toBeNull();
    expect(returnedQuery.get('attempt_page')).toBeNull();
    expect(returnedQuery.get('attempt_page_size')).toBeNull();
    expect(returnedQuery.get('page')).toBe('2');
    expect(returnedQuery.get('page_size')).toBe('20');
    expect(returnedQuery.get('anchor')).toBe('keep');
  });

  it('resets the outer page when a committed filter changes', async () => {
    const row = adminRow(7);
    const fetchMock = installJsonFetchFixtures([
      timeZoneFixture('/admin/api/time-zones'),
      {
        method: 'GET',
        path: '/admin/api/logs?page=4&page_size=20',
        body: listBody([row], pagination('2', 20, 21, 2)),
      },
      {
        method: 'GET',
        path: '/admin/api/logs?user_id=7&page=1&page_size=20',
        body: listBody([row], pagination('1', 20, 1, 1)),
      },
    ]);
    const view = await renderWithProviders(
      <>
        <LocationProbe />
        <RoleLogPanel accountId="viewer" role="admin" />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/logs?page=4&page_size=20&anchor=keep',
      },
    );

    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
        '/admin/api/logs?page=4&page_size=20',
      ),
    );
    await view.user.type(screen.getByLabelText('User ID'), '7');
    await view.user.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
        '/admin/api/logs?user_id=7&page=1&page_size=20',
      ),
    );
    const query = queryFromProbe(view.container);
    expect(query.get('page')).toBe('1');
    expect(query.get('user_id')).toBe('7');
    expect(query.get('anchor')).toBe('keep');
  });

  it('persists the management mismatch filter, resets the page, and omits it for all', async () => {
    const row = adminRow(7);
    const mismatchRow = { ...row, usage_total_mismatch: true };
    const fetchMock = installJsonFetchFixtures([
      timeZoneFixture('/admin/api/time-zones'),
      {
        method: 'GET',
        path: '/admin/api/logs?page=4&page_size=20',
        body: listBody([row], pagination('4', 20, 61, 4)),
      },
      {
        method: 'GET',
        path: '/admin/api/logs?usage_total_mismatch=true&page=1&page_size=20',
        body: listBody([mismatchRow], pagination()),
      },
      {
        method: 'GET',
        path: '/admin/api/logs?page=1&page_size=20',
        body: listBody([row], pagination()),
      },
    ]);
    const view = await renderWithProviders(
      <>
        <LocationProbe />
        <RoleLogPanel accountId="viewer" role="admin" />
      </>,
      {
        station: 'admin',
        role: 'admin',
        route: '/logs?page=4&page_size=20&anchor=keep',
      },
    );

    const mismatchFilter = screen.getByRole('combobox', { name: 'Usage total' });
    expect(mismatchFilter).toHaveValue('');
    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
        '/admin/api/logs?page=4&page_size=20',
      ),
    );
    expect(
      fetchMock.mock.calls
        .map(([path]) => String(path))
        .some((path) => path.includes('usage_total_mismatch')),
    ).toBe(false);

    await view.user.selectOptions(mismatchFilter, 'true');
    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
        '/admin/api/logs?usage_total_mismatch=true&page=1&page_size=20',
      ),
    );
    expect(queryFromProbe(view.container).get('usage_total_mismatch')).toBe('true');
    expect(queryFromProbe(view.container).get('page')).toBe('1');
    expect(queryFromProbe(view.container).get('anchor')).toBe('keep');

    await view.user.selectOptions(mismatchFilter, '');
    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
        '/admin/api/logs?page=1&page_size=20',
      ),
    );
    expect(queryFromProbe(view.container).has('usage_total_mismatch')).toBe(false);
  });

  it('shows mismatch badges only in management logs and attempts', async () => {
    const row = {
      ...adminRow(8),
      usage_total_mismatch: true,
      usage: { ...usage, usage_unknown: true },
    };
    const mismatchAttempt = {
      ...attempt(1),
      usage_total_mismatch: true,
      usage: { ...usage, usage_unknown: true },
    };
    installJsonFetchFixtures([
      timeZoneFixture('/admin/api/time-zones'),
      {
        method: 'GET',
        path: '/admin/api/logs?page=1&page_size=20',
        body: listBody([row], pagination()),
      },
      {
        method: 'GET',
        path: `/admin/api/logs/${row.id}?attempt_page=1&attempt_page_size=20`,
        body: detailBody(row, [mismatchAttempt]),
      },
    ]);
    const view = await renderWithProviders(<RoleLogPanel accountId="viewer" role="admin" />, {
      station: 'admin',
      role: 'admin',
      route: '/logs?page=1&page_size=20',
    });
    await view.user.click(await screen.findByRole('button', { name: 'Details' }));
    const drawer = await screen.findByRole('dialog', { name: /Log details/ });
    await view.user.click(
      within(drawer).getByText('Service call attempts', { selector: 'strong' }),
    );
    expect(await within(drawer).findAllByText('Usage total mismatch')).toHaveLength(2);
    const explanation =
      'Usage total mismatch. The upstream-reported total differs from the sum of valid usage components. Billing uses those components.';
    await waitFor(() => expect(screen.getAllByLabelText(explanation)).toHaveLength(2));
  });

  it('does not expose the management mismatch filter or badge to ordinary users', async () => {
    const row = userCharityRow(31);
    const fetchMock = installJsonFetchFixtures([
      timeZoneFixture('/api/time-zones'),
      {
        method: 'GET',
        path: '/api/logs?page=1&page_size=20',
        body: listBody([row], pagination()),
      },
    ]);
    await renderWithProviders(<RoleLogPanel accountId="viewer" role="user" />, {
      station: 'user',
      role: 'user',
      route: '/logs?page=1&page_size=20',
    });
    await screen.findByRole('button', { name: 'Details' });
    expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
      '/api/logs?page=1&page_size=20',
    );
    expect(screen.queryByRole('combobox', { name: 'Usage total' })).toBeNull();
    expect(screen.queryByText('Usage total mismatch')).toBeNull();
  });

  it('submits the user filter from the steward management station', async () => {
    const row = adminRow(7);
    const fetchMock = installJsonFetchFixtures([
      timeZoneFixture('/api/time-zones'),
      {
        method: 'GET',
        path: '/api/steward/logs?page=1&page_size=20',
        body: listBody([row], pagination('1', 20, 1, 1)),
      },
      {
        method: 'GET',
        path: '/api/steward/logs?user_id=8&page=1&page_size=20',
        body: listBody([row], pagination('1', 20, 1, 1)),
      },
    ]);
    const view = await renderWithProviders(<RoleLogPanel accountId="viewer" role="steward" />, {
      station: 'user',
      role: 'level5',
      route: '/logs?page=1&page_size=20',
    });

    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
        '/api/steward/logs?page=1&page_size=20',
      ),
    );
    await view.user.type(screen.getByLabelText('User ID'), '8');
    await view.user.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
        '/api/steward/logs?user_id=8&page=1&page_size=20',
      ),
    );
  });

  it('keeps charity details without attempts or a fabricated count', async () => {
    const row = userCharityRow(30);
    const detailPath = `/api/logs/${requestID(30)}?attempt_page=1&attempt_page_size=20`;
    const fetchMock = installJsonFetchFixtures([
      timeZoneFixture('/api/time-zones'),
      {
        method: 'GET',
        path: '/api/logs?page=1&page_size=20',
        body: listBody([row], pagination()),
      },
      {
        method: 'GET',
        path: detailPath,
        body: { request: row, caller_safe_result: { class: 'success' } },
      },
    ]);
    const view = await renderWithProviders(<RoleLogPanel accountId="viewer" role="user" />, {
      station: 'user',
      role: 'user',
      route: '/logs?page=1&page_size=20',
    });

    await waitFor(() => expect(screen.getByRole('button', { name: 'Details' })).toBeVisible());
    await view.user.click(screen.getByRole('button', { name: 'Details' }));
    const dialog = await screen.findByRole('dialog', { name: /Log details/ });
    await waitFor(() =>
      expect(within(dialog).getAllByText('Success', { exact: true })[0]).toBeVisible(),
    );
    expect(within(dialog).queryByText('Service call attempts', { exact: true })).toBeNull();
    expect(within(dialog).queryByRole('navigation', { name: 'Pagination' })).toBeNull();
    expect(
      fetchMock.mock.calls.map(([path]) => String(path)).filter((path) => path === detailPath),
    ).toHaveLength(1);
    expect(
      fetchMock.mock.calls.map(([path]) => String(path)).some((path) => path.includes('/attempts')),
    ).toBe(false);
  });

  it('hides an empty steward pager and marks the result region busy state', async () => {
    const fetchMock = installJsonFetchFixtures([
      timeZoneFixture('/api/time-zones'),
      {
        method: 'GET',
        path: '/api/steward/logs?page=9&page_size=100',
        body: listBody([], pagination('1', 100, 0, 1)),
      },
    ]);
    await renderWithProviders(<RoleLogPanel accountId="viewer" role="steward" />, {
      station: 'user',
      role: 'level5',
      route: '/logs?page=9&page_size=100',
    });

    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
        '/api/steward/logs?page=9&page_size=100',
      ),
    );
    await waitFor(() => expect(screen.getByText('No logs', { exact: true })).toBeVisible());
    expect(screen.queryByRole('navigation', { name: 'Pagination' })).toBeNull();
    const emptyResult = screen.getByText('No logs', { exact: true }).closest('.ops-stack');
    expect(emptyResult?.getAttribute('aria-busy')).toBe('false');
  });
});

it('retries an identical requested log page after a transient failure without claiming it was clamped', async () => {
  const next: JsonFetchFixture = {
    method: 'GET',
    path: '/admin/api/logs?page=2&page_size=20',
    status: 503,
    body: { error: { code: 'service_unavailable', message: 'The query timed out.' } },
  };
  const fetchMock = installJsonFetchFixtures([
    timeZoneFixture('/admin/api/time-zones'),
    {
      method: 'GET',
      path: '/admin/api/logs?page=1&page_size=20',
      body: listBody([adminRow(1)], pagination('1', 20, 21, 2)),
    },
    next,
  ]);
  const view = await renderWithProviders(<RoleLogPanel accountId="viewer" role="admin" />, {
    station: 'admin',
    role: 'admin',
    route: '/logs',
  });
  await screen.findByText('#2', { exact: true });
  await view.user.click(screen.getByRole('button', { name: 'Next' }));
  await screen.findByText('The query timed out.');
  expect(screen.getByText('#2', { exact: true })).toBeVisible();
  expect(screen.queryByText('That page is no longer available. Showing page 1.')).toBeNull();
  next.status = 200;
  next.body = listBody([adminRow(2)], pagination('2', 20, 21, 2));
  await view.user.click(screen.getByRole('button', { name: 'Next' }));
  await screen.findByText('#3', { exact: true });
  expect(
    fetchMock.mock.calls.filter(([path]) => String(path).includes('/logs?page=2')),
  ).toHaveLength(2);
});

const runningSearch = (search_id: string) => ({ status: 'running', search_id });

function logSearchFixtures(fixtures: JsonFetchFixture[]) {
  const fetchMock = installJsonFetchFixtures([
    timeZoneFixture('/admin/api/time-zones'),
    ...fixtures,
  ]);
  const resolveFixture = fetchMock.getMockImplementation()!;
  fetchMock.mockImplementation((input, init?: RequestInit) =>
    init?.method === 'DELETE'
      ? Promise.resolve(new Response(null, { status: 204 }))
      : resolveFixture(input, init),
  );
  return {
    fetchMock,
    calls: (prefix: string, method = 'GET') =>
      fetchMock.mock.calls
        .filter(
          ([path, options]) =>
            String(path).startsWith(prefix) && (options.method ?? 'GET') === method,
        )
        .map(([path]) => String(path)),
  };
}

it('reuses completed search results when changing page windows and refreshes from the list', async () => {
  const first = {
    method: 'GET',
    path: '/admin/api/logs?page=1&page_size=20',
    status: 202,
    body: runningSearch('large-search'),
  };
  const searchPath = '/admin/api/log-searches/large-search';
  const firstPoll: JsonFetchFixture = {
    method: 'GET',
    path: searchPath + '?page=1&page_size=20',
    status: 202,
    body: runningSearch('large-search'),
  };
  const { fetchMock, calls } = logSearchFixtures([
    first,
    firstPoll,
    {
      method: 'GET',
      path: searchPath + '?page=2&page_size=20',
      body: listBody([adminRow(2)], pagination('2', 20, 401, 21)),
    },
    {
      method: 'GET',
      path: searchPath + '?page=1&page_size=50',
      body: listBody([adminRow(3)], pagination('1', 50, 401, 9)),
    },
    {
      method: 'GET',
      path: searchPath + '?page=6&page_size=50',
      body: listBody([adminRow(4)], pagination('6', 50, 401, 9)),
    },
    {
      method: 'GET',
      path: '/admin/api/logs?page=6&page_size=50',
      body: listBody([adminRow(5)], pagination('6', 50, 442, 9)),
    },
  ]);

  const view = await renderWithProviders(<RoleLogPanel accountId="viewer" role="admin" />, {
    station: 'admin',
    role: 'admin',
    route: '/logs',
  });
  await screen.findByText('Digging through the logs…');
  await waitFor(() => expect(calls(firstPoll.path)).toHaveLength(1));
  expect(screen.getByRole('button', { name: 'Cancel search' })).toBeVisible();
  firstPoll.status = 200;
  firstPoll.body = listBody([adminRow(1)], pagination('1', 20, 401, 21));
  expect(await screen.findByText('Search complete', {}, { timeout: 4000 })).toBeVisible();
  expect(screen.getByText('401 items')).toBeVisible();
  expect(
    new Headers(fetchMock.mock.calls.find(([path]) => path === first.path)![1].headers).get(
      'Prefer',
    ),
  ).toBe('respond-async');

  await view.user.click(screen.getByRole('button', { name: 'Next' }));
  await screen.findByText('#3', { exact: true });
  await view.user.selectOptions(screen.getByRole('combobox', { name: 'Items per page' }), '50');
  await screen.findByText('#4', { exact: true });
  const jump = screen.getByRole('textbox', { name: 'Go to page' });
  await view.user.clear(jump);
  await view.user.type(jump, '6{Enter}');
  await screen.findByText('#5', { exact: true });
  expect(calls('/admin/api/logs?')).toEqual([first.path]);
  expect(calls(searchPath)).toEqual([
    firstPoll.path,
    firstPoll.path,
    searchPath + '?page=2&page_size=20',
    searchPath + '?page=1&page_size=50',
    searchPath + '?page=6&page_size=50',
  ]);

  await view.user.click(screen.getByRole('button', { name: 'Refresh' }));
  await screen.findByText('442 items');
  expect(calls(searchPath, 'DELETE')).toEqual([searchPath]);
  expect(calls('/admin/api/logs?')).toEqual([first.path, '/admin/api/logs?page=6&page_size=50']);
});

it.each([
  [404, 'not_found'],
  [409, 'refresh_required'],
])('restarts a search with the same filters and window after HTTP %i', async (status, code) => {
  const first: JsonFetchFixture = {
    method: 'GET',
    path: '/admin/api/logs?status=500&page=4&page_size=50',
    status: 202,
    body: runningSearch('old-search'),
  };
  const old = '/admin/api/log-searches/old-search?page=4&page_size=50';
  const { fetchMock, calls } = logSearchFixtures([
    first,
    { method: 'GET', path: old, status, body: { error: { code, message: 'Search changed.' } } },
  ]);
  const resolveFixture = fetchMock.getMockImplementation()!;
  fetchMock.mockImplementation((input, init?: RequestInit) => {
    if (input === old) {
      first.status = 200;
      first.body = listBody([], pagination('1', 50, 0, 1));
    }
    return resolveFixture(input, init);
  });
  await renderWithProviders(<RoleLogPanel accountId="viewer" role="admin" />, {
    station: 'admin',
    role: 'admin',
    route: '/logs?status=500&page=4&page_size=50',
  });
  expect(await screen.findByText('Search complete')).toBeVisible();
  expect(screen.getByText('No log records', { exact: true })).toBeVisible();
  expect(calls('/admin/api/logs?')).toEqual([first.path, first.path]);
  expect(calls(old)).toEqual([old]);
});

it('stops checking a released search when filters change and discards its late page', async () => {
  const old = '/admin/api/log-searches/previous';
  let sendLatePage!: (response: Response) => void;
  const latePage = new Promise<Response>((resolve) => {
    sendLatePage = resolve;
  });
  const { fetchMock, calls } = logSearchFixtures([
    {
      method: 'GET',
      path: '/admin/api/logs?page=1&page_size=20',
      status: 202,
      body: runningSearch('previous'),
    },
    {
      method: 'GET',
      path: '/admin/api/logs?outcome=failed&page=1&page_size=20',
      body: listBody([], pagination('1', 20, 0, 1)),
    },
  ]);
  const resolveFixture = fetchMock.getMockImplementation()!;
  fetchMock.mockImplementation((input, init?: RequestInit) =>
    String(input).startsWith(old + '?') ? latePage : resolveFixture(input, init),
  );

  const view = await renderWithProviders(<RoleLogPanel accountId="viewer" role="admin" />, {
    station: 'admin',
    role: 'admin',
    route: '/logs',
  });
  expect(await screen.findByText('Digging through the logs…')).toBeVisible();
  await waitFor(() => expect(calls(old)).toHaveLength(1));
  await view.user.selectOptions(
    screen.getByRole('combobox', { name: 'Request outcome' }),
    'failed',
  );
  expect(await screen.findByText('No log records', { exact: true })).toBeVisible();
  expect(calls(old, 'DELETE')).toEqual([old]);
  await act(async () => {
    sendLatePage(
      new Response(JSON.stringify(listBody([adminRow(9)], pagination())), {
        headers: { 'Content-Type': 'application/json' },
      }),
    );
  });
  expect(screen.queryByText('#10', { exact: true })).toBeNull();
  expect(calls(old)).toHaveLength(1);
});

it('waits for an explicit retry when the replacement search also disappears', async () => {
  const path = '/admin/api/logs?page=1&page_size=20';
  const receipt: JsonFetchFixture = {
    method: 'GET',
    path,
    status: 202,
    body: runningSearch('evicted-search'),
  };
  const { calls } = logSearchFixtures([
    receipt,
    {
      method: 'GET',
      path: '/admin/api/log-searches/evicted-search?page=1&page_size=20',
      status: 404,
      body: { error: { code: 'not_found', message: 'Search missing.' } },
    },
  ]);
  const view = await renderWithProviders(<RoleLogPanel accountId="viewer" role="admin" />, {
    station: 'admin',
    role: 'admin',
    route: '/logs',
  });
  const retry = await screen.findByRole('button', { name: 'Retry' });
  expect(calls(path)).toEqual([path, path]);
  expect(screen.queryByText('No log records', { exact: true })).toBeNull();
  receipt.status = 200;
  receipt.body = listBody([], pagination('1', 20, 0, 1));
  await view.user.click(retry);
  expect(await screen.findByText('No log records', { exact: true })).toBeVisible();
  expect(calls(path)).toEqual([path, path, path]);
});

it('confirms a known cancellation only after DELETE succeeds and can retry a failed attempt', async () => {
  const path = '/admin/api/log-searches/pending-cancel';
  const poll = path + '?page=1&page_size=20';
  let replyToCancel!: (response: Response) => void;
  const firstCancel = new Promise<Response>((resolve) => {
    replyToCancel = resolve;
  });
  const { fetchMock, calls } = logSearchFixtures([
    {
      method: 'GET',
      path: '/admin/api/logs?page=1&page_size=20',
      status: 202,
      body: runningSearch('pending-cancel'),
    },
    { method: 'GET', path: poll, status: 202, body: runningSearch('pending-cancel') },
  ]);
  const resolveFixture = fetchMock.getMockImplementation()!;
  let cancelAttempts = 0;
  fetchMock.mockImplementation((input, init?: RequestInit) =>
    init?.method === 'DELETE' && ++cancelAttempts === 1 ? firstCancel : resolveFixture(input, init),
  );
  const view = await renderWithProviders(<RoleLogPanel accountId="viewer" role="admin" />, {
    station: 'admin',
    role: 'admin',
    route: '/logs',
  });
  await waitFor(() => expect(calls(poll)).toHaveLength(1));
  await view.user.click(await screen.findByRole('button', { name: 'Cancel search' }));
  expect(screen.getByText('Canceling search…')).toBeVisible();
  expect(screen.queryByText('Search canceled')).toBeNull();
  await act(async () => {
    replyToCancel(new Response(null, { status: 503 }));
  });
  expect(await screen.findByText('Canceling the search failed.')).toBeVisible();
  expect(screen.queryByText('Search canceled')).toBeNull();
  await view.user.click(screen.getByRole('button', { name: 'Retry cancel' }));
  expect(await screen.findByText('Search canceled')).toBeVisible();
  expect(calls(path, 'DELETE')).toEqual([path, path]);
  expect(calls(poll)).toHaveLength(1);
});
