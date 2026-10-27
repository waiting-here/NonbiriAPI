import { describe, expect, it, vi } from 'vitest';
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
