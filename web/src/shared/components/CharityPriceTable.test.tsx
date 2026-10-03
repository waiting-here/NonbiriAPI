import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { CharityPriceTable, type CharityPriceTableProps } from './CharityPriceTable';

const NOW = 1_800_000_000;
const rows = [{ label: 'Per request', userMilli: '100000', discountedUserMilli: '50000' }];

describe('charity offer boundaries', () => {
  it.each<{
    name: string;
    discount: CharityPriceTableProps['discount'];
    active: boolean;
    times: number;
  }>([
    { name: 'unbounded offer', discount: { enabled: true, percent: 50 }, active: true, times: 0 },
    {
      name: 'bounded offer',
      discount: { enabled: true, percent: 50, endAt: NOW + 60 },
      active: true,
      times: 1,
    },
    {
      name: 'scheduled offer',
      discount: { enabled: true, percent: 50, startAt: NOW + 30, endAt: NOW + 60 },
      active: false,
      times: 2,
    },
    {
      name: 'expired offer',
      discount: { enabled: true, percent: 50, endAt: NOW },
      active: false,
      times: 0,
    },
    {
      name: 'disabled offer',
      discount: { enabled: false, percent: 50, endAt: NOW + 60 },
      active: false,
      times: 0,
    },
  ])('$name displays the applicable price and dates', async ({ discount, active, times }) => {
    const { container } = await renderWithProviders(
      <CharityPriceTable mode="per_request" rows={rows} serverNow={NOW} discount={discount} />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    expect(screen.queryByLabelText('Offer price: 50') !== null).toBe(active);
    expect(container.querySelectorAll('s')).toHaveLength(active ? 1 : 0);
    expect(container.querySelectorAll('time')).toHaveLength(times);
    expect(container.textContent).not.toContain('Limited-time');
    if (times > 0) expect(container.textContent).toContain('your local time');
  });

  it('shows an unbounded free offer without implying an expiry', async () => {
    await renderWithProviders(
      <CharityPriceTable
        mode="per_request"
        rows={[{ ...rows[0], discountedUserMilli: '0' }]}
        serverNow={NOW}
        discount={{ enabled: true, percent: 0 }}
      />,
      { station: 'user', role: 'user', locale: 'en' },
    );
    expect(screen.getByRole('status')).toHaveTextContent(/^Free$/);
    expect(screen.getByLabelText('Offer price: 0')).toBeVisible();
  });
  it('keeps all four compact token prices exact as a scheduled offer becomes active', async () => {
    const tokenRows = ['Input', 'Cache write', 'Cache read', 'Output'].map((label, index) => ({
      label,
      userMilli: String((index + 1) * 1234),
      discountedUserMilli: String((index + 1) * 617),
    }));
    const props = {
      compact: true,
      mode: 'per_token' as const,
      rows: tokenRows,
      discount: { enabled: true, percent: 50, startAt: NOW + 30 },
    };
    const view = await renderWithProviders(<CharityPriceTable {...props} serverNow={NOW} />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    expect(screen.getByLabelText('Offer price: 1.234')).toBeVisible();
    expect(screen.getByLabelText('Offer price: 2.468')).toBeVisible();
    expect(screen.getByLabelText('Offer price: 3.702')).toBeVisible();
    expect(screen.getByLabelText('Offer price: 4.936')).toBeVisible();
    expect(view.container.querySelectorAll('s')).toHaveLength(0);
    view.rerender(<CharityPriceTable {...props} serverNow={NOW + 30} />);
    expect(screen.getByLabelText('Offer price: 0.617')).toBeVisible();
    expect(screen.getByLabelText('Offer price: 1.234')).toBeVisible();
    expect(screen.getByLabelText('Offer price: 1.851')).toBeVisible();
    expect(screen.getByLabelText('Offer price: 2.468')).toBeVisible();
    expect(view.container.querySelectorAll('s')).toHaveLength(4);
    expect(screen.getByLabelText('Original price: 4.936')).toBeVisible();
  });
});
