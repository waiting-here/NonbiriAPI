import type { GamePayment } from '../types';

export type DuelGame = 'bidding' | 'likes';
export type Seat = 0 | 1;
export type Pair<T> = readonly [T, T];
export interface Rates {
  readonly platform: number;
  readonly welfare: number;
  readonly thursday: number;
}
export interface Profile {
  readonly kind: 'anonymous' | 'public' | 'deleted';
  readonly displayName?: string;
  readonly avatarURL?: string | null;
}
export interface DuelMode {
  readonly enabled: boolean;
  readonly available: boolean;
  readonly ticket: string;
  readonly rates: Rates;
  readonly termsHash: string;
  readonly contentHash: string;
}
export interface DuelConfig {
  readonly enabled: boolean;
  readonly available: boolean;
  readonly modes: Readonly<Record<string, DuelMode>>;
}
export interface DuelLobbyContext {
  readonly config: DuelConfig;
  readonly wallets: { readonly balance: string; readonly gameBalance: string };
  readonly accepting: boolean;
  readonly refreshWallets?: () => void;
}
export interface Resolution<P> {
  readonly round: number;
  readonly startedAt: number;
  readonly endsAt: number;
  readonly summary: P;
}
export interface RoundStart<S> {
  readonly round: number;
  readonly startedAt: number;
  readonly events: S;
}
export interface DuelQueue<L> {
  readonly id: string;
  readonly revision: string;
  readonly mode: string;
  readonly deadline: number;
  readonly ticket: string;
  readonly payment: GamePayment;
  readonly termsHash: string;
  readonly loadout: L | null;
}
export interface DuelState<V, P, S> {
  readonly id: string;
  readonly game: DuelGame;
  readonly mode: string;
  readonly contentHash: string;
  readonly revision: string;
  readonly phaseSeq: string;
  readonly phase: 'joker' | 'bid' | 'plan' | 'settlement';
  readonly round: number;
  readonly deadline: number;
  readonly serverNow: number;
  readonly you: Seat;
  readonly locked: Pair<boolean>;
  readonly ticket: string;
  readonly rates: Rates;
  readonly payment: GamePayment;
  readonly profiles: Pair<Profile>;
  readonly view: V;
  readonly resolution: Resolution<P> | null;
  readonly roundStart: RoundStart<S> | null;
}
export interface DuelResult<V, P> {
  readonly id: string;
  readonly game: DuelGame;
  readonly mode: string;
  readonly terminalAt: number;
  readonly outcome: 'win' | 'loss' | 'draw' | 'system_cancelled';
  readonly reason:
    | 'rounds'
    | 'target'
    | 'double-overload'
    | 'limit'
    | 'surrender'
    | 'server_restart'
    | 'account_unavailable';
  readonly scores: Pair<number>;
  readonly payment: GamePayment;
  readonly refund: GamePayment;
  readonly prize: string;
  readonly rake: { readonly platform: string; readonly welfare: string; readonly thursday: string };
  readonly profiles: Pair<Profile>;
  readonly you: Seat;
  readonly view: V | null;
  readonly resolution: Resolution<P> | null;
}
export interface DuelHome<V, P, S, L> {
  readonly serverNow: number;
  readonly queue: DuelQueue<L> | null;
  readonly current: DuelState<V, P, S> | null;
  readonly latestResult: DuelResult<V, P> | null;
}
export interface DuelRound<V, F, S> {
  readonly round: number;
  readonly before: V;
  readonly after: V;
  readonly facts: F;
  readonly startEvents: S | null;
  readonly timeouts: Pair<boolean>;
}
export interface Page<T> {
  readonly items: readonly T[];
  readonly nextCursor: string | null;
}
export interface DuelDetail<V, P, S, A> {
  readonly result: DuelResult<V, P>;
  readonly contentHash: string;
  readonly ticket: string;
  readonly rates: Rates;
  readonly initial: V;
  readonly terminalActions: Pair<A | null>;
  readonly roundStartEvents: S | null;
}
export interface DuelCodec<V, F, P = never, S = never, L = never, A = never> {
  readonly game: DuelGame;
  readonly modes: readonly string[];
  readonly view: (value: unknown) => V;
  readonly facts: (value: unknown) => F;
  readonly presentation?: (value: unknown) => P;
  readonly presentationDuration?: (value: P) => number;
  readonly start?: (value: unknown) => S;
  readonly loadout?: (value: unknown) => L;
  readonly action?: (value: unknown) => A;
}
