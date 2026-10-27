import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { statisticsPage } from '../../../../test/fixtures/gacha-statistics';
import {
  shopStatisticsCards,
  shopStatisticsRarities,
  shopStatisticsSummary,
} from '../../../../test/fixtures/gacha-shop-statistics';
import { GachaShopStatistics } from './GachaShopStatistics';
import type { ChartRow } from './chartExport';

vi.mock('./GachaStatisticsChart', () => ({
  GachaStatisticsChart: ({ title, rows }: { title: string; rows: ChartRow[] }) => (
    <div>
      <p>{title}</p>
      <output data-testid="chart-data">{JSON.stringify(rows)}</output>
    </div>
  ),
}));
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
async function setup({ fail = false, empty = false, route = '/' } = {}) {
  const requests: URL[] = [];
  let failed = false;
  vi.stubGlobal(
    'fetch',
    vi.fn<typeof fetch>(async (input) => {
      const url = new URL(String(input), 'https://admin.example');
      requests.push(url);
      if (url.pathname !== '/admin/api/gacha/statistics/shop/cards')
        throw Error('Unexpected request: ' + url.pathname);
      if (fail && !failed) {
        failed = true;
        return json(
          { error: { code: 'internal_error', message: 'Shop statistics unavailable' } },
          503,
        );
      }
      const q = url.searchParams.get('q') ?? '';
      const cards = empty
        ? []
        : shopStatisticsCards.filter((row) =>
            (row.name + row.name_en + row.card_id).toLowerCase().includes(q.toLowerCase()),
          );
      return json({
        summary: empty ? { total_redemptions: '0', participant_count: '0' } : shopStatisticsSummary,
        ...statisticsPage(cards, url.searchParams),
        rarity_totals: empty ? [] : shopStatisticsRarities,
        top_cards: empty ? [] : shopStatisticsCards.slice(0, 2),
      });
    }),
  );
  const view = await renderWithProviders(<GachaShopStatistics accountKey="admin" />, {
    station: 'admin',
    locale: 'en',
    role: 'admin',
    route,
  });
  return { ...view, requests };
}
describe('administrator mystery-shop card statistics', () => {
  it('shows exact redemption totals and people without a redundant first-acquisition column', async () => {
    await setup();
    const panel = screen.getByRole('region', { name: 'Mystery-shop card statistics' });
    await within(panel).findByLabelText('9,007,199,254,741,001');
    expect(within(panel).getByLabelText('9,007,199,254,740,997')).toBeVisible();
    const table = within(panel).getByRole('table', { name: 'Card redemption counts' });
    expect(within(table).getAllByRole('columnheader')).toHaveLength(4);
    expect(within(table).queryByRole('columnheader', { name: /First/ })).not.toBeInTheDocument();
    const first = within(table).getByRole('row', { name: /Shop star/ });
    expect(within(first).getAllByLabelText('9,007,199,254,740,993')).toHaveLength(2);
    expect(within(panel).getByText(/Ticket purchases and bundles are excluded/)).toBeVisible();
  });
  it('pages, searches and sorts independently of whole-shop charts and full download URLs', async () => {
    const view = await setup();
    const panel = screen.getByRole('region', { name: 'Mystery-shop card statistics' });
    await within(panel).findByRole('table', { name: 'Card redemption counts' });
    await view.user.click(within(panel).getByRole('button', { name: 'Next' }));
    await within(panel).findByText('historical-card', { selector: 'strong' });
    expect(within(panel).getByText('Unspecified')).toBeVisible();
    await view.user.type(within(panel).getByLabelText('Search cards'), 'silver');
    await view.user.click(
      within(within(panel).getByRole('form', { name: 'Card filters' })).getByRole('button', {
        name: 'Search',
      }),
    );
    await within(panel).findByText('Limited silver moon');
    await view.user.selectOptions(within(panel).getByLabelText('Card order'), 'name');
    await waitFor(() =>
      expect(
        view.requests.some(
          (url) =>
            url.searchParams.get('q') === 'silver' &&
            url.searchParams.get('sort') === 'name' &&
            url.searchParams.get('page') === '1',
        ),
      ).toBe(true),
    );
    expect(within(panel).getAllByTestId('chart-data')[1]).toHaveTextContent('Shop star');
    expect(within(panel).getByLabelText('9,007,199,254,741,001')).toBeVisible();
    expect(within(panel).getByRole('link', { name: 'Download full JSON' })).toHaveAttribute(
      'href',
      '/admin/api/gacha/statistics/shop/export?format=json',
    );
    expect(within(panel).getByRole('link', { name: 'Download full CSV' })).toHaveAttribute(
      'href',
      '/admin/api/gacha/statistics/shop/export?format=csv',
    );
    expect(view.requests.every((url) => url.pathname.includes('/statistics/shop/'))).toBe(true);
  });
  it('restores shop table URL state and retries a failed read without losing it', async () => {
    const view = await setup({
      fail: true,
      route: '/?stats_shop_q=silver&stats_shop_sort=rarity&stats_shop_size=10',
    });
    const panel = screen.getByRole('region', { name: 'Mystery-shop card statistics' });
    expect(await within(panel).findByRole('alert')).toHaveTextContent(
      'Shop statistics unavailable',
    );
    await view.user.click(within(panel).getByRole('button', { name: 'Retry' }));
    await within(panel).findByText('Limited silver moon');
    expect(within(panel).getByLabelText('Search cards')).toHaveValue('silver');
    expect(within(panel).getByLabelText('Card order')).toHaveValue('rarity');
    expect(
      view.requests.every(
        (url) =>
          url.searchParams.get('q') === 'silver' && url.searchParams.get('page_size') === '10',
      ),
    ).toBe(true);
  });
  it('explains a shop with no card rows while preserving zero totals and complete download links', async () => {
    await setup({ empty: true });
    const panel = screen.getByRole('region', { name: 'Mystery-shop card statistics' });
    await within(panel).findByRole('heading', { name: 'No shop card statistics' });
    expect(within(panel).getByText('No cards have been redeemed from the shop yet.')).toBeVisible();
    expect(within(panel).getByRole('link', { name: 'Download full JSON' })).toBeVisible();
    expect(
      within(panel).queryByRole('table', { name: 'Card redemption counts' }),
    ).not.toBeInTheDocument();
  });
});
