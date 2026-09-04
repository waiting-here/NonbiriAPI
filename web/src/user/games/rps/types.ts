import type { PublicIdentity } from '../common/strict';
import type { RPSMode, RPSModeConfig } from '../common/types';

export const RPS_PHASES = [
  'gesture',
  'dealer_raise',
  'followers',
  'paid_pool_gesture',
  'free_pool_gesture',
  'ultimate_gesture',
  'terminal_processing',
] as const;
export type RPSPhase = (typeof RPS_PHASES)[number];
export const GESTURES = ['rock', 'scissors', 'paper'] as const;
export type Gesture = (typeof GESTURES)[number];
export type RPSTerminalReason =
  | 'quick_resolved'
  | 'standard_round_limit'
  | 'standard_insufficient_balance'
  | 'deathmatch_balance_exhausted'
  | 'ultimate_resolved'
  | 'free_tie_limit';

export interface RPSQueue {
  readonly id: string;
  readonly mode: RPSMode;
  readonly state: 'waiting';
  readonly revision: string;
  readonly deadline: number;
  readonly serverNow: number;
}
export type FunSnapshot =
  | { readonly state: 'none' }
  | { readonly state: 'insufficient'; readonly completedCount: string }
  | {
      readonly state: 'full';
      readonly completedCount: string;
      readonly profitableCount: string;
      readonly rockCount: string;
      readonly scissorsCount: string;
      readonly paperCount: string;
    };

interface SeatMoney {
  readonly seatNo: number;
  readonly viewer: 'self' | 'opponent';
  readonly deletionState: 'active' | 'deletion_pending' | 'deidentified';
  readonly startingBalance: string;
  readonly currentBalance: string;
  readonly currentRoundInput: string;
  readonly currentAllIn: boolean;
  readonly totalInput: string;
  readonly totalReturned: string;
  readonly timeoutCount: string;
  readonly terminalReturn?: string;
  readonly walletNet?: string;
}
export interface ActiveRPSSeat extends SeatMoney {
  readonly deletionState: 'active';
  readonly displayName: string;
  readonly avatarURL: string | null;
  readonly funSnapshot: FunSnapshot;
  readonly visibleGesture?: Gesture;
  readonly followerAction?: 'call' | 'surrender';
}
export interface DeletedRPSSeat extends SeatMoney {
  readonly viewer: 'opponent';
  readonly deletionState: 'deletion_pending' | 'deidentified';
}
export type RPSSeat = ActiveRPSSeat | DeletedRPSSeat;

export interface RPSRecentEvent {
  readonly seq: string;
  readonly identityEpoch: string;
  readonly kind: string;
  readonly phaseSeq: string;
  readonly safePayload: Readonly<Record<string, unknown>>;
}
export interface RPSState {
  readonly sessionID: string;
  readonly mode: RPSMode;
  readonly state: 'started' | 'terminal_processing';
  readonly phase: RPSPhase;
  readonly phaseSeq: string;
  readonly revision: string;
  readonly identityEpoch: string;
  readonly serverNow: number;
  readonly deadline: number | null;
  readonly ruleSnapshot: {
    readonly rulesVersion: number;
    readonly base: string;
    readonly pumpsBP: {
      readonly platform: number;
      readonly welfare: number;
      readonly thursday: number;
    };
    readonly gestureSeconds: number;
    readonly dealerSeconds: number;
    readonly followerSeconds: number;
    readonly standardMultiplier: 5;
    readonly freeTieReminder: 3;
    readonly freeTieLimit: 6;
  };
  readonly economy: {
    readonly playerPool: string;
    readonly permanentMultiplier: string;
    readonly poolBaseMultiplier: string | null;
    readonly currentPlanMultiplier: string | null;
    readonly dealerRaise: string | null;
    readonly cuts: {
      readonly platform: string;
      readonly welfare: string;
      readonly thursday: string;
    };
    readonly welfareCarry: string;
  };
  readonly seats: readonly [RPSSeat, RPSSeat, RPSSeat];
  readonly currentActorOptions:
    | readonly []
    | readonly ['gesture']
    | readonly ['dealer_decision']
    | readonly ['follower_decision'];
  readonly roundSummary: {
    readonly baseRoundCount: string;
    readonly paidTieCount: string;
    readonly freeTieCount: string;
    readonly paidPoolStreak: string;
    readonly freePoolStreak: string;
    readonly reminderActive: boolean;
    readonly lastRevealResult: string | null;
  };
  readonly recentEvents: readonly RPSRecentEvent[];
  readonly eventsTruncated: boolean;
  readonly firstAvailableSeq: string;
}
export interface RPSPendingResult {
  readonly sessionID: string;
  readonly mode: RPSMode;
  readonly terminalReason: RPSTerminalReason;
  readonly ownSeatNo: number;
  readonly ownInput: string;
  readonly ownReturned: string;
  readonly ownWalletNet: string;
  readonly seats: readonly {
    readonly seatNo: number;
    readonly result: 'win' | 'loss' | 'tie' | 'deidentified';
  }[];
  readonly createdAt: number;
}
export type RPSHomeState =
  | {
      readonly kind: 'idle';
      readonly tutorialSeen: boolean;
      readonly modes: Readonly<Record<RPSMode, RPSModeConfig>>;
    }
  | { readonly kind: 'queue'; readonly queue: RPSQueue }
  | { readonly kind: 'session'; readonly session: RPSState }
  | { readonly kind: 'pending_result'; readonly result: RPSPendingResult };

export type RPSActionPayload =
  | { readonly action: 'gesture'; readonly payload: { readonly gesture: Gesture } }
  | {
      readonly action: 'dealer_decision';
      readonly payload:
        { readonly decision: 'no_raise' } | { readonly decision: 'raise'; readonly amount: string };
    }
  | {
      readonly action: 'follower_decision';
      readonly payload: { readonly decision: 'call' | 'surrender' };
    };
export interface RPSActionIntent {
  readonly sessionID: string;
  readonly phaseSeq: string;
  readonly expectedRevision: string;
  readonly identityEpoch: string;
  readonly idempotencyKey: string;
  readonly value: RPSActionPayload;
}

export type RPSLeaderboardRow =
  | {
      readonly board: 'profit_rate';
      readonly rank: string;
      readonly identity: PublicIdentity;
      readonly sessionCount: string;
      readonly profitableCount: string;
      readonly profitRate: string;
      readonly isMe: boolean;
    }
  | {
      readonly board: 'net_profit';
      readonly rank: string;
      readonly identity: PublicIdentity;
      readonly sessionCount: string;
      readonly netProfit: string;
      readonly isMe: boolean;
    };
export interface RPSLeaderboard {
  readonly mode: RPSMode;
  readonly board: 'profit_rate' | 'net_profit';
  readonly windowDays: 30;
  readonly windowStart: number;
  readonly minSessions: 10;
  readonly rows: readonly RPSLeaderboardRow[];
  readonly me: RPSLeaderboardRow | null;
}
