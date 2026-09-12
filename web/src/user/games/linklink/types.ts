import type { LinkLinkSpec, GamePayment } from '../common/types';
import type { PublicIdentity } from '../common/strict';

export interface LinkLinkCoordinate {
  readonly row: number;
  readonly col: number;
}
export interface LinkLinkTile extends LinkLinkCoordinate {
  readonly tileKey: string;
  readonly removed: boolean;
}
export interface LinkLinkState {
  readonly opportunitiesInitial: number;
  readonly opportunitiesRemaining: number;
  readonly rulesVersion: number;
  readonly payment: GamePayment;
  readonly kind: 'active';
  readonly sessionID: string;
  readonly spec: LinkLinkSpec;
  readonly price: string;
  readonly revision: string;
  readonly board: {
    readonly rows: number;
    readonly cols: number;
    readonly tiles: readonly LinkLinkTile[];
  };
  readonly pairsRemoved: number;
  readonly totalPairs: number;
  readonly startedAt: number;
  readonly deadline: number;
  readonly serverNow: number;
}
export interface LinkLinkSummary {
  readonly opportunitiesInitial: number;
  readonly opportunitiesRemaining: number;
  readonly rulesVersion: number;
  readonly payment: GamePayment;
  readonly kind: 'summary';
  readonly sessionID: string;
  readonly spec: LinkLinkSpec;
  readonly price: string;
  readonly terminalReason: 'completed' | 'timed_out' | 'abandoned';
  readonly startedAt: number;
  readonly deadline: number;
  readonly terminalAt: number;
  readonly pairsRemoved: number;
  readonly totalPairs: number;
  readonly score: string | null;
}
export type LinkLinkCurrent = LinkLinkState | LinkLinkSummary | null;
export interface LinkLinkMatchResult {
  readonly result: LinkLinkState | LinkLinkSummary;
  readonly path: readonly LinkLinkCoordinate[] | null;
}
export interface LinkLinkMatchIntent {
  readonly sessionID: string;
  readonly expectedRevision: string;
  readonly first: LinkLinkCoordinate;
  readonly second: LinkLinkCoordinate;
  readonly idempotencyKey: string;
}

export interface LinkLinkHint {
  readonly first: LinkLinkCoordinate;
  readonly second: LinkLinkCoordinate;
  readonly path: readonly LinkLinkCoordinate[];
}
export interface LinkLinkHintResult {
  readonly result: LinkLinkState;
  readonly hint: LinkLinkHint | null;
  readonly reshuffled: boolean;
}
export interface LinkLinkHintIntent {
  readonly sessionID: string;
  readonly expectedRevision: string;
  readonly idempotencyKey: string;
}
export interface LinkLinkRank {
  readonly rank: string;
  readonly score: string;
  readonly achievedAt: number;
  readonly identity: PublicIdentity;
  readonly isMe: boolean;
}
export interface LinkLinkLeaderboard {
  readonly spec: LinkLinkSpec;
  readonly windowDays: 7 | 30;
  readonly windowStart: number;
  readonly asOf: number;
  readonly rows: readonly LinkLinkRank[];
  readonly me: LinkLinkRank | null;
}
