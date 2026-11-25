import { renderHistoricalLogs as renderWithProviders } from '../../../../test/unit/logSupport';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures, type JsonFetchFixture } from '../../../../test/unit/support';
import { RoleLogPanel } from './RoleLogPanel';
import type { LogRole } from './data';

const usage = {
  uncached_input_tokens: '0',
  cache_write_input_tokens: '0',
  cache_read_input_tokens: '0',
  output_tokens: '1',
  total_tokens: '1',
  usage_unknown: false,
  charge: '0',
};
const requestID = `req_${'A'.repeat(21)}Q`;
const localFrom = '2026-09-08T12:34:00';
const localTo = '2026-09-08T13:34:00';
const from = Date.parse(`${localFrom}Z`) / 1_000;
const to = Date.parse(`${localTo}Z`) / 1_000;
const nativeOptions = Intl.DateTimeFormat.prototype.resolvedOptions;
let currentZone = 'UTC';

function row(role: LogRole) {
  return {
    id: requestID,
    route_kind: 'openai_chat_completions',
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
    ...(role === 'user' ? {} : { usage_total_mismatch: false }),
    ...(role === 'admin' ? { user_id: null, caller_identity: null } : {}),
    ...(role === 'user' ? { model: 'model', attempt_count: '1' } : { attempt_count: '1' }),
    ...(role === 'steward' ? { user_id: null, caller_identity: null } : {}),
  };
}

function installFixtures(role: LogRole) {
  const prefix = role === 'admin' ? '/admin/api' : '/api';
  const logPath =
    role === 'admin' ? '/admin/api/logs' : role === 'steward' ? '/api/steward/logs' : '/api/logs';
  const listBody = {
    data: [row(role)],
    next_cursor: null,
    pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
  };
  const filteredLogPath = `${logPath}?from=${from}&to=${to}&page=1&page_size=20`;
  const resolvePath = `${prefix}/time/resolve?${new URLSearchParams({
    local: localFrom,
    time_zone: 'UTC',
  })}`;
  const resolveToPath = `${prefix}/time/resolve?${new URLSearchParams({
    local: localTo,
    time_zone: 'UTC',
  })}`;
  return installJsonFetchFixtures([
    {
      method: 'GET',
      path: `${prefix}/time-zones`,
      body: { version: 'go1.26.6-zoneinfo', zones: ['UTC'] },
    },
    {
      method: 'GET',
      path: resolvePath,
      body: {
        instant: from,
        local: localFrom,
        time_zone: 'UTC',
        offset_seconds: 0,
        adjustment: 'none',
      },
    },
    {
      method: 'GET',
      path: resolveToPath,
      body: {
        instant: to,
        local: localTo,
        time_zone: 'UTC',
        offset_seconds: 0,
        adjustment: 'none',
      },
    },
    {
      method: 'GET',
      path: `${logPath}?page=1&page_size=20`,
      body: listBody,
    },
    {
      method: 'GET',
      path: filteredLogPath,
      body: listBody,
    },
  ]);
}

beforeEach(() => {
  currentZone = 'UTC';
  vi.spyOn(Intl.DateTimeFormat.prototype, 'resolvedOptions').mockImplementation(function (
    this: Intl.DateTimeFormat,
  ) {
    return { ...nativeOptions.call(this), timeZone: currentZone };
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('role log time filters', () => {
  it('rechecks a zone change that has not emitted a browser notification', async () => {
    const fetchMock = installFixtures('user');
    const view = await renderWithProviders(<RoleLogPanel accountId="viewer" role="user" />, {
      station: 'user',
      role: 'user',
    });
    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
        '/api/logs?page=1&page_size=20',
      ),
    );
    await view.user.click(screen.getByText('More filters', { exact: true }));
    const start = screen.getByLabelText('Filter by start time');
    fireEvent.change(start, { target: { value: localFrom.slice(0, 16) } });
    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([path]) => String(path).includes('/time/resolve?'))).toBe(
        true,
      ),
    );
    await waitFor(() => expect(screen.queryByText('Checking this time…')).toBeNull());
    currentZone = 'Asia/Tokyo';
    fireEvent.submit(screen.getByRole('button', { name: 'Search' }).closest('form')!);
    expect(screen.getByText('Check the date and time zone, then search again.')).toBeVisible();
    expect(start).toHaveFocus();
    expect(fetchMock.mock.calls.some(([path]) => String(path).includes('/api/logs?from='))).toBe(
      false,
    );
  });

  it.each([
    {
      role: 'admin' as const,
      station: 'admin' as const,
      testRole: 'admin' as const,
      timePath: '/admin/api/time-context',
    },
    {
      role: 'steward' as const,
      station: 'steward' as const,
      testRole: 'level5' as const,
      timePath: '/api/steward/time-context',
    },
  ])(
    'uses the %s station for time resolution and preserves range validation',
    async ({ role, station, testRole, timePath }) => {
      const fetchMock = installFixtures(role);
      const view = await renderWithProviders(<RoleLogPanel accountId="viewer" role={role} />, {
        station: station === 'steward' ? 'admin' : station,
        role: testRole,
      });
      await waitFor(() =>
        expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(timePath),
      );
      const pathsBefore = fetchMock.mock.calls.map(([path]) => String(path));
      expect(
        pathsBefore.some(
          (path) =>
            path ===
            (station === 'admin' ? '/api/steward/time-context' : '/admin/api/time-context'),
        ),
      ).toBe(false);

      const fromInput = screen.getByLabelText('From');
      const toInput = screen.getByLabelText('To');
      fireEvent.change(fromInput, { target: { value: localFrom.slice(0, 16) } });
      fireEvent.change(toInput, { target: { value: localTo.slice(0, 16) } });
      await waitFor(() => expect(screen.getByRole('button', { name: 'Search' })).toBeEnabled());
      await view.user.click(screen.getByRole('button', { name: 'Search' }));
      await waitFor(() =>
        expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
          `${role === 'admin' ? '/admin/api/logs' : '/api/steward/logs'}?from=${from}&to=${to}&page=1&page_size=20`,
        ),
      );
    },
  );

  it('keeps an invalid range in the inputs and does not replace the applied filter', async () => {
    installFixtures('admin');
    const view = await renderWithProviders(<RoleLogPanel accountId="viewer" role="admin" />, {
      station: 'admin',
      role: 'admin',
    });
    const fromInput = screen.getByLabelText('From');
    const toInput = screen.getByLabelText('To');
    fireEvent.change(fromInput, { target: { value: localTo.slice(0, 16) } });
    fireEvent.change(toInput, { target: { value: localFrom.slice(0, 16) } });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Search' })).toBeEnabled());
    await view.user.click(screen.getByRole('button', { name: 'Search' }));
    expect(toInput).toHaveAccessibleDescription('Choose an end time later than the start time.');
    expect(toInput).toHaveFocus();
    expect(fromInput).toHaveValue(localTo.slice(0, 16));
    expect(toInput).toHaveValue(localFrom.slice(0, 16));
  });
});

it('advances a relative range on focus while preserving searches that have handed off', async () => {
  let seconds = to;
  vi.spyOn(Date, 'now').mockImplementation(() => seconds * 1000);
  const listPath = (end: number) => `/api/logs?from=${end - 900}&to=${end}&page=1&page_size=20`;
  const body = {
    data: [row('user')],
    next_cursor: null,
    pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
  };
  const receipt = { status: 'running', search_id: 'relative-search' };
  const searchPath = '/api/log-searches/relative-search';
  const poll = searchPath + '?page=1&page_size=20';
  const requests: JsonFetchFixture[] = [to, to + 180, to + 240].map((end) => ({
    method: 'GET',
    path: listPath(end),
    body,
  }));
  requests.push({ method: 'GET', path: listPath(to + 60), body: receipt, status: 202 });
  const fetchMock = installJsonFetchFixtures([
    {
      method: 'GET',
      path: '/api/time-zones',
      body: { version: 'go1.26.6-zoneinfo', zones: ['UTC'] },
    },
    ...requests,
  ]);
  const mocked = fetchMock.getMockImplementation()!;
  let finishSearch!: (response: Response) => void;
  const pending = new Promise<Response>((resolve) => {
    finishSearch = resolve;
  });
  fetchMock.mockImplementation((path, options?: RequestInit) => {
    if (path === poll) return pending;
    if (path === searchPath && options?.method === 'DELETE') {
      return Promise.resolve(new Response(null, { status: 204 }));
    }
    return mocked(path, options);
  });
  const listCalls = () =>
    fetchMock.mock.calls
      .map(([path]) => String(path))
      .filter((path) => path.startsWith('/api/logs?'));

  const view = await renderWithProviders(<RoleLogPanel role="user" accountId="viewer" />, {
    station: 'user',
    role: 'user',
    route: '/logs?range=15m',
  });
  await screen.findByText('Search complete');
  expect(listCalls()).toEqual([listPath(to)]);

  seconds = to + 60;
  fireEvent.focus(window);
  await screen.findByText('Digging through the logs…');
  await waitFor(() => expect(fetchMock.mock.calls.some(([path]) => path === poll)).toBe(true));
  seconds = to + 120;
  fireEvent.focus(window);
  expect(listCalls()).toEqual([listPath(to), listPath(to + 60)]);
  await act(async () =>
    finishSearch(
      new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } }),
    ),
  );
  await screen.findByText('Search complete');

  seconds = to + 180;
  fireEvent.focus(window);
  expect(listCalls()).toEqual([listPath(to), listPath(to + 60)]);
  await view.user.click(screen.getByRole('button', { name: 'Refresh' }));
  await waitFor(() => expect(listCalls()).toContain(listPath(to + 180)));
  await screen.findByText('Search complete');

  seconds = to + 240;
  await view.user.click(screen.getByRole('button', { name: 'Last 15 minutes' }));
  await waitFor(() =>
    expect(listCalls()).toEqual([
      listPath(to),
      listPath(to + 60),
      listPath(to + 180),
      listPath(to + 240),
    ]),
  );
});
