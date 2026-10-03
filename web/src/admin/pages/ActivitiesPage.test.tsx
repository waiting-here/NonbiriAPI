import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import type { Period, Pool } from '../features/operations/economy';
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

const welfarePool: Pool = {
  id: `pol_${'C'.repeat(21)}A`,
  pool_type: 'welfare',
  period_id: null,
  state: 'open',
  revision: '3',
  balance: '50',
  created_at: 1_700_000_000,
  closed_at: null,
};

function installActivities(initialPeriod: Period | null, rejectWrite = false, pool?: Pool) {
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
          loan_enabled: false,
          loan_tiers: ['10000', '100000', '1000000'],
          loan_a: '0.9',
          loan_b: '1.3',
          welfare: { enabled: false, threshold: '1', cap: '2' },
          thursday: { enabled: false },
        });
      if (method === 'GET' && url.pathname === '/admin/api/activities/thursday')
        return reply({ period });
      if (method === 'GET' && url.pathname === '/admin/api/pools')
        return reply({
          data: pool ? [pool] : [],
          next_cursor: null,
          pagination: { page: '1', page_size: 20, total_items: pool ? '1' : '0', total_pages: '1' },
        });
      if (pool && method === 'POST' && url.pathname === `/admin/api/pools/${pool.id}/adjustments`) {
        writes.push(JSON.parse(String(init?.body)));
        if (rejectWrite)
          return new Response(
            JSON.stringify({
              error: { code: 'invalid_request', message: 'Pool adjustment rejected.' },
            }),
            { status: 400, headers: { 'content-type': 'application/json' } },
          );
        return reply(pool);
      }
      if (method === 'PUT' && url.pathname === '/admin/api/activities/thursday/next') {
        const body = JSON.parse(String(init?.body)) as Pick<
          Period,
          'period_key' | 'opens_at' | 'entry' | 'literature' | 'per_user_limit' | 'pumps_bp'
        >;
        writes.push(body);
        if (rejectWrite)
          return new Response(
            JSON.stringify({
              error: { code: 'conflict', message: 'revision changed', source: 'platform' },
            }),
            { status: 409, headers: { 'content-type': 'application/json' } },
          );
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

  it('updates the loan example with exact thousandth precision without submitting', async () => {
    const writes = installActivities(null);
    await renderActivities();
    expect(
      await screen.findByText(
        'Borrow 10,000 → receive 9,000 game credits; repay 13,000 general credits.',
      ),
    ).toBeVisible();
    const panel = screen.getByRole('heading', { name: 'Cyber loan' }).closest('section')!;
    const amount = within(panel).getAllByRole('textbox')[0];
    fireEvent.change(amount, { target: { value: '10001' } });
    fireEvent.change(screen.getByLabelText('Disbursement ratio'), { target: { value: '0.333' } });
    fireEvent.change(screen.getByLabelText('Repayment ratio'), { target: { value: '1.111' } });
    expect(
      screen.getByText(
        'Borrow 10,001 → receive 3,330.333 game credits; repay 11,111.111 general credits.',
      ),
    ).toBeVisible();
    expect(writes).toHaveLength(0);
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
      await view.user.click(screen.getByRole('button', { name: 'Save and schedule' }));
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

  it('validates multiline text by field and retains it after a rejected save', async () => {
    const writes = installActivities(null, true);
    const view = await renderActivities();
    await waitFor(() => expect(view.entry).toBeEnabled());
    await view.user.type(view.entry, '1');
    const literature = screen.getByLabelText('Literature');
    const save = screen.getByRole('button', { name: 'Save and schedule' });
    expect(save).toBeEnabled();
    fireEvent.change(literature, { target: { value: '界'.repeat(1025) } });
    expect(literature).toHaveAttribute('aria-invalid', 'true');
    expect(screen.getByRole('alert')).toHaveTextContent('Shorten the literature');
    expect(save).toBeDisabled();
    fireEvent.change(literature, { target: { value: 'First line\n\tSecond line' } });
    expect(literature).toHaveAttribute('aria-invalid', 'false');
    await view.user.click(save);
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toMatchObject({ literature: 'First line\n\tSecond line' });
    expect(literature).toHaveValue('First line\n\tSecond line');
  });

  it('edits natural fee percentages and sends the existing exact integer units', async () => {
    const writes = installActivities(null);
    const view = await renderActivities();
    await waitFor(() => expect(view.entry).toBeEnabled());
    await view.user.clear(view.entry);
    await view.user.type(view.entry, '2');
    await view.user.type(screen.getByLabelText('Literature'), 'Next announcement');
    const fee = screen.getByLabelText('Platform fee (%)');
    await view.user.clear(fee);
    await view.user.type(fee, '1.25');
    expect(fee).toHaveValue(1.25);
    await view.user.click(screen.getByRole('button', { name: 'Save and schedule' }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0].pumps_bp).toEqual({ platform: 125, welfare: 0, next_pool: 0 });
  });

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
    fireEvent.click(screen.getByRole('button', { name: 'Save and schedule' }));
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
    await screen.findByText('The activity is open or settling; its rules cannot be edited.');
    expect(view.entry).toBeDisabled();
    expect(screen.getByLabelText('Literature')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Save and schedule' })).toBeDisabled();
    expect(writes).toHaveLength(0);
  });
});

describe('pool adjustment confirmation', () => {
  it('keeps a current open Thursday pool protected from decreases', async () => {
    const writes = installActivities({ ...previousPeriod, state: 'open' }, false, {
      ...welfarePool,
      id: previousPeriod.current_pool_id,
      pool_type: 'thursday',
      period_id: previousPeriod.id,
    });
    const view = await renderActivities();
    await view.user.click(await screen.findByRole('button', { name: 'Select' }));
    expect(screen.getByRole('option', { name: 'Decrease' })).toBeDisabled();
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    expect(writes).toHaveLength(0);
  });

  it.each(['increase', 'decrease'] as const)(
    'confirms %s once, sends nothing on cancel, and retains failed input',
    async (direction) => {
      const writes = installActivities(null, true, welfarePool);
      const view = await renderActivities();
      expect(screen.queryByRole('columnheader', { name: 'Revision' })).not.toBeInTheDocument();
      await view.user.click(await screen.findByRole('button', { name: 'Select' }));
      await view.user.selectOptions(screen.getByLabelText('Direction'), direction);
      await view.user.type(screen.getByLabelText('Amount (credits)'), '2.5');
      await view.user.type(screen.getByLabelText('Reason'), 'Correct pool balance');
      expect(screen.queryByRole('checkbox', { name: /forcibly removes/ })).not.toBeInTheDocument();
      await view.user.click(screen.getByRole('button', { name: 'Apply adjustment' }));
      let dialog = screen.getByRole('alertdialog');
      expect(dialog).toHaveTextContent('2.5');
      expect(dialog).toHaveTextContent('Correct pool balance');
      expect(dialog).toHaveTextContent(direction === 'decrease' ? 'Remove' : 'Add');
      expect(writes).toHaveLength(0);
      await view.user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
      expect(writes).toHaveLength(0);
      expect(screen.getByLabelText('Amount (credits)')).toHaveValue('2.5');
      await view.user.click(screen.getByRole('button', { name: 'Apply adjustment' }));
      dialog = screen.getByRole('alertdialog');
      await view.user.click(within(dialog).getByRole('button', { name: 'Apply adjustment' }));
      await screen.findByText('Pool adjustment rejected.');
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
      expect(writes).toEqual([
        {
          expected_revision: '3',
          direction,
          amount: '2.5',
          reason: 'Correct pool balance',
          confirmation: direction === 'decrease' ? 'DECREASE' : '',
        },
      ]);
      expect(screen.getByLabelText('Amount (credits)')).toHaveValue('2.5');
      expect(screen.getByLabelText('Reason')).toHaveValue('Correct pool balance');
      await view.user.type(screen.getByLabelText('Reason'), ' again');
      expect(screen.queryByText('Pool adjustment rejected.')).not.toBeInTheDocument();
    },
  );
});
