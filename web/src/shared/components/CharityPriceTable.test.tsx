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
});
