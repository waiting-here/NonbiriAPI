import { fireEvent, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import type { Period } from '../features/operations/economy';
import { ActivitiesPage } from './ActivitiesPage';

const previousPeriod: Period = {
  id: `thu_${'A'.repeat(22)}`,
  period_key: '2026-09-10',
  opens_at: Date.parse('2026-09-10T00:00:00+08:00') / 1_000,
  closes_at: Date.parse('2026-09-11T00:00:00+08:00') / 1_000,
  state: 'configuration_error',
  revision: '9',
  literature: 'Previous announcement',
  entry: '1',
  per_user_limit: 2,
  pumps_bp: { platform: 100, welfare: 200, next_pool: 300 },
  current_pool_id: `pol_${'A'.repeat(22)}`,
  next_pool_id: `pol_${'B'.repeat(21)}A`,
  settlement: null,
  created_at: 1_700_000_000,
  terminal_at: null,
};

function installActivities(initialPeriod: Period | null) {
  let period = initialPeriod;
  const writes: Record<string, unknown>[] = [];
  const reply = (body: unknown) =>
    new Response(JSON.stringify(body), {
      headers: { 'content-type': 'application/json' },
    });
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      const method = init?.method ?? 'GET';
      if (method === 'GET' && url.pathname === '/admin/api/session')
        return reply({ admin: { username: 'fixture-admin' } });
      if (method === 'GET' && url.pathname === '/admin/api/activities/config')
        return reply({
          revision: '4',
          master_enabled: false,
          welfare: { enabled: false, threshold: '1', cap: '2' },
          thursday: { enabled: false },
        });
      if (method === 'GET' && url.pathname === '/admin/api/activities/thursday')
        return reply({ period });
      if (method === 'GET' && url.pathname === '/admin/api/pools')
        return reply({
          data: [],
          next_cursor: null,
          pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
        });
      if (method === 'PUT' && url.pathname === '/admin/api/activities/thursday/next') {
        const body = JSON.parse(String(init?.body)) as Pick<
          Period,
          'period_key' | 'opens_at' | 'entry' | 'literature' | 'per_user_limit' | 'pumps_bp'
        >;
        writes.push(body);
        period = {
          ...previousPeriod,
          period_key: body.period_key,
          opens_at: body.opens_at,
          closes_at: body.opens_at + 86_400,
          entry: body.entry,
          literature: body.literature,
          per_user_limit: body.per_user_limit,
          pumps_bp: body.pumps_bp,
          state: 'configured',
          revision: '1',
        };
        return reply(period);
      }
      throw new Error(`Unexpected activity request: ${method} ${url.pathname}`);
    }),
  );
  return writes;
}

async function renderActivities() {
  const view = await renderWithProviders(<ActivitiesPage />, {
    station: 'admin',
    role: 'admin',
    route: '/activities',
  });
  const entry = await screen.findByLabelText('Entry (credits)');
  return { ...view, entry };
}

describe('automatic activity schedules', () => {
  beforeEach(() => {
    vi.spyOn(Date, 'now').mockReturnValue(Date.parse('2026-09-11T12:00:00Z'));
  });

  it.each([null, previousPeriod])(
    'creates the next Thursday from the activity revision with prior state %j',
    async (initial) => {
      const writes = installActivities(initial);
      const view = await renderActivities();
      await waitFor(() => expect(view.entry).toBeEnabled());
      expect(screen.getByRole('heading', { name: 'Create next period' })).toBeVisible();
      expect(screen.getByText('2026-09-17 00:00 — 2026-09-18 00:00')).toBeVisible();
      expect(screen.queryByLabelText('Period key')).not.toBeInTheDocument();
      expect(document.querySelector('input[type="datetime-local"]')).toBeNull();
      await view.user.clear(view.entry);
      await view.user.type(view.entry, '2');
      await view.user.clear(screen.getByLabelText('Literature'));
      await view.user.type(screen.getByLabelText('Literature'), 'Next announcement');
      await view.user.click(screen.getByRole('button', { name: 'Save next period' }));
      await waitFor(() => expect(writes).toHaveLength(1));
      expect(writes[0]).toEqual(
        expect.objectContaining({
          expected_revision: '4',
          period_key: '2026-09-17',
          opens_at: Date.parse('2026-09-17T00:00:00+08:00') / 1_000,
          entry: '2',
          literature: 'Next announcement',
        }),
      );
      await screen.findByRole('heading', { name: 'Update configured next period' });
    },
  );

  it('refreshes a new schedule across the Thursday boundary without losing edits, and allows cancel', async () => {
    vi.mocked(Date.now).mockReturnValue(Date.parse('2026-12-30T15:59:59Z'));
    const writes = installActivities(null);
    const view = await renderActivities();
    await waitFor(() => expect(view.entry).toBeEnabled());
    expect(screen.getByText('2026-12-31 00:00 — 2027-01-01 00:00')).toBeVisible();
    await view.user.type(view.entry, '3');
    await view.user.type(screen.getByLabelText('Literature'), 'Retained draft');
    vi.mocked(Date.now).mockReturnValue(Date.parse('2026-12-30T16:00:00Z'));
    fireEvent.focus(window);
    await screen.findByText('2027-01-07 00:00 — 2027-01-08 00:00');
    expect(screen.getByLabelText('Literature')).toHaveValue('Retained draft');
    await view.user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(view.entry).toHaveValue('');
    expect(screen.getByLabelText('Literature')).toHaveValue('');
    expect(screen.getByText('2027-01-07 00:00 — 2027-01-08 00:00')).toBeVisible();
    expect(writes).toHaveLength(0);
  });

  it('recalculates a new schedule on submission after a suspended tab crosses the boundary', async () => {
    vi.mocked(Date.now).mockReturnValue(Date.parse('2026-12-30T15:59:59Z'));
    const writes = installActivities(null);
    const view = await renderActivities();
    await waitFor(() => expect(view.entry).toBeEnabled());
    await view.user.type(view.entry, '1');
    await view.user.type(screen.getByLabelText('Literature'), 'After midnight');
    vi.mocked(Date.now).mockReturnValue(Date.parse('2026-12-30T16:00:00Z'));
    fireEvent.click(screen.getByRole('button', { name: 'Save next period' }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toEqual(
      expect.objectContaining({
        period_key: '2027-01-07',
        opens_at: Date.parse('2027-01-07T00:00:00+08:00') / 1_000,
      }),
    );
  });

  it.each(['open', 'settling'] as const)('keeps a %s period read-only', async (state) => {
    const writes = installActivities({
      ...previousPeriod,
      state,
      settlement:
        state === 'settling'
          ? {
              frozen_pool: '0',
              contribution_count: '0',
              eligible_contribution_count: '0',
              processed_count: '0',
              payout_total: '0',
              rollover: '0',
            }
          : null,
    });
    const view = await renderActivities();
    await screen.findByText(
      'The period is open or settling; frozen/current configuration cannot be edited.',
    );
    expect(view.entry).toBeDisabled();
    expect(screen.getByLabelText('Literature')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Save next period' })).toBeDisabled();
    expect(writes).toHaveLength(0);
  });
});
