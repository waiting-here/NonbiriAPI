import { ApiError } from '@shared/query/http';
import { decoded, idempotentOptions } from './api';
import { charityScopePath } from './charityScope';
import {
  amount,
  array,
  decimal,
  decimalID,
  integer,
  invalidResponse,
  nullableInteger,
  opaqueID,
  oneOf,
  record,
  string,
  type WireRecord,
} from './wire';

export type RecurringLimitRole = 'owner' | 'steward' | 'admin';
export type RecurringLimitMode = 'reset' | 'sliding';
export type RecurringLimitInterval = '1h' | '5h' | 'day' | 'week' | 'month';
export type RecurringLimitAlignment = 'first_success' | 'calendar';
export type RecurringLimitMetric =
  'calls' | 'tokens' | 'input_tokens' | 'output_tokens' | 'credits';
export type RecurringLimitState = 'limited' | 'waiting_first_success' | 'available';

export function isHourlyInterval(interval: RecurringLimitInterval): boolean {
  return interval === '1h' || interval === '5h';
}

export interface RecurringLimitRuleInput {
  id: string | null;
  mode: RecurringLimitMode;
  interval: RecurringLimitInterval;
  alignment: RecurringLimitAlignment | null;
  time_zone: string;
  week_starts_on: number | null;
  metric: RecurringLimitMetric;
  limit: string;
}

export interface RecurringLimitRuleView extends RecurringLimitRuleInput {
  used: string;
  reserved: string;
  remaining: string;
  state: RecurringLimitState;
  period_start: number | null;
  period_end: number | null;
  next_transition_at: number | null;
  effective_at?: number;
}

export interface RecurringLimitsResponse {
  donation_id: string;
  key_id: string;
  donation_revision: string;
  server_now: number;
  rules: RecurringLimitRuleView[];
}

export interface RecurringLimitsWriteReceipt {
  donation_id: string;
  key_id: string;
  donation_revision: string;
}

export interface RecurringLimitsWritePayload {
  expected_revision: string;
  rules: RecurringLimitRuleInput[];
}

const U128_MAX = (1n << 128n) - 1n;
const MIN_PERIOD_SECOND = -62_167_219_200;
const MAX_PERIOD_SECOND = 253_402_300_799;
const TIME_ZONE = /^[A-Za-z0-9_+/-]{1,64}$/;

function canonicalUnsignedDecimal(value: unknown, label: string): string {
  return decimal(value, label, { u128: true });
}

/** Credits are sent as credits with at most three fractional digits. */
function canonicalCredits(value: unknown, label: string): string {
  return amount(value, label, false, U128_MAX);
}

function wireTimeZone(value: unknown, label: string): string {
  const zone = string(value, label, { min: 1, max: 64, bytes: 64, ascii: true });
  if (!TIME_ZONE.test(zone) || zone === 'Local' || zone.startsWith('/') || zone.includes('..')) {
    invalidResponse(label);
  }
  return zone;
}

function periodSecond(value: unknown, label: string): number | null {
  return value === null ? null : integer(value, label, MIN_PERIOD_SECOND, MAX_PERIOD_SECOND);
}

function validateCombination(
  mode: RecurringLimitMode,
  interval: RecurringLimitInterval,
  alignment: RecurringLimitAlignment | null,
  weekStartsOn: number | null,
  label: string,
): void {
  if (mode === 'sliding') {
    if (alignment !== null || weekStartsOn !== null)
      invalidResponse(`${label} sliding combination`);
    return;
  }
  if (alignment === null || (isHourlyInterval(interval) && alignment === 'calendar')) {
    invalidResponse(`${label} reset combination`);
  }
  const weekCombination = alignment === 'calendar' && interval === 'week';
  if (weekCombination) {
    if (weekStartsOn === null || weekStartsOn < 1 || weekStartsOn > 7) {
      invalidResponse(`${label} week start`);
    }
  } else if (weekStartsOn !== null) {
    invalidResponse(`${label} week start`);
  }
}

function normalizeRuleInput(value: unknown, label: string): RecurringLimitRuleInput {
  const root = record(
    value,
    ['id', 'mode', 'interval', 'alignment', 'time_zone', 'week_starts_on', 'metric', 'limit'],
    label,
  );
  const id = root.id === null ? null : opaqueID(root.id, 'qlr_', `${label} id`);
  const mode = oneOf(root.mode, ['reset', 'sliding'] as const, `${label} mode`);
  const interval = oneOf(
    root.interval,
    ['1h', '5h', 'day', 'week', 'month'] as const,
    `${label} interval`,
  );
  const alignment =
    root.alignment === null
      ? null
      : oneOf(root.alignment, ['first_success', 'calendar'] as const, `${label} alignment`);
  const weekStartsOn = nullableInteger(root.week_starts_on, `${label} week start`, 1, 7);
  validateCombination(mode, interval, alignment, weekStartsOn, label);
  const metric = oneOf(
    root.metric,
    ['calls', 'tokens', 'input_tokens', 'output_tokens', 'credits'] as const,
    `${label} metric`,
  );
  const limit =
    metric === 'credits'
      ? canonicalCredits(root.limit, `${label} credit limit`)
      : canonicalUnsignedDecimal(root.limit, `${label} limit`);
  return {
    id,
    mode,
    interval,
    alignment,
    time_zone: wireTimeZone(root.time_zone, `${label} time zone`),
    week_starts_on: weekStartsOn,
    metric,
    limit,
  };
}

function metricValue(value: unknown, metric: RecurringLimitMetric, label: string): string {
  return metric === 'credits'
    ? canonicalCredits(value, label)
    : canonicalUnsignedDecimal(value, label);
}

export function normalizeRuleView(value: unknown, label: string): RecurringLimitRuleView {
  const required = [
    'id',
    'mode',
    'interval',
    'alignment',
    'time_zone',
    'week_starts_on',
    'metric',
    'limit',
    'used',
    'reserved',
    'remaining',
    'state',
    'period_start',
    'period_end',
    'next_transition_at',
  ];
  const root = record(
    value,
    [
      'id',
      'mode',
      'interval',
      'alignment',
      'time_zone',
      'week_starts_on',
      'metric',
      'limit',
      'used',
      'reserved',
      'remaining',
      'state',
      'period_start',
      'period_end',
      'next_transition_at',
      'effective_at',
    ],
    label,
    required,
  );
  const input = normalizeRuleInput(
    {
      id: root.id,
      mode: root.mode,
      interval: root.interval,
      alignment: root.alignment,
      time_zone: root.time_zone,
      week_starts_on: root.week_starts_on,
      metric: root.metric,
      limit: root.limit,
    },
    label,
  );
  const state = oneOf(
    root.state,
    ['limited', 'waiting_first_success', 'available'] as const,
    `${label} state`,
  );
  return {
    ...input,
    used: metricValue(root.used, input.metric, `${label} used`),
    reserved: metricValue(root.reserved, input.metric, `${label} reserved`),
    remaining: metricValue(root.remaining, input.metric, `${label} remaining`),
    state,
    period_start: periodSecond(root.period_start, `${label} period start`),
    period_end: periodSecond(root.period_end, `${label} period end`),
    next_transition_at: periodSecond(root.next_transition_at, `${label} next transition`),
    effective_at:
      root.effective_at === undefined
        ? undefined
        : integer(root.effective_at, `${label} effective time`, 0, MAX_PERIOD_SECOND),
  };
}

function normalizeRecurringLimits(value: unknown): RecurringLimitsResponse {
  const root = record(
    value,
    ['donation_id', 'key_id', 'donation_revision', 'server_now', 'rules'],
    'recurring limits response',
  );
  const rules = array(root.rules, 'recurring limits rules', 16).map((entry, index) =>
    normalizeRuleView(entry, `recurring limit rule ${index + 1}`),
  );
  const ids = rules.map((rule) => rule.id);
  if (new Set(ids).size !== ids.length || ids.some((id) => id === null)) {
    invalidResponse('recurring limits rule ids');
  }
  return {
    donation_id: decimalID(root.donation_id, 'recurring limits donation id'),
    key_id: decimalID(root.key_id, 'recurring limits key id'),
    donation_revision: decimal(root.donation_revision, 'recurring limits donation revision', {
      positive: true,
    }),
    server_now: integer(root.server_now, 'recurring limits server time', 0, MAX_PERIOD_SECOND),
    rules,
  };
}

function normalizeWritePayload(payload: RecurringLimitsWritePayload): RecurringLimitsWritePayload {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
    throw new ApiError('invalid_request', 'Recurring limits payload is invalid.', 400);
  }
  const root = record(payload, ['expected_revision', 'rules'], 'recurring limits payload');
  const expectedRevision = decimal(root.expected_revision, 'expected donation revision', {
    positive: true,
  });
  if (!Array.isArray(root.rules) || root.rules.length > 16) {
    throw new ApiError('invalid_request', 'A key can have at most 16 recurring rules.', 400);
  }
  const rules = root.rules.map((rule, index) =>
    normalizeRuleInput(rule, `recurring limit rule ${index + 1}`),
  );
  return { expected_revision: expectedRevision, rules };
}

export function recurringLimitsBase(role: RecurringLimitRole): string {
  if (role === 'admin') return '/admin/api';
  if (role === 'steward') return '/api/steward';
  if (role === 'owner') return '/api';
  throw new ApiError('invalid_request', 'Recurring limits role is invalid.', 400);
}

export function recurringLimitsPath(
  role: RecurringLimitRole,
  donationId: string,
  keyId: string,
): string {
  const donation = decimalID(donationId, 'recurring limits donation id');
  const key = decimalID(keyId, 'recurring limits key id');
  return `${recurringLimitsBase(role)}/donations/${encodeURIComponent(donation)}/keys/${encodeURIComponent(key)}/recurring-limits`;
}

export const recurringLimitsKeys = {
  root: (role: RecurringLimitRole, accountId: string) =>
    role === 'admin'
      ? (['admin', 'operations', 'recurring-limits', accountId] as const)
      : role === 'steward'
        ? (['user', 'operations', 'steward', 'recurring-limits', accountId] as const)
        : (['user', 'operations', 'owner', 'recurring-limits', accountId] as const),
  detail: (role: RecurringLimitRole, accountId: string, donationId: string, keyId: string) =>
    [...recurringLimitsKeys.root(role, accountId), donationId, keyId] as const,
};

/** Scope used for in-memory drafts; account changes must never reuse it. */
export function recurringLimitsDraftKey(
  role: RecurringLimitRole,
  accountId: string,
  donationId: string,
  keyId: string,
): string {
  return JSON.stringify([role, accountId, donationId, keyId]);
}

export function getRecurringLimits(
  role: RecurringLimitRole,
  donationId: string,
  keyId: string,
  signal?: AbortSignal,
  modelID?: string,
): Promise<RecurringLimitsResponse> {
  return decoded(
    charityScopePath(recurringLimitsPath(role, donationId, keyId), modelID),
    normalizeRecurringLimits,
    {
      signal,
    },
  );
}

export function putRecurringLimits(
  role: RecurringLimitRole,
  donationId: string,
  keyId: string,
  payload: RecurringLimitsWritePayload,
  idempotencyKey: string,
  modelID?: string,
): Promise<RecurringLimitsWriteReceipt> {
  if (role === 'owner') {
    throw new ApiError('forbidden', 'Recurring limits are read-only for key owners.', 403);
  }
  const body = normalizeWritePayload(payload);
  return decoded(
    charityScopePath(recurringLimitsPath(role, donationId, keyId), modelID),
    (value) => {
      const root = record(
        value,
        ['donation_id', 'key_id', 'donation_revision'],
        'recurring limits write receipt',
      );
      return {
        donation_id: decimalID(root.donation_id, 'recurring limits receipt donation id'),
        key_id: decimalID(root.key_id, 'recurring limits receipt key id'),
        donation_revision: decimal(root.donation_revision, 'recurring limits receipt revision', {
          positive: true,
        }),
      };
    },
    idempotentOptions(idempotencyKey, { method: 'PUT', json: body }),
  );
}

// Names kept as explicit aliases for callers that use update terminology.
export const updateRecurringLimits = putRecurringLimits;
export const saveRecurringLimits = putRecurringLimits;
export const normalizeRecurringLimitsResponse = normalizeRecurringLimits;
export const normalizeRecurringLimitRule = normalizeRuleView;

/** Validate a draft before rendering a save affordance. */
export function validateRecurringLimitsPayload(
  payload: RecurringLimitsWritePayload,
): string | undefined {
  try {
    normalizeWritePayload(payload);
    return undefined;
  } catch (error) {
    return error instanceof Error ? error.message : 'Recurring limits payload is invalid.';
  }
}

export type RecurringLimitsRecord = WireRecord;
