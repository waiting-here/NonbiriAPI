import { array, exactRecord, invalidResponse, oneOf, string, unixSecond } from './wire';

export const SMART_BACKOFF_PROFILES = [
  'none',
  'five_hour_week',
  'five_hour_week_month',
  'day',
  'day_month',
] as const;
export type SmartBackoffProfile = (typeof SMART_BACKOFF_PROFILES)[number];
export const SMART_BACKOFF_PERIODS = ['auto', 'five_hour', 'day', 'week', 'month'] as const;
export type SmartBackoffPeriod = (typeof SMART_BACKOFF_PERIODS)[number];
export interface SmartBackoffRule {
  kind: 'code' | 'message';
  value: string;
  period: SmartBackoffPeriod;
}
export interface SmartBackoff {
  state: 'waiting' | 'ready' | 'probing';
  reason: 'five_hour_quota' | 'day_quota' | 'week_quota' | 'month_quota' | 'quota';
  source: 'builtin' | 'custom_rule';
  retry_at: number;
}
export interface SmartBackoffConfiguration {
  smart_backoff_profile: SmartBackoffProfile;
  smart_backoff_rules: SmartBackoffRule[];
}
export interface SmartBackoffFields extends SmartBackoffConfiguration {
  smart_backoff: SmartBackoff | null;
}
export const DEFAULT_SMART_BACKOFF: SmartBackoffConfiguration = {
  smart_backoff_profile: 'none',
  smart_backoff_rules: [],
};

export function smartBackoffPeriods(profile: SmartBackoffProfile): readonly SmartBackoffPeriod[] {
  switch (profile) {
    case 'five_hour_week':
      return ['auto', 'five_hour', 'week'];
    case 'five_hour_week_month':
      return ['auto', 'five_hour', 'week', 'month'];
    case 'day':
      return ['auto', 'day'];
    case 'day_month':
      return ['auto', 'day', 'month'];
    case 'none':
      return SMART_BACKOFF_PERIODS;
  }
}
export function normalizeSmartBackoffProfile(value: unknown): SmartBackoffProfile {
  return oneOf(value, SMART_BACKOFF_PROFILES, 'smart backoff profile');
}
export function normalizeSmartBackoffRules(
  value: unknown,
  profile: SmartBackoffProfile,
): SmartBackoffRule[] {
  return array(value, 'smart backoff rules', 8).map((item) => {
    const rule = exactRecord(item, ['kind', 'value', 'period'], 'smart backoff rule');
    const kind = oneOf(rule.kind, ['code', 'message'] as const, 'smart backoff rule kind');
    const text = string(rule.value, 'smart backoff rule value', { min: 1 });
    const invalidUnicode = Array.from(text).some((part) => {
      const point = part.codePointAt(0)!;
      return (point >= 127 && point <= 159) || (point >= 0xd800 && point <= 0xdfff);
    });
    if (
      text.trim() !== text ||
      invalidUnicode ||
      (kind === 'code' ? !/^[A-Za-z0-9._:-]{1,64}$/.test(text) : Array.from(text).length > 160)
    )
      invalidResponse('smart backoff rule value');
    return {
      kind,
      value: text,
      period: oneOf(rule.period, smartBackoffPeriods(profile), 'smart backoff rule period'),
    };
  });
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
  const profile = normalizeSmartBackoffProfile(item.smart_backoff_profile);
  return {
    smart_backoff_profile: profile,
    smart_backoff_rules: normalizeSmartBackoffRules(item.smart_backoff_rules, profile),
    smart_backoff: normalizeSmartBackoff(item.smart_backoff),
  };
}
export function smartBackoffInput(value: SmartBackoffConfiguration): SmartBackoffConfiguration {
  const profile = normalizeSmartBackoffProfile(value.smart_backoff_profile);
  return {
    smart_backoff_profile: profile,
    smart_backoff_rules: normalizeSmartBackoffRules(
      value.smart_backoff_rules.map((rule) => ({ ...rule, value: rule.value.trim() })),
      profile,
    ),
  };
}
export function validSmartBackoff(value: SmartBackoffConfiguration): boolean {
  try {
    smartBackoffInput(value);
    return true;
  } catch {
    return false;
  }
}
export function smartBackoffDeadline(value: SmartBackoff | null): number | null {
  return value?.state === 'waiting' ? value.retry_at : null;
}
