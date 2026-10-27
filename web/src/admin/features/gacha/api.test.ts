import { describe, expect, it, vi } from 'vitest';
import {
  shopStatisticsCards,
  shopStatisticsRarities,
  shopStatisticsSummary,
} from '../../../../test/fixtures/gacha-shop-statistics';
import {
  statisticsCards,
  statisticsPool,
  statisticsRarities,
  statisticsPage,
} from '../../../../test/fixtures/gacha-statistics';
import {
  listPoolCardStatistics,
  normalizePoolCardStatistics,
  normalizePoolStatistics,
  poolStatisticsExportURL,
  listShopCardStatistics,
  normalizeShopCardStatistics,
  shopStatisticsExportURL,
} from './api';

describe('administrator pool statistics wire', () => {
  it('preserves exact counts and the three separate card metrics', () => {
    const result = normalizePoolCardStatistics({
      pool: { ...statisticsPool, extra: 'ignored' },
      ...statisticsPage(statisticsCards),
      rarity_totals: statisticsRarities,
      top_cards: statisticsCards.slice(0, 2),
      user_id: 'private-user',
    });
    expect(result.pool.total_draws).toBe('9007199254740993');
    expect(result.items[0]).toMatchObject({
      count: '9007199254740000',
      participant_count: '1234',
      first_acquisition_count: '17',
    });
    expect(result).not.toHaveProperty('user_id');
    expect(result.pool).not.toHaveProperty('extra');
    expect(normalizePoolStatistics({ ...statisticsPool, starts_at: null }).starts_at).toBeNull();
  });
  it('requests only one selected-pool card page and exports the complete pool without table filters', async () => {
    const fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          pool: statisticsPool,
          ...statisticsPage(statisticsCards),
          rarity_totals: statisticsRarities,
          top_cards: statisticsCards.slice(0, 2),
        }),
        { headers: { 'Content-Type': 'application/json' } },
      ),
    );
    vi.stubGlobal('fetch', fetch);
    await listPoolCardStatistics('pool/one', { page: '2', pageSize: 20 }, 'silver moon', 'rarity');
    const url = new URL(String(fetch.mock.calls[0]?.[0]), 'https://admin.example');
    expect(url.pathname).toBe('/admin/api/gacha/statistics/pools/pool%2Fone/cards');
    expect(url.searchParams.get('q')).toBe('silver moon');
    expect(url.searchParams.get('page')).toBe('2');
    expect(url.searchParams.get('sort')).toBe('rarity');
    const download = new URL(poolStatisticsExportURL('pool/one', 'csv'), 'https://admin.example');
    expect(download.pathname).toBe('/admin/api/gacha/statistics/pools/pool%2Fone/export');
    expect([...download.searchParams]).toEqual([['format', 'csv']]);
  });
});

describe('administrator shop statistics wire', () => {
  it('keeps exact whole-shop aggregates and card fields without account identities', () => {
    const data = normalizeShopCardStatistics({
      summary: { ...shopStatisticsSummary, account_ids: ['private'] },
      ...statisticsPage(shopStatisticsCards),
      rarity_totals: shopStatisticsRarities,
      top_cards: shopStatisticsCards.slice(0, 2),
      account_ids: ['private'],
    });
    expect(data.summary.total_redemptions).toBe('9007199254741001');
    expect(data.items[0]).toMatchObject({
      count: '9007199254740993',
      participant_count: '9007199254740993',
      first_acquisition_count: '9007199254740993',
    });
    expect(data).not.toHaveProperty('account_ids');
    expect(data.summary).not.toHaveProperty('account_ids');
  });
  it('requests a shop card page while both complete exports omit table filters', async () => {
    const fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          summary: shopStatisticsSummary,
          ...statisticsPage(shopStatisticsCards),
          rarity_totals: shopStatisticsRarities,
          top_cards: [],
        }),
        { headers: { 'Content-Type': 'application/json' } },
      ),
    );
    vi.stubGlobal('fetch', fetch);
    await listShopCardStatistics({ page: '2', pageSize: 10 }, 'silver moon', 'name');
    const url = new URL(String(fetch.mock.calls[0]?.[0]), 'https://admin.example');
    expect(url.pathname).toBe('/admin/api/gacha/statistics/shop/cards');
    expect(Object.fromEntries(url.searchParams)).toEqual({
      page: '2',
      page_size: '10',
      q: 'silver moon',
      sort: 'name',
    });
    for (const format of ['csv', 'json'] as const) {
      const download = new URL(shopStatisticsExportURL(format), 'https://admin.example');
      expect(download.pathname).toBe('/admin/api/gacha/statistics/shop/export');
      expect([...download.searchParams]).toEqual([['format', format]]);
    }
  });
});
