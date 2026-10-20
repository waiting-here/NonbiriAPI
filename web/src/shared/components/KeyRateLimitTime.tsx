import { useEffect, useState } from 'react';
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

interface RecoveryStatusProps {
  until: number | null;
  smartBackoff?: SmartBackoff | null;
}
export function KeyRateLimitTime(props: RecoveryStatusProps) {
  return <KeyRecoveryStatus key={props.until ?? 'none'} {...props} />;
}

function KeyRecoveryStatus({ until, smartBackoff }: RecoveryStatusProps) {
  const { t } = useTranslation();
  const formatDateTime = useDateTimeFormatter();
  const [headerActive, setHeaderActive] = useState(
    () => until !== null && until * 1000 > Date.now(),
  );
  useEffect(() => {
    if (until === null || until * 1000 <= Date.now()) return;
    let timer: ReturnType<typeof setTimeout>;
    const schedule = () => {
      const remaining = until * 1000 - Date.now();
      if (remaining <= 0) setHeaderActive(false);
      else timer = setTimeout(schedule, Math.min(remaining, 2_147_483_647));
    };
    schedule();
    return () => clearTimeout(timer);
  }, [until]);
  if (!headerActive && !smartBackoff) return null;
  return (
    <section className="smart-backoff-status" aria-label={t('common.smartBackoff.statusTitle')}>
      <dl className="smart-backoff-status__rows">
        {headerActive && until !== null ? (
          <div>
            <dt>{t('common.smartBackoff.headerTitle')}</dt>
            <dd>
              <p className="muted">{t('common.keyLimits.rateLimitedUntil', { time: '' })}</p>
              <time className="smart-backoff-time" dateTime={new Date(until * 1000).toISOString()}>
                {formatDateTime(until)}
              </time>
            </dd>
          </div>
        ) : null}
        {smartBackoff ? (
          <div>
            <dt>{t('common.smartBackoff.automaticTitle')}</dt>
            <dd>
              <strong>{t(stateKeys[smartBackoff.state])}</strong>
              <p className="muted">
                {t('common.smartBackoff.reasonSource', {
                  reason: t(reasonKeys[smartBackoff.reason]),
                  source: t(sourceKeys[smartBackoff.source]),
                })}
              </p>
              <p className="muted">{t('common.smartBackoff.retryAt', { time: '' })}</p>
              <time
                className="smart-backoff-time"
                dateTime={new Date(smartBackoff.retry_at * 1000).toISOString()}
              >
                {formatDateTime(smartBackoff.retry_at)}
              </time>
            </dd>
          </div>
        ) : null}
      </dl>
      {headerActive && smartBackoff ? (
        <p className="muted">{t('common.smartBackoff.independentHelp')}</p>
      ) : null}
    </section>
  );
}
