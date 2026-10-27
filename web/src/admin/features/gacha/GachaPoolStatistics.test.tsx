import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import {
  statisticsCards,
  statisticsPage,
  statisticsPool,
  statisticsRarities,
  removedStatisticsPool,
} from '../../../../test/fixtures/gacha-statistics';
import { GachaPoolStatistics } from './GachaPoolStatistics';
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
async function setup({ empty = false, fail = false } = {}) {
  const requests: URL[] = [];
  let failed = false;
  vi.stubGlobal(
    'fetch',
    vi.fn<typeof fetch>(async (input) => {
      const url = new URL(String(input), 'https://admin.example');
      requests.push(url);
      if (url.pathname === '/admin/api/gacha/statistics/pools')
        return json(
          statisticsPage(empty ? [] : [statisticsPool, removedStatisticsPool], url.searchParams),
        );
      if (url.pathname === '/admin/api/gacha/statistics/pools/permanent/cards') {
        if (fail && !failed) {
          failed = true;
          return json(
            { error: { code: 'internal_error', message: 'Statistics unavailable' } },
            503,
          );
        }
        const q = url.searchParams.get('q') ?? '';
        const items = statisticsCards.filter((row) =>
          row.name_en.toLowerCase().includes(q.toLowerCase()),
        );
        return json({
          pool: statisticsPool,
          ...statisticsPage(items, url.searchParams),
          rarity_totals: statisticsRarities,
          top_cards: statisticsCards.slice(0, 2),
        });
      }
      if (url.pathname === '/admin/api/gacha/statistics/pools/gp_removed/cards')
        return json({
          pool: removedStatisticsPool,
          ...statisticsPage(
            [
              {
                ...statisticsCards[0]!,
                rarity: null,
                count: '0',
                participant_count: '0',
                first_acquisition_count: '0',
              },
            ],
            url.searchParams,
          ),
          rarity_totals: [{ rarity: null, count: '0', first_acquisition_count: '0' }],
          top_cards: [],
        });
      throw Error('Unexpected request: ' + url.pathname);
    }),
  );
  const view = await renderWithProviders(<GachaPoolStatistics accountKey="admin" />, {
    station: 'admin',
    locale: 'en',
    role: 'admin',
  });
  return { ...view, requests };
}
describe('administrator draw statistics', () => {
  it('keeps exact totals and separates acquisitions, participants and first acquisitions', async () => {
    await setup();
    const panel = await screen.findByRole('region', { name: 'Selected pool statistics' });
    await waitFor(() =>
      expect(within(panel).getByLabelText('9,007,199,254,740,993')).toBeVisible(),
    );
    expect(
      within(panel).getByText(
        'Each draw result counts, including repeats. Card participants are distinct within this pool. A first acquisition means the player did not already own the card when it was drawn.',
      ),
    ).toBeVisible();
    const table = within(panel).getByRole('table', { name: 'Card counts' });
    expect(within(table).getByRole('columnheader', { name: 'First-time owners' })).toBeVisible();
    const row = within(table).getByRole('row', { name: /First star/ });
    expect(within(row).getByLabelText('9,007,199,254,740,000')).toBeVisible();
    expect(within(row).getByLabelText('1,234')).toBeVisible();
    expect(within(row).getByLabelText('17')).toBeVisible();
    expect(within(panel).getByLabelText('19')).toBeVisible();
  });
  it('pages and searches one pool while charts and complete downloads keep whole-pool scope', async () => {
    const view = await setup();
    const panel = await screen.findByRole('region', { name: 'Selected pool statistics' });
    await within(panel).findByRole('table', { name: 'Card counts' });
    await view.user.click(within(panel).getByRole('button', { name: 'Next' }));
    await waitFor(() =>
      expect(
        view.requests.some(
          (url) =>
            url.pathname.endsWith('/permanent/cards') && url.searchParams.get('page') === '2',
        ),
      ).toBe(true),
    );
    await view.user.type(within(panel).getByLabelText('Search cards'), 'silver');
    const filters = within(panel).getByRole('form', { name: 'Card filters' });
    await view.user.click(within(filters).getByRole('button', { name: 'Search' }));
    await waitFor(() =>
      expect(
        view.requests.some(
          (url) => url.searchParams.get('q') === 'silver' && url.searchParams.get('page') === '1',
        ),
      ).toBe(true),
    );
    await within(panel).findByText('Silver moon');
    await view.user.selectOptions(within(panel).getByLabelText('Card order'), 'rarity');
    await waitFor(() =>
      expect(
        view.requests.some(
          (url) =>
            url.searchParams.get('q') === 'silver' && url.searchParams.get('sort') === 'rarity',
        ),
      ).toBe(true),
    );
    expect(within(panel).getAllByTestId('chart-data')[1]).toHaveTextContent('First star');
    expect(within(panel).getByRole('link', { name: 'Download full JSON' })).toHaveAttribute(
      'href',
      '/admin/api/gacha/statistics/pools/permanent/export?format=json',
    );
    expect(within(panel).getByRole('link', { name: 'Download full CSV' })).toHaveAttribute(
      'href',
      '/admin/api/gacha/statistics/pools/permanent/export?format=csv',
    );
  });
  it('retains removed pool statistics and zero counts with unspecified rarity', async () => {
    const view = await setup();
    const list = await screen.findByRole('table', { name: 'Pool statistics' });
    const removed = within(list).getByRole('row', { name: /Removed season/ });
    await view.user.click(within(removed).getByRole('button', { name: 'View card counts' }));
    const panel = screen.getByRole('region', { name: 'Selected pool statistics' });
    await within(panel).findByRole('heading', { name: 'Removed season' });
    expect(within(panel).getByText('Pool configuration removed')).toBeVisible();
    expect(within(panel).getByText('This pool has no completed draw results yet.')).toBeVisible();
    expect(
      within(within(panel).getByRole('table', { name: 'Card counts' })).getByText('Unspecified'),
    ).toBeVisible();
  });
  it('keeps the pool browser usable when card loading fails and retries the selected pool', async () => {
    const view = await setup({ fail: true });
    const panel = await screen.findByRole('region', { name: 'Selected pool statistics' });
    expect(await within(panel).findByRole('alert')).toHaveTextContent('Statistics unavailable');
    expect(screen.getByRole('table', { name: 'Pool statistics' })).toBeVisible();
    await view.user.click(within(panel).getByRole('button', { name: 'Retry' }));
    await within(panel).findByRole('table', { name: 'Card counts' });
  });
  it('explains an empty pool list without requesting any card or user data', async () => {
    const view = await setup({ empty: true });
    await screen.findByRole('heading', { name: 'No pool statistics' });
    expect(view.requests).toHaveLength(1);
    expect(
      screen.queryByRole('region', { name: 'Selected pool statistics' }),
    ).not.toBeInTheDocument();
  });
});
