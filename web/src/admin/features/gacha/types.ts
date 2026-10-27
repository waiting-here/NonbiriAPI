import type { PageMetadata, PageSize } from '@shared/operations/pageNumbers';

export type PoolSort = 'recent' | 'draws';
export type CardSort = 'count' | 'rarity' | 'name';
export type CardRarity = 'N' | 'R' | 'SR' | 'SSR' | 'SP' | null;
export interface StatisticsWindow {
  page: string;
  pageSize: PageSize;
}
export interface PoolStatistics {
  id: string;
  name: string;
  name_en: string;
  kind: 'permanent' | 'limited';
  starts_at: number | null;
  ends_at: number | null;
  total_draws: string;
  participant_count: string;
  available: boolean;
}
export interface CardStatistics {
  card_id: string;
  name: string;
  name_en: string;
  rarity: CardRarity;
  count: string;
  participant_count: string;
  first_acquisition_count: string;
}
export interface RarityStatistics {
  rarity: CardRarity;
  count: string;
  first_acquisition_count: string;
}
export interface StatisticsPage<T> {
  items: T[];
  pagination: PageMetadata;
}
export interface PoolCardStatistics extends StatisticsPage<CardStatistics> {
  pool: PoolStatistics;
  rarity_totals: RarityStatistics[];
  top_cards: CardStatistics[];
}

export interface ShopStatisticsSummary {
  total_redemptions: string;
  participant_count: string;
}
export interface ShopCardStatistics extends StatisticsPage<CardStatistics> {
  summary: ShopStatisticsSummary;
  rarity_totals: RarityStatistics[];
  top_cards: CardStatistics[];
}
