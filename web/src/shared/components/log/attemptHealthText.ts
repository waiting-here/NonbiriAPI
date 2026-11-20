import type { TFunction } from 'i18next';
import { failureReasonKey } from '@shared/observability/failure';
import type { AdminLogAttempt, StewardLogAttempt } from './data';
import type { AttemptBackoff, AttemptHealthImpact } from './managementFailures';

export const STREAK_KEYS: Record<AttemptHealthImpact['streak_effect'], string> = {
  incremented: 'common.operations.logs.healthImpact.streak.incremented',
  reset: 'common.operations.logs.healthImpact.streak.reset',
  unchanged: 'common.operations.logs.healthImpact.streak.unchanged',
  pending: 'common.operations.logs.healthImpact.streak.pending',
  unknown: 'common.operations.logs.healthImpact.streak.unknown',
};

export const SCOPE_KEYS: Record<AttemptBackoff['scope'], string> = {
  key: 'common.operations.logs.healthImpact.backoff.scope.key',
  service: 'common.operations.logs.healthImpact.backoff.scope.service',
  model: 'common.operations.logs.healthImpact.backoff.scope.model',
  unknown: 'common.operations.logs.healthImpact.backoff.scope.unknown',
};

export function backoffReasonKey({ reason, period }: AttemptBackoff): string {
  switch (reason) {
    case 'retry_header':
      return 'common.operations.logs.healthImpact.backoff.retryHeader';
    case 'smart_trial':
      return 'common.operations.logs.healthImpact.backoff.smartTrial';
    case 'smart_quota':
      return `common.smartBackoff.reasons.${period ? `${period}_quota` : 'quota'}`;
    default:
      return (
        failureReasonKey(reason) ?? 'common.operations.logs.healthImpact.backoff.reasonUnknown'
      );
  }
}

export function continuedAfterAttempt(impact?: AttemptHealthImpact): boolean {
  return (
    impact?.streak_effect === 'unchanged' ||
    impact?.streak_effect === 'reset' ||
    (impact?.streak_effect === 'incremented' &&
      impact.streak_before !== undefined &&
      impact.streak_after !== undefined)
  );
}
export function attemptHealthSummary(
  attempt: AdminLogAttempt | StewardLogAttempt,
  t: TFunction,
  time: (at: number) => string,
) {
  const impact = attempt.health_impact;
  const backoff = impact?.backoff;
  const disabled = attempt.failure_disablement;
  const parts: string[] = [];
  if (disabled) {
    parts.push(
      t('common.operations.logs.failureDisablement'),
      t('common.operations.logs.failureStreak', {
        before: disabled.streak_before,
        after: disabled.streak_after,
        threshold: disabled.threshold,
      }),
    );
  } else {
    parts.push(t(STREAK_KEYS[impact?.streak_effect ?? 'unknown']));
    if (impact?.streak_before !== undefined && impact.streak_after !== undefined) {
      parts.push(impact.streak_before + ' → ' + impact.streak_after);
    }
    if (continuedAfterAttempt(impact)) {
      parts.push(t('common.operations.logs.healthImpact.didNotDisable'));
    }
  }
  if (backoff) {
    parts.push(
      t(
        backoff.observed_only
          ? 'common.operations.logs.healthImpact.backoff.observed'
          : 'common.operations.logs.healthImpact.backoff.triggered',
      ),
      t(backoffReasonKey(backoff)),
      t(SCOPE_KEYS[backoff.scope]),
    );
    if (backoff.retry_at !== undefined) parts.push(time(backoff.retry_at));
  }
  return parts.join(' · ');
}
