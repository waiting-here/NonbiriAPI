import { decoded, queryPath } from '@shared/operations/api';
import { normalizePageMetadata } from '@shared/operations/pageNumbers';
import {
  array,
  boolean,
  decimal,
  nullableUnixSecond,
  oneOf,
  optionalField,
  record,
  string,
} from '@shared/operations/wire';
import type {
  CardSort,
  CardStatistics,
  PoolCardStatistics,
  PoolSort,
  PoolStatistics,
  RarityStatistics,
  StatisticsPage,
  StatisticsWindow,
  ShopCardStatistics,
} from './types';

const root = '/admin/api/gacha/statistics/pools';
const rarity = (value: unknown) =>
  value === null ? null : oneOf(value, ['N', 'R', 'SR', 'SSR', 'SP'] as const, 'card rarity');
export function normalizePoolStatistics(value: unknown): PoolStatistics {
  const row = record(
    value,
    [
      'id',
      'name',
      'name_en',
      'kind',
      'starts_at',
      'ends_at',
      'total_draws',
      'participant_count',
      'available',
    ],
    'pool statistics',
    ['id', 'name', 'kind', 'total_draws', 'participant_count', 'available'],
  );
  return {
    id: string(row.id, 'pool id', { min: 1 }),
    name: string(row.name, 'pool name'),
    name_en: optionalField(row, 'name_en', (item) => string(item, 'pool English name')) ?? '',
    kind: oneOf(row.kind, ['permanent', 'limited'] as const, 'pool kind'),
    starts_at:
      optionalField(row, 'starts_at', (item) => nullableUnixSecond(item, 'pool start')) ?? null,
    ends_at: optionalField(row, 'ends_at', (item) => nullableUnixSecond(item, 'pool end')) ?? null,
    total_draws: decimal(row.total_draws, 'total draws'),
    participant_count: decimal(row.participant_count, 'draw participants'),
    available: boolean(row.available, 'pool configuration availability'),
  };
}
export function normalizeCardStatistics(value: unknown): CardStatistics {
  const row = record(
    value,
    [
      'card_id',
      'name',
      'name_en',
      'rarity',
      'count',
      'participant_count',
      'first_acquisition_count',
    ],
    'card statistics',
    ['card_id', 'name', 'rarity', 'count', 'participant_count', 'first_acquisition_count'],
  );
  return {
    card_id: string(row.card_id, 'card id', { min: 1 }),
    name: string(row.name, 'card name'),
    name_en: optionalField(row, 'name_en', (item) => string(item, 'card English name')) ?? '',
    rarity: rarity(row.rarity),
    count: decimal(row.count, 'card count'),
    participant_count: decimal(row.participant_count, 'card participants'),
    first_acquisition_count: decimal(row.first_acquisition_count, 'first acquisitions'),
  };
}
function normalizeRarityStatistics(value: unknown): RarityStatistics {
  const row = record(value, ['rarity', 'count', 'first_acquisition_count'], 'rarity statistics');
  return {
    rarity: rarity(row.rarity),
    count: decimal(row.count, 'rarity count'),
    first_acquisition_count: decimal(row.first_acquisition_count, 'rarity first acquisitions'),
  };
}
export function normalizePoolStatisticsPage(value: unknown): StatisticsPage<PoolStatistics> {
  const row = record(value, ['items', 'pagination'], 'pool statistics page');
  return {
    items: array(row.items, 'pools').map(normalizePoolStatistics),
    pagination: normalizePageMetadata(row.pagination),
  };
}
export function normalizePoolCardStatistics(value: unknown): PoolCardStatistics {
  const row = record(
    value,
    ['pool', 'items', 'pagination', 'rarity_totals', 'top_cards'],
    'pool card statistics',
  );
  return {
    pool: normalizePoolStatistics(row.pool),
    items: array(row.items, 'card statistics').map(normalizeCardStatistics),
    pagination: normalizePageMetadata(row.pagination),
    rarity_totals: array(row.rarity_totals, 'rarity totals').map(normalizeRarityStatistics),
    top_cards: array(row.top_cards, 'most drawn cards').map(normalizeCardStatistics),
  };
}
export function listPoolStatistics(
  window: StatisticsWindow,
  q: string,
  sort: PoolSort,
  signal?: AbortSignal,
) {
  return decoded(
    queryPath(root, { page: window.page, page_size: window.pageSize, q, sort }),
    normalizePoolStatisticsPage,
    { signal },
  );
}
export function listPoolCardStatistics(
  poolId: string,
  window: StatisticsWindow,
  q: string,
  sort: CardSort,
  signal?: AbortSignal,
) {
  return decoded(
    queryPath(root + '/' + encodeURIComponent(poolId) + '/cards', {
      page: window.page,
      page_size: window.pageSize,
      q,
      sort,
    }),
    normalizePoolCardStatistics,
    { signal },
  );
}
export function poolStatisticsExportURL(poolId: string, format: 'csv' | 'json'): string {
  return queryPath(root + '/' + encodeURIComponent(poolId) + '/export', { format });
}

const shopRoot = '/admin/api/gacha/statistics/shop';
export function normalizeShopCardStatistics(value: unknown): ShopCardStatistics {
  const row = record(
    value,
    ['summary', 'items', 'pagination', 'rarity_totals', 'top_cards'],
    'shop card statistics',
  );
  const summary = record(
    row.summary,
    ['total_redemptions', 'participant_count'],
    'shop statistics summary',
  );
  return {
    summary: {
      total_redemptions: decimal(summary.total_redemptions, 'total card redemptions'),
      participant_count: decimal(summary.participant_count, 'card redemption participants'),
    },
    items: array(row.items, 'shop card statistics').map(normalizeCardStatistics),
    pagination: normalizePageMetadata(row.pagination),
    rarity_totals: array(row.rarity_totals, 'shop rarity totals').map(normalizeRarityStatistics),
    top_cards: array(row.top_cards, 'most redeemed cards').map(normalizeCardStatistics),
  };
}
export function listShopCardStatistics(
  window: StatisticsWindow,
  q: string,
  sort: CardSort,
  signal?: AbortSignal,
) {
  return decoded(
    queryPath(shopRoot + '/cards', { page: window.page, page_size: window.pageSize, q, sort }),
    normalizeShopCardStatistics,
    { signal },
  );
}
export function shopStatisticsExportURL(format: 'csv' | 'json'): string {
  return queryPath(shopRoot + '/export', { format });
}
