import { fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures, renderWithProviders } from '../../../../test/unit/support';
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
    caller_result_class: 'success',
    caller_status: 200,
    caller_error_code: null,
    started_at: 1,
    completed_at: 2,
    usage,
    ...(role === 'admin' ? { user_id: null } : {}),
    ...(role === 'user' ? { model: 'model', attempt_count: '1' } : { attempt_count: '1' }),
    ...(role === 'steward' ? { caller_identity: null } : {}),
  };
}

function installFixtures(role: 'admin' | 'steward') {
  const prefix = role === 'admin' ? '/admin/api' : '/api';
  const logPath = role === 'admin' ? '/admin/api/logs' : '/api/steward/logs';
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
    const fetchMock = installFixtures('admin');
    await renderWithProviders(<RoleLogPanel accountId="viewer" role="admin" />, {
      station: 'admin',
      role: 'admin',
    });
    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
        '/admin/api/logs?page=1&page_size=20',
      ),
    );
    currentZone = 'Asia/Tokyo';
    fireEvent.submit(screen.getByRole('button', { name: 'Apply filter' }).closest('form')!);
    expect(screen.getByText(/status code or time range is invalid/i)).toBeVisible();
    expect(fetchMock.mock.calls.map(([path]) => String(path))).not.toContain(
      `/admin/api/logs?from=${from}&to=${to}&page=1&page_size=20`,
    );
  });

  it.each([
    {
      role: 'admin' as const,
      station: 'admin' as const,
      testRole: 'admin' as const,
      timePath: '/admin/api/time-zones',
    },
    {
      role: 'steward' as const,
      station: 'user' as const,
      testRole: 'level5' as const,
      timePath: '/api/time-zones',
    },
  ])(
    'uses the %s station for time resolution and preserves range validation',
    async ({ role, station, testRole, timePath }) => {
      const fetchMock = installFixtures(role);
      const view = await renderWithProviders(<RoleLogPanel accountId="viewer" role={role} />, {
        station,
        role: testRole,
      });
      await waitFor(() =>
        expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(timePath),
      );
      const pathsBefore = fetchMock.mock.calls.map(([path]) => String(path));
      expect(
        pathsBefore.some(
          (path) => path === (station === 'admin' ? '/api/time-zones' : '/admin/api/time-zones'),
        ),
      ).toBe(false);

      const fromInput = screen.getByLabelText('From');
      const toInput = screen.getByLabelText('To');
      fireEvent.change(fromInput, { target: { value: localFrom.slice(0, 16) } });
      fireEvent.change(toInput, { target: { value: localTo.slice(0, 16) } });
      expect(screen.getByRole('button', { name: 'Apply filter' })).toBeDisabled();
      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'Apply filter' })).toBeEnabled(),
      );
      await view.user.click(screen.getByRole('button', { name: 'Apply filter' }));
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
    await waitFor(() => expect(screen.getByRole('button', { name: 'Apply filter' })).toBeEnabled());
    await view.user.click(screen.getByRole('button', { name: 'Apply filter' }));
    expect(screen.getByText(/status code or time range is invalid/i)).toBeVisible();
    expect(fromInput).toHaveValue(localTo.slice(0, 16));
    expect(toInput).toHaveValue(localFrom.slice(0, 16));
  });
});
