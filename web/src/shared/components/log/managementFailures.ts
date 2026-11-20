import { NATIVE_SERVICES, type NativeService } from '@shared/nativeServices';

export const ATTEMPT_SEQ_PARAM = 'attempt_seq';

export function logAttemptSequence(value: unknown): string | undefined {
  return typeof value === 'string' && /^(?:[1-9][0-9]?|100)$/.test(value) ? value : undefined;
}

export interface AttemptFailureReason {
  reason: string;
  status_code?: number;
}

export interface FailureGroup extends AttemptFailureReason {
  count: number;
}

export interface FailureDisablement {
  connector_type: string;
  native_service?: NativeService;
  streak_before: string;
  streak_after: string;
  threshold: string;
}

const STREAK_EFFECTS = ['incremented', 'reset', 'unchanged', 'pending', 'unknown'] as const;
const BACKOFF_SCOPES = ['key', 'service', 'model', 'unknown'] as const;
const BACKOFF_PERIODS = ['five_hour', 'day', 'week', 'month'] as const;

export interface AttemptBackoff {
  reason?: string;
  scope: (typeof BACKOFF_SCOPES)[number];
  retry_at?: number;
  period?: (typeof BACKOFF_PERIODS)[number];
  observed_only: boolean;
}

export interface AttemptHealthImpact {
  streak_effect: (typeof STREAK_EFFECTS)[number];
  streak_before?: string;
  streak_after?: string;
  backoff?: AttemptBackoff;
}

export function normalizeAttemptFailureReason(
  row: Record<string, unknown>,
): AttemptFailureReason | undefined {
  const { reason, status_code: status } = row;
  if (typeof reason !== 'string' || !/^[a-z][a-z0-9_]{0,63}$/.test(reason)) return;
  return {
    reason,
    status_code:
      typeof status === 'number' && Number.isInteger(status) && status >= 100 && status <= 599
        ? status
        : undefined,
  };
}

export function normalizeFailureSummary(value: unknown): FailureGroup[] | undefined {
  if (!Array.isArray(value)) return;
  const groups: FailureGroup[] = [];
  for (const row of value) {
    if (
      !row ||
      typeof row !== 'object' ||
      typeof row.count !== 'number' ||
      !Number.isSafeInteger(row.count) ||
      row.count <= 0
    )
      continue;
    const failure = normalizeAttemptFailureReason(row);
    if (failure) groups.push({ ...failure, count: row.count });
  }
  return groups;
}

function canonicalCount(value: unknown): value is string {
  return typeof value === 'string' && /^(?:0|[1-9][0-9]{0,38})$/.test(value);
}

function normalizeAttemptBackoff(value: unknown): AttemptBackoff | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return;
  const row = value as Record<string, unknown>;
  const reason = normalizeAttemptFailureReason(row)?.reason;
  const scope = BACKOFF_SCOPES.find((item) => item === row.scope) ?? 'unknown';
  const retryAt = row.retry_at;
  const retry_at =
    typeof retryAt === 'number' &&
    Number.isSafeInteger(retryAt) &&
    retryAt > 0 &&
    retryAt <= 253_402_300_799
      ? retryAt
      : undefined;
  if (reason === undefined && scope === 'unknown' && retry_at === undefined) return;
  return {
    reason,
    scope,
    retry_at,
    period: BACKOFF_PERIODS.find((item) => item === row.period),
    observed_only: row.observed_only === true,
  };
}

export function normalizeAttemptHealthImpact(value: unknown): AttemptHealthImpact | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return;
  const row = value as Record<string, unknown>;
  return {
    streak_effect: STREAK_EFFECTS.find((item) => item === row.streak_effect) ?? 'unknown',
    streak_before: canonicalCount(row.streak_before) ? row.streak_before : undefined,
    streak_after: canonicalCount(row.streak_after) ? row.streak_after : undefined,
    backoff: normalizeAttemptBackoff(row.backoff),
  };
}

export function normalizeFailureDisablement(value: unknown): FailureDisablement | undefined {
  if (!value || typeof value !== 'object') return;
  const row = value as Record<string, unknown>;
  const { connector_type, streak_before, streak_after, threshold } = row;
  if (
    typeof connector_type !== 'string' ||
    !/^[a-z][a-z0-9-]{0,63}$/.test(connector_type) ||
    !canonicalCount(streak_before) ||
    !canonicalCount(streak_after) ||
    !canonicalCount(threshold)
  )
    return;
  return {
    connector_type,
    native_service: NATIVE_SERVICES.find((service) => service === row.native_service),
    streak_before,
    streak_after,
    threshold,
  };
}
