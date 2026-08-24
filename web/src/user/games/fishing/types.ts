import type { ApiError } from '@shared/query/http';

export const BAIT_IDS = ['worm', 'lure', 'premium'] as const;
export type BaitId = (typeof BAIT_IDS)[number];

export type FishingPhase =
  | 'idle'
  | 'casting'
  | 'waiting'
  | 'reeling'
  | 'settling'
  | 'result'
  | 'error';

export interface FishingBait {
  readonly id: BaitId;
  /** Canonical milli-credit amount from the server. */
  readonly price: string;
}

export interface FishingGameParams {
  readonly baits: readonly FishingBait[];
  readonly rtpPercent: { readonly standard: number; readonly premium: number };
  readonly treasureMultipliers: { readonly bottle: number; readonly clover: number; readonly shell: number };
}

export interface GameSummary {
  readonly id: string;
  readonly version: number;
  readonly enabled: boolean;
}

export interface FishingGameSummary extends GameSummary {
  readonly id: 'fishing';
  readonly version: 1;
  readonly params: FishingGameParams;
}

export interface GamesConfig {
  readonly masterEnabled: boolean;
  /** Canonical milli-credit string; never converted to a JS number. */
  readonly credits: string;
  readonly gameProfilePublic: boolean;
  readonly games: readonly GameSummary[];
}

export interface PendingRound {
  readonly roundId: string;
  readonly bait: BaitId;
  readonly price: string;
  readonly createdAt: number;
  readonly autoSettleAt: number;
}

export interface StartRoundResponse extends PendingRound {
  readonly gameId: 'fishing';
  readonly gameVersion: 1;
  readonly credits: string;
  readonly state: 'pending' | 'committed' | 'released';
  readonly idempotentReplay: boolean;
}

export interface FishingResult {
  readonly roundId: string;
  readonly gameId: 'fishing';
  readonly gameVersion: 1;
  readonly bait: BaitId;
  readonly price: string;
  readonly speciesKey: string;
  readonly tier: string;
  readonly sizeCm: number;
  readonly isJunk: boolean;
  readonly isTreasure: boolean;
  readonly meter: boolean;
  readonly creditsWon: string;
  readonly credits: string;
  readonly settledAt: number;
  readonly idempotentReplay?: boolean;
}

export interface FishingState {
  readonly pendingRound: PendingRound | null;
  readonly unrevealedResult: FishingResult | null;
  readonly hasMoreUnrevealed: boolean;
}

export interface LeaderboardEntry {
  readonly rank: number;
  readonly speciesKey?: string;
  readonly sizeCm?: number;
  readonly totalCredits?: string;
  /** Omitted by the server for anonymous rows. */
  readonly displayName?: string;
  /** Omitted by the server for anonymous rows. */
  readonly avatarUrl?: string;
  /** Only meaningful when displayName is present. */
  readonly level4Badge?: boolean;
  readonly isMe: boolean;
}

export interface Leaderboard {
  readonly board: 'single' | 'total';
  readonly windowStart: number | null;
  readonly entries: readonly LeaderboardEntry[];
  readonly me: LeaderboardEntry | null;
}

export interface FishingFlowState {
  readonly phase: FishingPhase;
  readonly roundId: string | null;
  readonly result: FishingResult | null;
  readonly error: ApiError | Error | null;
  readonly resultSource: 'settled' | 'recovered' | null;
}
