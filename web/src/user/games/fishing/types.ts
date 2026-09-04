import type { PublicIdentity } from '../common/strict';
import type { Bait } from '../common/types';

export const FISHING_TIERS = [
  'small',
  'regular',
  'big',
  'giant',
  'legend',
  'junk',
  'treasure',
] as const;
export type FishingTier = (typeof FISHING_TIERS)[number];

export interface FishingOutcome {
  readonly ordinal: number;
  readonly speciesKey: string;
  readonly tier: FishingTier;
  readonly sizeCM: number;
  readonly reward: string;
}

export interface FishingBatchResult {
  readonly batchID: string;
  readonly bait: Bait;
  readonly count: 1 | 10;
  readonly unitPrice: string;
  readonly entryTotal: string;
  readonly outcomes: readonly FishingOutcome[];
  readonly payoutTotal: string;
  readonly balance: string;
  readonly settledAt: number;
  readonly idempotentReplay: boolean;
}

export interface FishingSettlementPending {
  readonly batchID: string;
  readonly bait: Bait;
  readonly count: 1 | 10;
  readonly entryTotal: string;
  readonly state: 'settlement_pending' | 'recovery_required';
  readonly nextAttemptAt: number | null;
  readonly retryExhausted: boolean;
}

export interface FishingState {
  readonly settlementPending: FishingSettlementPending | null;
  readonly unrevealed: FishingBatchResult | null;
  readonly hasMoreUnrevealed: boolean;
}

interface FishingLeaderboardBase {
  readonly rank: string;
  readonly identity: PublicIdentity;
  readonly isMe: boolean;
}

export interface FishingSingleRow extends FishingLeaderboardBase {
  readonly speciesKey: string;
  readonly sizeCM: number;
}

export interface FishingTotalRow extends FishingLeaderboardBase {
  readonly totalCredits: string;
}

export type FishingLeaderboard =
  | {
      readonly board: 'single';
      readonly windowStart: null;
      readonly entries: readonly FishingSingleRow[];
      readonly me: FishingSingleRow | null;
    }
  | {
      readonly board: 'total';
      readonly windowStart: number;
      readonly entries: readonly FishingTotalRow[];
      readonly me: FishingTotalRow | null;
    };

export interface FishingStartIntent {
  readonly bait: Bait;
  readonly count: 1 | 10;
  readonly idempotencyKey: string;
}

export type FishingStartResult = FishingBatchResult | FishingSettlementPending;
