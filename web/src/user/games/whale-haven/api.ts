import { formatAmount } from '@shared/components/Amount';
import { createIdempotencyKey, gameRequest, isResponseUnknown } from '../common/request';
import type {
  CatchActionKind,
  DecorItemId,
  FeedItemId,
  GameSnapshot,
  NeighborFilterKind,
  NeighborStation,
  WhaleOutfitId,
} from './game/types';

export const base = '/api/games/whale-haven';
type Settings = { enabled: boolean; exchange_enabled: boolean; values: Record<string, number> };
export interface Profile {
  snapshot: GameSnapshot;
  settings: Settings;
  settings_revision: string;
  timezone_offset_minutes: number;
  social: {
    open: boolean;
    door: string;
    accept_work: boolean;
    away: null | {
      target: string;
      label: string;
      allow_hire: boolean;
      working: boolean;
      started_at_ms: number;
      work_ends_at_ms?: number;
    };
    guests: {
      neighbor_id: string;
      label: string;
      whale_name: string;
      whale_outfit: WhaleOutfitId;
      allow_hire: boolean;
      hire_blocked?: 'quota' | 'wallet_full';
      working: boolean;
      started_at_ms: number;
      work_ends_at_ms?: number;
    }[];
  };
}
export type Action =
  | { type: 'adopt'; name: string; open: boolean }
  | { type: 'rename'; name: string }
  | { type: 'buy_feed'; feedId: FeedItemId; quantity?: number }
  | { type: 'use_feed'; feedId: FeedItemId }
  | { type: 'toggle_decor'; decorId: DecorItemId }
  | { type: 'select_outfit'; outfitId: WhaleOutfitId }
  | { type: 'toggle_sleep'; forceSleep?: boolean }
  | { type: 'interact'; zone: 'head' | 'body'; streak: number }
  | { type: 'set_open'; open: boolean }
  | { type: 'send_visit'; target: string; allowHire: boolean; presetId?: string }
  | { type: 'toggle_pin_neighbor'; neighborId: string }
  | { type: 'catch_guest'; neighborId: string; action: CatchActionKind; presetId?: string }
  | { type: 'set_preference'; key: 'petRoamEnabled' | 'petLookEnabled'; enabled: boolean }
  | {
      type:
        | 'recall_home'
        | 'harvest_pearls'
        | 'toggle_auto_refill'
        | 'activate_aroma'
        | 'spin_conch_gacha'
        | 'chat';
    };
export type Neighbor = NeighborStation & { pinned: boolean };
export interface NeighborHouse {
  house: Neighbor;
  guests: {
    whaleName: string;
    whaleOutfit: WhaleOutfitId;
    stage: 'eating' | 'working';
    mine: boolean;
  }[];
}
export interface Quote {
  quantity: string;
  max_lots: string;
  settings_revision: string;
  profile_revision: string;
  source_pearls: string;
  gross_general_milli: string;
  platform_milli: string;
  welfare_milli: string;
  thursday_milli: string;
  net_general_milli: string;
  daily_source_remaining: string;
  general_wallet_cap_milli: string;
  wallet_cap_blocked: boolean;
}
export async function fetchProfile(signal?: AbortSignal) {
  return (await gameRequest<Profile>(base + '/profile', { signal })).data;
}
export async function searchNeighbors(q: string, filter: NeighborFilterKind, signal: AbortSignal) {
  const params = new URLSearchParams({ q, filter });
  return (
    await gameRequest<{ items: Neighbor[] }>(base + '/neighbors/search?' + params, { signal })
  ).data.items;
}
export async function fetchNeighborHouse(door: string, signal: AbortSignal) {
  const params = new URLSearchParams({ door });
  return (await gameRequest<NeighborHouse>(base + '/neighbors/house?' + params, { signal })).data;
}
async function post<T>(path: string, json: unknown): Promise<T> {
  const options = { method: 'POST' as const, json, idempotencyKey: createIdempotencyKey() };
  try {
    return (await gameRequest<T>(base + path, options)).data;
  } catch (error) {
    if (!isResponseUnknown(error)) throw error;
    return (await gameRequest<T>(base + path, options)).data;
  }
}
export function act(profile: Profile, action: Action) {
  return post<Profile>('/actions', {
    ...action,
    expected_profile_revision: String(profile.snapshot.revision),
    expected_settings_revision: profile.settings_revision,
  });
}
export async function getQuote(quantity: string, signal?: AbortSignal) {
  return (
    await gameRequest<Quote>(base + '/exchange/quote', {
      method: 'POST',
      json: { quantity },
      signal,
    })
  ).data;
}
export function exchange(quote: Quote) {
  return post<Profile>('/exchange', {
    quantity: quote.quantity,
    expected_settings_revision: quote.settings_revision,
    expected_profile_revision: quote.profile_revision,
  });
}
export function formatMilliCredits(value: string | number) {
  return formatAmount(String(value));
}
export function costWithCoupon(profile: Profile, basePrice: number) {
  if (!profile.snapshot.home.buffs?.wardrobeDiscountCoupons) return basePrice;
  return Math.max(
    1,
    Math.floor((basePrice * (10000 - profile.settings.values.coupon_discount_bp)) / 10000),
  );
}
