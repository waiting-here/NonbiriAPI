import type {
  CardStatistics,
  PoolStatistics,
  StatisticsPage,
} from '../../src/admin/features/gacha/types';

export const statisticsPool: PoolStatistics = {
  id: 'permanent',
  name: '永续智能',
  name_en: 'Permanent pool',
  kind: 'permanent',
  starts_at: null,
  ends_at: null,
  total_draws: '9007199254740993',
  participant_count: '1234',
  available: true,
};
export const removedStatisticsPool: PoolStatistics = {
  ...statisticsPool,
  id: 'gp_removed',
  name: '已结束期次',
  name_en: 'Removed season',
  kind: 'limited',
  starts_at: 1780000000,
  ends_at: 1780600000,
  total_draws: '0',
  participant_count: '0',
  available: false,
};
export const statisticsCards: CardStatistics[] = [
  {
    card_id: 'card-a',
    name: '初星',
    name_en: 'First star',
    rarity: 'N',
    count: '9007199254740000',
    participant_count: '1234',
    first_acquisition_count: '17',
  },
  {
    card_id: 'card-b',
    name: '银月',
    name_en: 'Silver moon',
    rarity: 'SSR',
    count: '993',
    participant_count: '23',
    first_acquisition_count: '2',
  },
  ...Array.from({ length: 30 }, (_, index) => ({
    card_id: 'card-' + (index + 3),
    name: '卡牌 ' + (index + 3),
    name_en: 'Card ' + (index + 3),
    rarity: 'R' as const,
    count: '0',
    participant_count: '0',
    first_acquisition_count: '0',
  })),
];
export const statisticsRarities = [
  { rarity: 'N' as const, count: '9007199254740000', first_acquisition_count: '17' },
  { rarity: 'R' as const, count: '0', first_acquisition_count: '0' },
  { rarity: 'SSR' as const, count: '993', first_acquisition_count: '2' },
];
export function statisticsPage<T>(
  items: readonly T[],
  params = new URLSearchParams(),
): StatisticsPage<T> {
  const size = Number(params.get('page_size') ?? 20) as 10 | 20 | 50 | 100;
  const pages = Math.max(1, Math.ceil(items.length / size));
  const page = Math.min(Number(params.get('page') ?? 1), pages);
  return {
    items: items.slice((page - 1) * size, page * size),
    pagination: {
      page: String(page),
      page_size: size,
      total_items: String(items.length),
      total_pages: String(pages),
    },
  };
}
