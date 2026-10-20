import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { defaultRecurringDiscount } from '@shared/operations/recurringDiscount';
import { renderWithProviders } from '../../../test/unit/support';
import { CharityPriceTable, type CharityPriceTableProps } from './CharityPriceTable';

const NOW = 1_800_000_000;
const recurring = { ...defaultRecurringDiscount(), enabled: true, time_zone: 'Asia/Tokyo' };
const base: CharityPriceTableProps = {
  mode: 'per_request',
  rows: [{ label: 'Per request', userMilli: '100000', currentUserMilli: '50000' }],
  serverNow: NOW,
  discount: { enabled: false, percent: 80 },
  recurringDiscount: recurring,
  effectiveDiscount: { source: 'recurring', percent: 50, next_change_at: null },
};

describe('server-projected charity prices', () => {
  it.each([
    { source: 'recurring', percent: 50, current: '50000', next: NOW + 60, fixed: false },
    { source: 'fixed', percent: 50, current: '50000', next: null, fixed: true },
    { source: 'fixed', percent: 100, current: '100000', next: NOW + 30, fixed: true },
    { source: 'fixed', percent: 100, current: '100000', next: null, fixed: true },
    { source: 'none', percent: 100, current: '100000', next: null, fixed: false },
  ] as const)(
    'renders $source/$percent and only its next change',
    async ({ source, percent, current, next, fixed }) => {
      const { container } = await renderWithProviders(
        <CharityPriceTable
          {...base}
          rows={[{ ...base.rows[0]!, currentUserMilli: current }]}
          discount={{ enabled: fixed, percent: 50, startAt: NOW + 30, endAt: NOW + 60 }}
          recurringDiscount={source === 'none' ? defaultRecurringDiscount() : recurring}
          effectiveDiscount={{ source, percent, next_change_at: next }}
        />,
        { station: 'user', role: 'user', locale: 'en' },
      );
      expect(screen.getByLabelText(`Current price: ${Number(current) / 1000}`)).toBeVisible();
      expect(container.querySelectorAll('s')).toHaveLength(percent < 100 ? 1 : 0);
      expect(container.querySelectorAll('time')).toHaveLength(next === null ? 0 : 1);
      expect(Boolean(screen.queryByText(/Asia\/Tokyo/))).toBe(source !== 'none');
      expect(Boolean(screen.queryByText(/Recurring discounts are paused/))).toBe(fixed);
    },
  );

  it('shows free current prices without implying an expiry', async () => {
    await renderWithProviders(
      <CharityPriceTable
        {...base}
        rows={[{ ...base.rows[0]!, currentUserMilli: '0' }]}
        effectiveDiscount={{ source: 'recurring', percent: 0, next_change_at: null }}
      />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    expect(screen.getByRole('status')).toHaveTextContent(/^Free$/);
    expect(screen.getByLabelText('Current price: 0')).toBeVisible();
  });

  it('updates all four exact compact prices only when a new server snapshot arrives', async () => {
    const rows = ['Input', 'Cache write', 'Cache read', 'Output'].map((label, index) => ({
      label,
      userMilli: String((index + 1) * 1234),
      currentUserMilli: String((index + 1) * 1234),
    }));
    const props = {
      ...base,
      compact: true,
      mode: 'per_token' as const,
      rows,
      effectiveDiscount: { source: 'recurring' as const, percent: 100, next_change_at: NOW + 30 },
    };
    const view = await renderWithProviders(<CharityPriceTable {...props} />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    view.rerender(<CharityPriceTable {...props} serverNow={NOW + 30} />);
    expect(screen.getByLabelText('Current price: 4.936')).toBeVisible();
    expect(view.container.querySelectorAll('s')).toHaveLength(0);
    view.rerender(
      <CharityPriceTable
        {...props}
        serverNow={NOW + 30}
        rows={rows.map((row, i) => ({ ...row, currentUserMilli: String((i + 1) * 617) }))}
        effectiveDiscount={{ source: 'recurring', percent: 50, next_change_at: null }}
      />,
    );
    for (const amount of ['0.617', '1.234', '1.851', '2.468'])
      expect(screen.getByLabelText(`Current price: ${amount}`)).toBeVisible();
    expect(view.container.querySelectorAll('s')).toHaveLength(4);
    expect(screen.getByLabelText('Original price: 4.936')).toBeVisible();
  });

  it('shows long-context current prices and an undiscounted independent fine', async () => {
    await renderWithProviders(
      <CharityPriceTable
        {...base}
        longContext={{
          inputThreshold: '100000',
          rows: [{ label: 'Long request', userMilli: '300000', currentUserMilli: '150000' }],
        }}
        contextPenalty={{ inputThreshold: '200000', fineMilli: '50000' }}
      />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    expect(screen.getByText('Long context: input ≥ 100,000 tokens')).toBeVisible();
    expect(screen.getByLabelText('Current price: 150')).toBeVisible();
    expect(screen.getByText('Context penalty: input ≥ 200,000 tokens')).toBeVisible();
    expect(screen.getByText('Extra 50 credits per request, without a discount')).toBeVisible();
  });
});
