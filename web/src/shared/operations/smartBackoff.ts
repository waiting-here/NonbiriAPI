import { exactRecord, oneOf, unixSecond } from './wire';

export interface SmartBackoff {
  state: 'waiting' | 'ready' | 'probing';
  reason: 'five_hour_quota' | 'day_quota' | 'week_quota' | 'month_quota' | 'quota';
  source: 'builtin' | 'custom_rule';
  retry_at: number;
}
export interface SmartBackoffFields {
  smart_backoff: SmartBackoff | null;
}
export function normalizeSmartBackoff(value: unknown): SmartBackoff | null {
  if (value === null) return null;
  const item = exactRecord(value, ['state', 'reason', 'source', 'retry_at'], 'smart backoff');
  return {
    state: oneOf(item.state, ['waiting', 'ready', 'probing'] as const, 'smart backoff state'),
    reason: oneOf(
      item.reason,
      ['five_hour_quota', 'day_quota', 'week_quota', 'month_quota', 'quota'] as const,
      'smart backoff reason',
    ),
    source: oneOf(item.source, ['builtin', 'custom_rule'] as const, 'smart backoff source'),
    retry_at: unixSecond(item.retry_at, 'smart backoff retry time'),
  };
}
export function normalizeSmartBackoffFields(item: Record<string, unknown>): SmartBackoffFields {
  return { smart_backoff: normalizeSmartBackoff(item.smart_backoff) };
}
export function smartBackoffDeadline(value: SmartBackoff | null): number | null {
  return value?.state === 'waiting' ? value.retry_at : null;
}
