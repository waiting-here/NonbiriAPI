import { act, screen, waitFor, within } from '@testing-library/react';
import { useLocation, useNavigate } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import {
  installJsonFetchFixtures,
  renderWithProviders,
  type JsonFetchFixture,
} from '../../../../test/unit/support';
import { RoleLogPanel } from './RoleLogPanel';

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
    user_id: String(index + 1),
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
  it('waits for a real account and purges content when that session is cleared', async () => {
    const fetchMock = installJsonFetchFixtures([
      timeZoneFixture('/admin/api/time-zones'),
      {
        method: 'GET',
        path: '/admin/api/logs?page=1&page_size=20',
        body: listBody([adminRow(7)], pagination()),
      },
    ]);
    const view = await renderWithProviders(<RoleLogPanel role="admin" />, {
      station: 'admin',
      role: 'admin',
    });
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
        .filter((path) => path.startsWith('/admin/api/logs')),
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
        body: detailBody(clampedRow, [attempt(1)]),
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
    expect(screen.getByText('Page 2 of 2 · 21 items')).toBeVisible();
    const listResult = screen.getByText('Page 2 of 2 · 21 items').closest('.ops-stack');
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
    expect(screen.getByText('21', { exact: true })).toBeVisible();

    await view.user.click(screen.getByRole('button', { name: 'Details' }));
    const dialog = await screen.findByRole('dialog');
    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
        `/admin/api/logs/${requestID(20)}?attempt_page=1&attempt_page_size=20`,
      ),
    );
    await waitFor(() => expect(within(dialog).getByText('upstream-model')).toBeVisible());
    const detailQuery = queryFromProbe(view.container);
    expect(detailQuery.get('request_id')).toBe(requestID(20));
    expect(detailQuery.get('page')).toBe('2');
    expect(detailQuery.get('page_size')).toBe('20');
    expect(detailQuery.get('anchor')).toBe('keep');
    expect(detailQuery.has('attempt_page')).toBe(false);
    expect(within(dialog).getByRole('navigation', { name: 'Pagination' })).toBeVisible();
    expect(dialog.querySelector('[aria-busy]')?.getAttribute('aria-busy')).toBe('false');

    await view.user.click(within(dialog).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
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
    await view.user.click(screen.getByRole('button', { name: 'Apply filter' }));
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
    const dialog = await screen.findByRole('dialog');
    await waitFor(() => expect(within(dialog).getByText('Success', { exact: true })).toBeVisible());
    expect(within(dialog).queryByText('Service call attempts', { exact: true })).toBeNull();
    expect(within(dialog).queryByRole('navigation', { name: 'Pagination' })).toBeNull();
    expect(
      fetchMock.mock.calls.map(([path]) => String(path)).filter((path) => path === detailPath),
    ).toHaveLength(1);
    expect(
      fetchMock.mock.calls.map(([path]) => String(path)).some((path) => path.includes('/attempts')),
    ).toBe(false);
  });

  it('keeps an empty steward page paginated and marks the result region busy state', async () => {
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
    expect(screen.getByText('That page is no longer available. Showing page 1.')).toBeVisible();
    expect(screen.getByRole('navigation', { name: 'Pagination' })).toBeVisible();
    const emptyResult = screen.getByText('No logs', { exact: true }).closest('.ops-stack');
    expect(emptyResult?.getAttribute('aria-busy')).toBe('false');
  });
});
