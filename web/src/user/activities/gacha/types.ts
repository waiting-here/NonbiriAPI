export type DrawTier = 'N' | 'R' | 'SR' | 'SSR';
export type Rarity = DrawTier | 'SP';
export type Ticket = 'permanent' | 'limited';
export type Source = Ticket | 'shop';
export interface Card {
  id: string;
  theme: string;
  character: string;
  name: string;
  name_en: string;
  rarity: Rarity;
  source: Source;
  art: string;
  thumb: string;
  appearance?: string;
}
export interface Theme {
  id: string;
  name: string;
  name_en: string;
  rarity: Rarity;
  source: Source;
  tagline: string;
  tagline_en: string;
  appearance?: string;
  art: string;
}
export interface Rules {
  weights: [number, number, number, number];
  sr_pity: number;
  ssr_pity: number;
  sr_featured: number;
  ssr_featured: number;
  sr_guarantee: boolean;
  ssr_guarantee: boolean;
  ssr_guarantee_misses?: number;
}
export interface Pity {
  sr: number;
  ssr: number;
  sr_limit: number;
  ssr_limit: number;
  sr_featured: boolean;
  ssr_featured: boolean;
  ssr_misses?: number;
}
export interface Pool {
  id: string;
  name: string;
  name_en?: string;
  cover: string;
  kind: Ticket;
  group: string;
  rules: Rules;
  featured: string[];
  starts_at: number | null;
  ends_at: number | null;
  paused: boolean;
  published: boolean;
  revision: number;
  rates: [number, number, number, number];
}
export interface ShopSettings {
  permanent_price: string;
  limited_price: string;
  n_ticket: number;
  r_ticket: number;
  sr_ticket: number;
  ssr_tickets: number;
  permanent_cap: number;
  limited_cap: number;
  sr_card: number;
  ssr_card: number;
  sp_card: number;
  chibi_n: number;
  chibi_r: number;
  revision: number;
}
export interface DrawItem {
  card_id: string;
  duplicate: boolean;
  guarantee: 'none' | 'sr' | 'ssr' | 'newcomer';
}
export interface DrawReceipt {
  id: string;
  pool_id: string;
  pool_name: string;
  created_at: number;
  items: DrawItem[];
}
export interface Snapshot {
  visible_themes: string[];
  tickets: Record<Ticket, number>;
  fragments: Record<DrawTier, number>;
  credits: { general: string; game: string };
  owned: Record<string, { at: number; source: string }>;
  pity: Record<string, Pity>;
  newcomer: boolean;
  checked_in: boolean;
  claimed: string[];
  redeemed: string[];
  month_used: Record<Ticket, number>;
  donation: string;
  pools: Pool[];
  shop: ShopSettings;
  server_time: number;
  time_zone: string;
}
export type Action =
  | { action: 'enter' }
  | { action: 'check-in' }
  | { action: 'draw'; pool_id: string; quantity: 1 | 10 }
  | { action: 'buy'; ticket: Ticket; quantity: number; revision: number }
  | { action: 'redeem'; product: string; revision: number }
  | { action: 'claim'; theme: string };
export interface Outcome {
  snapshot: Snapshot;
  draw?: DrawReceipt;
  message?: string;
}
export interface HistoryPage {
  items: DrawReceipt[];
  next_cursor?: string;
}
export interface GachaPort {
  scope: string;
  load(): Promise<Snapshot>;
  act(input: Action, key: string): Promise<Outcome>;
  history(cursor?: string): Promise<HistoryPage>;
}
export interface Preferences {
  music: boolean;
  sound: boolean;
  reduced: boolean;
  vibration?: boolean;
}

export interface Preset {
  id: string;
  name: string;
  pool: Pool;
  revision: number;
}
export interface Configuration {
  entry_enabled: boolean;
  rules: Rules;
  shop: ShopSettings;
  pools: Pool[];
  presets: Preset[];
  revision: number;
  server_time: number;
  time_zone: string;
}
export type AdminInput =
  | { kind: 'settings'; expected_revision: number; rules: Rules; shop: ShopSettings; entry_enabled?: boolean }
  | { kind: 'pool'; expected_revision: number; pool: Pool }
  | { kind: 'preset'; expected_revision: number; preset: Preset };
