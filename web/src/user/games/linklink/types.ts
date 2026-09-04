import type { LinkLinkSpec } from '../common/types';

export interface LinkLinkCoordinate {
  readonly row: number;
  readonly col: number;
}
export interface LinkLinkTile extends LinkLinkCoordinate {
  readonly tileKey: string;
  readonly removed: boolean;
}
export interface LinkLinkState {
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
export interface LinkLinkMatchIntent {
  readonly sessionID: string;
  readonly expectedRevision: string;
  readonly first: LinkLinkCoordinate;
  readonly second: LinkLinkCoordinate;
  readonly idempotencyKey: string;
}
