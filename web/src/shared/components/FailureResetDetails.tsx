import { useTranslation } from 'react-i18next';
import { NATIVE_SERVICE_LABEL_KEYS } from '@shared/nativeServices';
import { formatDateTime } from '@shared/utils/datetime';
import { formatCount } from '@shared/utils/formatNumber';
import {
  FAILURE_RESET_REASON_KEYS,
  type ServiceFailureReset,
} from '@shared/operations/failureReset';

export function FailureResetDetails({ services }: { services: ServiceFailureReset[] }) {
  const { t } = useTranslation();
  if (!services.length) return null;
  return (
    <ul className="nb-small">
      {services.map((service) => (
        <li key={service.use_id}>
          {t(NATIVE_SERVICE_LABEL_KEYS[service.native_service])}
          {' · '}
          {t(
            service.status === 'reset'
              ? 'common.failureReset.serviceRecovered'
              : service.status === 'already_clear'
                ? 'common.failureReset.alreadyClear'
                : 'common.failureReset.ineligible',
            { failures: formatCount(service.failure_streak_before).display },
          )}
          {service.status === 'reset' && service.failure_disabled_before
            ? ' · ' + t('common.failureReset.failurePauseCleared')
            : null}
          {service.reason
            ? ' · ' +
              t(FAILURE_RESET_REASON_KEYS[service.reason] ?? 'common.failureReset.ineligible')
            : null}
          {service.manually_disabled ? ' · ' + t('common.failureReset.manualRetained') : null}
          {service.retry_after_until
            ? ' · ' +
              t('common.failureReset.retryRetained', {
                time: formatDateTime(service.retry_after_until),
              })
            : null}
          {service.backoff_retry_at
            ? ' · ' +
              t('common.failureReset.backoffRetained', {
                time: formatDateTime(service.backoff_retry_at),
              })
            : null}
        </li>
      ))}
    </ul>
  );
}
