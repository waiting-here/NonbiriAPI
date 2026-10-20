import type { SmartBackoff } from '@shared/operations/smartBackoff';
import './FailurePolicyControl.css';
import { useTranslation } from 'react-i18next';
import { useDateTimeFormatter } from '@shared/utils/datetime';

const stateKeys = {
  waiting: 'common.smartBackoff.states.waiting',
  ready: 'common.smartBackoff.states.ready',
  probing: 'common.smartBackoff.states.probing',
} as const;
const reasonKeys = {
  five_hour_quota: 'common.smartBackoff.reasons.five_hour_quota',
  day_quota: 'common.smartBackoff.reasons.day_quota',
  week_quota: 'common.smartBackoff.reasons.week_quota',
  month_quota: 'common.smartBackoff.reasons.month_quota',
  quota: 'common.smartBackoff.reasons.quota',
} as const;
const sourceKeys = {
  builtin: 'common.smartBackoff.sources.builtin',
  custom_rule: 'common.smartBackoff.sources.custom_rule',
} as const;

export function KeyRateLimitTime({
  until,
  smartBackoff,
}: {
  until: number | null;
  smartBackoff?: SmartBackoff | null;
}) {
  const { t } = useTranslation();
  const formatDateTime = useDateTimeFormatter();
  return (
    <div className="smart-backoff-status">
      {until !== null ? (
        <p className="muted">
          {t('common.keyLimits.rateLimitedUntil', { time: '' })}
          <time className="smart-backoff-time" dateTime={new Date(until * 1000).toISOString()}>
            {formatDateTime(until)}
          </time>
        </p>
      ) : null}
      {smartBackoff ? (
        <>
          <p>{t(stateKeys[smartBackoff.state])}</p>
          <p className="muted">
            {t('common.smartBackoff.reasonSource', {
              reason: t(reasonKeys[smartBackoff.reason]),
              source: t(sourceKeys[smartBackoff.source]),
            })}
          </p>
          <p className="muted">
            {t('common.smartBackoff.retryAt', { time: '' })}
            <time
              className="smart-backoff-time"
              dateTime={new Date(smartBackoff.retry_at * 1000).toISOString()}
            >
              {formatDateTime(smartBackoff.retry_at)}
            </time>
          </p>
        </>
      ) : null}
    </div>
  );
}
