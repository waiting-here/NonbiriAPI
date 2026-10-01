import { decoded, idempotentOptions } from '@shared/operations/api';
import { invalidResponse } from '@shared/operations/wire';
import { apiFetch, type ApiRequestOptions } from '@shared/query/http';
import { base, decodePeriod, type Direction, type Period } from '@shared/lakenotes/api';
import { RULES_ID, type Profile, type Cast, type Action } from './rules';

export interface Wallet {
  general_milli: string;
  game_milli: string;
}
export interface EntryReceipt {
  period_id: string;
  period_revision: string;
  fee_milli: string;
  operation_id?: string;
  ledger_seq?: string;
  created_at: number;
}
export interface CastView {
  id: string;
  source_period_id: string;
  rules_id: string;
  generation: string;
  revision: string;
  ack_tick: number;
  phase: 'waiting' | 'playing' | 'success' | 'failed';
  paused: boolean;
  readonly: boolean;
  recovery_action?: 'resume' | 'closed';
  state: Cast;
  profile_revision: string;
}
export interface ProfileView {
  readonly: boolean;
  revision: string;
  rules_id: string;
  profile: Profile;
  wallet: Wallet;
  period: Period | null;
  entitlement: EntryReceipt | null;
  cast: CastView | null;
}
export interface CastResult {
  cast: CastView;
  profile: ProfileView;
}
export interface ControlInput {
  generation: string;
  expected_revision: string;
}
export interface CheckpointInput extends ControlInput {
  from_tick: number;
  to_tick: number;
  initial_held: boolean;
  edges: { tick: number; held: boolean }[];
}
export interface QuoteInput {
  direction: Direction;
  quantity: string;
  period_id: string;
}
export interface ExchangeInput extends QuoteInput {
  expected_period_revision: string;
  expected_profile_revision: string;
}
export interface Quote extends QuoteInput {
  period_revision: string;
  profile_revision: string;
  source_amount: string;
  target_amount: string;
  source_lot: string;
  target_lot: string;
  coins: string;
  wallet: Wallet;
}
export interface ActionInput extends Action {
  expected_profile_revision: string;
}

export function decodeCast(value: unknown): CastView {
  const cast = value as CastView;
  if (cast.rules_id !== RULES_ID) invalidResponse('rules identity');
  return cast;
}
export function decodeProfile(value: unknown): ProfileView {
  const profile = value as ProfileView;
  if (profile.rules_id !== RULES_ID) invalidResponse('rules identity');
  return {
    ...profile,
    period: profile.period === null ? null : decodePeriod(profile.period),
    cast: profile.cast === null ? null : decodeCast(profile.cast),
  };
}
export const decodeCastResult = (value: unknown): CastResult => {
  const result = value as CastResult;
  return { cast: decodeCast(result.cast), profile: decodeProfile(result.profile) };
};
export const getProfile = (options?: ApiRequestOptions) =>
  decoded(base + '/profile', decodeProfile, options);
export const getCast = (id: string, options?: ApiRequestOptions) =>
  decoded(base + '/casts/' + encodeURIComponent(id), decodeCastResult, options);
export const enter = (
  input: { period_id: string; expected_period_revision: string },
  key: string,
  options?: ApiRequestOptions,
) =>
  decoded(
    base + '/entry',
    (value) => {
      const result = value as { receipt: EntryReceipt; profile: ProfileView };
      return { ...result, profile: decodeProfile(result.profile) };
    },
    idempotentOptions(key, { ...options, method: 'POST', json: input }),
  );
export const act = (input: ActionInput, key: string, options?: ApiRequestOptions) =>
  decoded(
    base + '/actions',
    (value) => {
      const result = value as { profile: ProfileView; coin_delta: string };
      return { ...result, profile: decodeProfile(result.profile) };
    },
    idempotentOptions(key, { ...options, method: 'POST', json: input }),
  );
export const startCast = (
  input: { expected_profile_revision: string },
  key: string,
  options?: ApiRequestOptions,
) =>
  decoded(
    base + '/casts',
    decodeCastResult,
    idempotentOptions(key, { ...options, method: 'POST', json: input }),
  );
export const controlCast = (
  id: string,
  action: 'pause' | 'resume',
  input: ControlInput,
  key: string,
  options?: ApiRequestOptions,
) =>
  decoded(
    base + '/casts/' + encodeURIComponent(id) + '/' + action,
    decodeCastResult,
    idempotentOptions(key, { ...options, method: 'POST', json: input }),
  );
export const checkpoint = (
  id: string,
  input: CheckpointInput,
  key: string,
  options?: ApiRequestOptions,
) =>
  decoded(
    base + '/casts/' + encodeURIComponent(id) + '/checkpoint',
    decodeCastResult,
    idempotentOptions(key, { ...options, method: 'POST', json: input }),
  );
export interface ExchangeReceipt extends Quote {
  id: string;
  operation_id: string;
  ledger_seq: string;
  created_at: number;
}
export const getQuote = (input: QuoteInput, options?: ApiRequestOptions) =>
  apiFetch<Quote>(base + '/exchange/quote', { ...options, method: 'POST', json: input });
export const exchange = (input: ExchangeInput, key: string, options?: ApiRequestOptions) =>
  decoded(
    base + '/exchange',
    (value) => {
      const result = value as { receipt: ExchangeReceipt; profile: ProfileView };
      return { ...result, profile: decodeProfile(result.profile) };
    },
    idempotentOptions(key, { ...options, method: 'POST', json: input }),
  );

export const controlInput = (cast: CastView): ControlInput => ({
  generation: cast.generation,
  expected_revision: cast.revision,
});
export const terminalPhase = (phase: string) => phase === 'success' || phase === 'failed';
export const lakeKeys = (account: string) => ['user', 'lake-notes', account] as const;
