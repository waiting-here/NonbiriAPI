import type {
  CardStatistics,
  RarityStatistics,
  ShopStatisticsSummary,
} from '../../src/admin/features/gacha/types';

export const shopStatisticsSummary: ShopStatisticsSummary = {
  total_redemptions: '9007199254741001',
  participant_count: '9007199254740997',
};
export const shopStatisticsCards: CardStatistics[] = [
  {
    card_id: 'shop-star',
    name: '商店初星',
    name_en: 'Shop star',
    rarity: 'SSR',
    count: '9007199254740993',
    participant_count: '9007199254740993',
    first_acquisition_count: '9007199254740993',
  },
  {
    card_id: 'limited-silver',
    name: '限定银月',
    name_en: 'Limited silver moon',
    rarity: 'SR',
    count: '7',
    participant_count: '7',
    first_acquisition_count: '7',
  },
  ...Array.from({ length: 29 }, (_, index) => ({
    card_id: 'shop-card-' + (index + 3),
    name: '可兑换卡牌 ' + (index + 3),
    name_en: 'Redeemable card ' + (index + 3),
    rarity: 'SP' as const,
    count: '0',
    participant_count: '0',
    first_acquisition_count: '0',
  })),
  {
    card_id: 'historical-card',
    name: '',
    name_en: '',
    rarity: null,
    count: '1',
    participant_count: '1',
    first_acquisition_count: '1',
  },
];
export const shopStatisticsRarities: RarityStatistics[] = [
  { rarity: 'SSR', count: '9007199254740993', first_acquisition_count: '9007199254740993' },
  { rarity: 'SR', count: '7', first_acquisition_count: '7' },
  { rarity: 'SP', count: '0', first_acquisition_count: '0' },
  { rarity: null, count: '1', first_acquisition_count: '1' },
];
