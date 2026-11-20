import {
  backoffReasonKey,
  continuedAfterAttempt,
  SCOPE_KEYS,
  STREAK_KEYS,
} from './attemptHealthText';
import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { GatewayConnectorLabel } from '@shared/components/GatewayConnectorLabel';
import { useDisplayTimeContext } from '@shared/components/timeContextValue';
import { Note } from '@shared/components/ui/Note';
import { NATIVE_SERVICE_LABEL_KEYS } from '@shared/nativeServices';
import { failureReasonKey } from '@shared/observability/failure';
import { browserTimeZone, fixedOffsetZone } from '@shared/time';
import { formatDateTime } from '@shared/utils/datetime';
import { formatCount } from '@shared/utils/formatNumber';
import type { AdminLogAttempt, StewardLogAttempt } from './data';
import type {
  AttemptBackoff,
  AttemptFailureReason,
  AttemptHealthImpact,
  FailureDisablement,
  FailureGroup,
} from './managementFailures';

export function AttemptReason({ failure }: { failure: AttemptFailureReason }) {
  const { t } = useTranslation();
  return (
    <>
      {t(failureReasonKey(failure.reason) ?? 'common.failure.reason.attempt_failed')}
      {failure.status_code !== undefined && (
        <span className="nb-muted"> ·&nbsp;HTTP&nbsp;{failure.status_code}</span>
      )}
    </>
  );
}

export function AttemptService({
  observation,
}: {
  observation: Pick<FailureDisablement, 'connector_type' | 'native_service'>;
}) {
  const { t } = useTranslation();
  return (
    <>
      <GatewayConnectorLabel value={observation.connector_type} />
      {observation.native_service &&
        ` · ${t(NATIVE_SERVICE_LABEL_KEYS[observation.native_service])}`}
    </>
  );
}

export function FailureStreak({ observation }: { observation: FailureDisablement }) {
  const { t } = useTranslation();
  return t('common.operations.logs.failureStreak', {
    before: formatCount(observation.streak_before).exact,
    after: formatCount(observation.streak_after).exact,
    threshold: formatCount(observation.threshold).exact,
  });
}

function AttemptStreak({ impact }: { impact?: AttemptHealthImpact }) {
  const { t } = useTranslation();
  const effect = impact?.streak_effect ?? 'unknown';
  const before = impact?.streak_before;
  const after = impact?.streak_after;
  let counts: string | undefined;
  if (before !== undefined && after !== undefined) {
    counts = `${formatCount(before).exact} → ${formatCount(after).exact}`;
  } else if (before !== undefined) {
    counts = t('common.operations.logs.healthImpact.before', {
      count: formatCount(before).exact,
    });
  } else if (after !== undefined) {
    counts = t('common.operations.logs.healthImpact.after', { count: formatCount(after).exact });
  }
  return (
    <p className="log-attempt-streak">
      <strong>{t(STREAK_KEYS[effect])}</strong>
      {counts && <span>{counts}</span>}
      {continuedAfterAttempt(impact) && (
        <span className="nb-muted">{t('common.operations.logs.healthImpact.didNotDisable')}</span>
      )}
    </p>
  );
}

function AttemptRetryTime({ at }: { at: number }) {
  const displayContext = useDisplayTimeContext();
  const offset = displayContext.offset_minutes ?? 0;
  const context =
    displayContext.mode === 'site' ? { ...displayContext, offset_minutes: offset } : displayContext;
  const zone = context.mode === 'site' ? fixedOffsetZone(offset) : (browserTimeZone() ?? 'UTC');
  return (
    <time dateTime={new Date(at * 1000).toISOString()} className="log-attempt-retry-time">
      {formatDateTime(at, undefined, context, 'second')} <span>({zone})</span>
    </time>
  );
}

function AttemptWait({
  backoff,
  attempt,
}: {
  backoff: AttemptBackoff;
  attempt: AdminLogAttempt | StewardLogAttempt;
}) {
  const { t } = useTranslation();
  const service = attempt.native_service ?? attempt.failure_disablement?.native_service;
  const scopedService = (backoff.scope === 'service' || backoff.scope === 'model') && service;
  const scopedModel = backoff.scope === 'model' && attempt.upstream_model_id;
  return (
    <div className="log-attempt-backoff">
      <Note
        tone={backoff.observed_only ? 'info' : 'warn'}
        title={t(
          backoff.observed_only
            ? 'common.operations.logs.healthImpact.backoff.observed'
            : 'common.operations.logs.healthImpact.backoff.triggered',
        )}
      >
        <p>{t(backoffReasonKey(backoff))}</p>
        <p>
          {t(SCOPE_KEYS[backoff.scope])}
          {scopedModel && (
            <>
              {' '}
              · <bdi className="mono">{scopedModel}</bdi>
            </>
          )}
          {scopedService && ` · ${t(NATIVE_SERVICE_LABEL_KEYS[scopedService])}`}
        </p>
        {backoff.retry_at === undefined ? (
          <p>{t('common.operations.logs.healthImpact.backoff.timeUnknown')}</p>
        ) : (
          <p className="log-attempt-retry">
            <span>
              {t(
                backoff.observed_only
                  ? 'common.operations.logs.healthImpact.backoff.suggestedRetryAt'
                  : 'common.operations.logs.healthImpact.backoff.retryAt',
              )}
            </span>
            <AttemptRetryTime at={backoff.retry_at} />
          </p>
        )}
      </Note>
    </div>
  );
}

export function AttemptHealthEffects({
  attempt,
}: {
  attempt: AdminLogAttempt | StewardLogAttempt;
}) {
  return (
    <>
      {!attempt.failure_disablement && <AttemptStreak impact={attempt.health_impact} />}
      {attempt.health_impact?.backoff && (
        <AttemptWait attempt={attempt} backoff={attempt.health_impact.backoff} />
      )}
    </>
  );
}

export function AttemptFailureSummary({ groups }: { groups?: FailureGroup[] }) {
  const { t } = useTranslation();
  const headingId = useId();
  if (!groups?.length) return null;
  return (
    <section className="log-failure-summary" aria-labelledby={headingId}>
      <h3 id={headingId}>{t('common.operations.logs.failureSummary')}</h3>
      <dl>
        {groups.map((group) => (
          <div key={`${group.reason}:${group.status_code ?? ''}`}>
            <dt>
              <AttemptReason failure={group} />
            </dt>
            <dd>
              {t(
                group.count === 1
                  ? 'common.operations.logs.failureSummaryCount_one'
                  : 'common.operations.logs.failureSummaryCount_other',
                { amount: formatCount(group.count).display },
              )}
            </dd>
          </div>
        ))}
      </dl>
    </section>
  );
}
