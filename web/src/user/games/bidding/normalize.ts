import {
  booleanValue,
  enumValue,
  exactRecord,
  invalidResponse,
  safeInteger,
} from '../common/strict';
import { list, nullable, pair, seatValue } from '../common/duel/normalize';
import type { DuelCodec, Pair, Seat } from '../common/duel/types';

export const BIDDING_MODES = ['tier1', 'tier2', 'tier3'] as const;
export interface Reward {
  readonly round: number;
  readonly side: Seat;
  readonly rank: number;
  readonly multiplier: number;
  readonly status: 'pool' | 'awarded' | 'discarded';
  readonly owner: Seat | null;
}
export interface BiddingView {
  readonly dealer: Seat | null;
  readonly hands: Pair<readonly number[]>;
  readonly played: Pair<readonly number[]>;
  readonly rewards: readonly Reward[];
  readonly jokers: Pair<boolean>;
  readonly pool: number;
  readonly scores: Pair<number>;
  readonly selected: number | null;
}
export interface BiddingRound {
  readonly round: number;
  readonly bids: Pair<number>;
  readonly joker: Seat | null;
  readonly fresh: number;
  readonly carryBefore: number;
  readonly awardedTo: Seat | null;
  readonly awarded: number;
  readonly carryAfter: number;
  readonly discarded: number;
  readonly scores: Pair<number>;
  readonly rewards: readonly Reward[];
}
export type BiddingAction =
  | { readonly kind: 'bid'; readonly card: number }
  | { readonly kind: 'joker'; readonly use: boolean }
  | { readonly kind: 'pass' };
const rank = (value: unknown) => safeInteger(value, 1, 13, 'card');
const points = (value: unknown) => safeInteger(value, 0, 208, 'points');
function cards(value: unknown) {
  const result = list(value, 13, rank);
  if (new Set(result).size !== result.length) invalidResponse('duplicate card');
  return result;
}
function reward(value: unknown): Reward {
  const r = exactRecord(value, ['round', 'side', 'rank', 'multiplier', 'status', 'owner']);
  const status = enumValue(r.status, ['pool', 'awarded', 'discarded'], 'reward status');
  const owner = nullable(r.owner, seatValue);
  if ((status === 'awarded') !== (owner !== null)) invalidResponse('reward owner');
  return {
    round: safeInteger(r.round, 1, 13, 'reward round'),
    side: seatValue(r.side),
    rank: rank(r.rank),
    multiplier: safeInteger(r.multiplier, 1, 2, 'multiplier'),
    status,
    owner,
  };
}
export function biddingView(value: unknown): BiddingView {
  const r = exactRecord(value, [
    'dealer',
    'hand_remaining',
    'played',
    'rewards',
    'joker_available',
    'pool_points',
    'scores',
    'own_selected_card',
  ]);
  const hands = pair(r.hand_remaining, cards),
    played = pair(r.played, cards);
  for (const seat of [0, 1])
    if (
      hands[seat].length + played[seat].length !== 13 ||
      new Set([...hands[seat], ...played[seat]]).size !== 13
    )
      invalidResponse('hand partition');
  if (played[0].length !== played[1].length) invalidResponse('revealed rounds');
  const rewards = list(r.rewards, 26, reward);
  if (
    !rewards.length ||
    rewards.length % 2 ||
    new Set(rewards.map((card) => `${card.round}:${card.side}`)).size !== rewards.length
  )
    invalidResponse('reward pairs');
  return {
    dealer: nullable(r.dealer, seatValue),
    hands,
    played,
    rewards,
    jokers: pair(r.joker_available, (v) => booleanValue(v, 'joker')),
    pool: points(r.pool_points),
    scores: pair(r.scores, points),
    selected: nullable(r.own_selected_card, rank),
  };
}
export function biddingRound(value: unknown): BiddingRound {
  const r = exactRecord(value, [
    'round',
    'bids',
    'joker_used_by',
    'fresh_value',
    'carry_before',
    'awarded_to',
    'awarded_points',
    'carry_after',
    'discarded_points',
    'score_after',
    'resolved_rewards',
  ]);
  return {
    round: safeInteger(r.round, 1, 13, 'round'),
    bids: pair(r.bids, rank),
    joker: nullable(r.joker_used_by, seatValue),
    fresh: points(r.fresh_value),
    carryBefore: points(r.carry_before),
    awardedTo: nullable(r.awarded_to, seatValue),
    awarded: points(r.awarded_points),
    carryAfter: points(r.carry_after),
    discarded: points(r.discarded_points),
    scores: pair(r.score_after, points),
    rewards: list(r.resolved_rewards, 26, reward),
  };
}
export function biddingAction(value: unknown): BiddingAction {
  const r = exactRecord(value, ['kind'], ['card', 'use']);
  const kind = enumValue(r.kind, ['joker', 'bid', 'pass'], 'action');
  if (kind === 'pass') {
    exactRecord(value, ['kind']);
    return { kind };
  }
  if (kind === 'bid') {
    exactRecord(value, ['kind', 'card']);
    return { kind, card: rank(r.card) };
  }
  exactRecord(value, ['kind', 'use']);
  return { kind, use: booleanValue(r.use, 'use joker') };
}
export const biddingCodec: DuelCodec<
  BiddingView,
  BiddingRound,
  never,
  never,
  never,
  BiddingAction
> = {
  game: 'bidding',
  modes: BIDDING_MODES,
  view: biddingView,
  facts: biddingRound,
  action: biddingAction,
};
