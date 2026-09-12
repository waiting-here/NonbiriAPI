import { fireEvent, screen, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures, renderWithProviders } from '../../../test/unit/support';
import { CreditsPage } from './CreditsPage';

vi.mock('../components/UserPageGate', () => ({
  UserPageGate: ({ children }: { children: ReactNode }) => <>{children}</>,
}));
vi.mock('../data', () => ({
  useUserSession: () => ({ data: { user: { id: 'account-1' } }, isPending: false, error: null }),
}));
vi.mock('../features/core/queries', () => ({
  coreSessionMatchesAccount: () => true,
}));

const nativeOptions = Intl.DateTimeFormat.prototype.resolvedOptions;
let currentZone = 'UTC';
const localFrom = '2026-09-08T12:34:00';
const localTo = '2026-09-08T13:34:00';
const from = Date.parse(`${localFrom}Z`) / 1_000;
const to = Date.parse(`${localTo}Z`) / 1_000;
const operationID = `op_${'A'.repeat(22)}`;
const entry = {
  asset_type: 'general',
  operation_id: operationID,
  line: 1,
  kind: 'checkin_award',
  delta: '1',
  created_at: 1_800_000_000,
  request_id: null,
};
const page = {
  data: [entry],
  page: '1',
  page_size: 20,
  total: '1',
  total_pages: '1',
  anchor: operationID,
  game_balance: '0',
  current_balance: '1',
  server_now: 1_800_000_001,
};

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

describe('credit history time filters', () => {
  it('rechecks the browser zone at submit before any focus notification', async () => {
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/api/time-zones',
        body: { version: 'go1.26.6-zoneinfo', zones: ['UTC'] },
      },
      {
        method: 'GET',
        path: '/api/credits/history?asset_type=all&page=1&page_size=20',
        body: page,
      },
    ]);
    await renderWithProviders(<CreditsPage />, { station: 'user', role: 'user' });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Apply filters' })).toBeEnabled(),
    );
    const requests = fetchMock.mock.calls.length;
    currentZone = 'Asia/Tokyo';
    fireEvent.submit(screen.getByRole('button', { name: 'Apply filters' }).closest('form')!);
    expect(screen.getByRole('alert')).toBeVisible();
    expect(fetchMock.mock.calls).toHaveLength(requests);
  });

  it('uses the user time station, sends resolved seconds, and restarts at page one', async () => {
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/api/time-zones',
        body: { version: 'go1.26.6-zoneinfo', zones: ['UTC'] },
      },
      {
        method: 'GET',
        path: `/api/time/resolve?${new URLSearchParams({ local: localFrom, time_zone: 'UTC' })}`,
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
        path: `/api/time/resolve?${new URLSearchParams({ local: localTo, time_zone: 'UTC' })}`,
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
        path: '/api/credits/history?asset_type=all&page=1&page_size=20',
        body: page,
      },
      {
        method: 'GET',
        path: `/api/credits/history?asset_type=all&page=1&page_size=20&from=${from}&to=${to}`,
        body: page,
      },
    ]);
    const view = await renderWithProviders(<CreditsPage />, { station: 'user', role: 'user' });
    await waitFor(() =>
      expect(screen.getByRole('heading', { name: 'Nonbiri credit history' })).toBeVisible(),
    );

    const fromInput = screen.getByLabelText('From');
    const toInput = screen.getByLabelText('Before');
    fireEvent.change(fromInput, { target: { value: localFrom.slice(0, 16) } });
    fireEvent.change(toInput, { target: { value: localTo.slice(0, 16) } });
    expect(screen.getByRole('button', { name: 'Apply filters' })).toBeDisabled();
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Apply filters' })).toBeEnabled(),
    );
    await view.user.click(screen.getByRole('button', { name: 'Apply filters' }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
        `/api/credits/history?asset_type=all&page=1&page_size=20&from=${from}&to=${to}`,
      ),
    );
    expect(fetchMock.mock.calls.map(([path]) => String(path))).not.toContain(
      '/admin/api/time-zones',
    );
  });
});
