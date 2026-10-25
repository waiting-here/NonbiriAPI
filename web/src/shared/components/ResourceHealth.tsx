import { useTranslation } from 'react-i18next';
import { NATIVE_SERVICE_LABEL_KEYS } from '@shared/nativeServices';
import type { BindingAvailability, ServiceStatus } from '@shared/operations/resourceHealth';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { StatusBadge } from './States';
import './ResourceHealth.css';

const stateKeys: Record<string, string> = {
  available: 'common.resourceHealth.states.available',
  failure_disabled: 'common.resourceHealth.states.failure_disabled',
  rate_limited: 'common.resourceHealth.states.rate_limited',
  disabled: 'common.resourceHealth.states.disabled',
  review_required: 'common.resourceHealth.states.review_required',
  pending: 'common.resourceHealth.states.pending',
  suspended: 'common.resourceHealth.states.suspended',
  exhausted: 'common.resourceHealth.states.exhausted',
  expired: 'common.resourceHealth.states.expired',
  ended: 'common.resourceHealth.states.ended',
  model_disabled: 'common.resourceHealth.states.model_disabled',
  feature_disabled: 'common.resourceHealth.states.feature_disabled',
};

export function ServiceHealth({ statuses }: { statuses?: ServiceStatus[] }) {
  const { t } = useTranslation();
  if (!statuses?.length) return null;
  const attention = statuses.filter(
    (status) =>
      status.state !== 'available' || status.failure_streak !== '0' || status.smart_backoff,
  );
  return (
    <section className="resource-health" aria-label={t('common.resourceHealth.title')}>
      <p className="resource-health__summary">
        <strong>{t('common.resourceHealth.title')}</strong>
        <span className="muted">
          {attention.length
            ? t('common.resourceHealth.summary', {
                ready: statuses.filter((status) => status.state === 'available').length,
                total: statuses.length,
              })
            : t('common.resourceHealth.healthy', { count: statuses.length })}
        </span>
      </p>
      {attention.length ? (
        <ul className="resource-health__list">
          {attention.map((status) => (
            <ServiceHealthRow key={status.use_id} status={status} />
          ))}
        </ul>
      ) : null}
    </section>
  );
}

function ServiceHealthRow({ status }: { status: ServiceStatus }) {
  const { t } = useTranslation();
  const formatDateTime = useDateTimeFormatter();
  const backoff = status.smart_backoff;
  const until = Math.max(
    status.retry_after_until ?? 0,
    backoff?.state === 'waiting' ? backoff.retry_at : 0,
  );
  const manualRecovery = status.state === 'failure_disabled' || status.failure_disabled;
  return (
    <li className="resource-health__item">
      <div className="resource-health__heading">
        <strong>{t(NATIVE_SERVICE_LABEL_KEYS[status.native_service])}</strong>
        <StatusBadge
          active={status.state === 'available'}
          danger={manualRecovery}
          label={t(stateKeys[status.state] ?? 'common.resourceHealth.states.unavailable')}
        />
      </div>
      {status.failure_streak !== '0' ? (
        <p>{t('common.resourceHealth.failures', { count: status.failure_streak })}</p>
      ) : null}
      {manualRecovery ? <p>{t('common.resourceHealth.manualRecovery')}</p> : null}
      {status.state === 'disabled' ? <p>{t('common.resourceHealth.enableService')}</p> : null}
      {status.state === 'review_required' ? (
        <p>{t('common.resourceHealth.reviewRequired')}</p>
      ) : null}
      {status.state === 'unavailable' ? (
        <p>{t('common.resourceHealth.authorizationChanged')}</p>
      ) : null}
      {backoff ? (
        <p>
          {t('common.resourceHealth.backoff', {
            reason: t(`common.smartBackoff.reasons.${backoff.reason}`),
            state: t(`common.smartBackoff.states.${backoff.state}`),
          })}
        </p>
      ) : null}
      {until > 0 ? (
        <p>{t('common.resourceHealth.retryAt', { time: formatDateTime(until) })}</p>
      ) : null}
      {status.state === 'rate_limited' && !manualRecovery && backoff?.state === 'probing' ? (
        <p>{t('common.resourceHealth.probing')}</p>
      ) : null}
      {status.state === 'available' && backoff?.state === 'ready' ? (
        <p>{t('common.resourceHealth.ready')}</p>
      ) : null}
    </li>
  );
}

export function BindingHealth({ value }: { value?: BindingAvailability }) {
  const { t } = useTranslation();
  if (!value) return null;
  return (
    <div className="resource-health__binding">
      <StatusBadge
        active={value.state === 'available'}
        label={t(
          value.state === 'available'
            ? 'common.resourceHealth.bindingAvailable'
            : (stateKeys[value.state] ?? 'common.resourceHealth.states.unavailable'),
        )}
      />
      <ServiceHealth statuses={value.service_statuses} />
      {value.state === 'unavailable' &&
      (!value.service_statuses.length ||
        value.service_statuses.some((service) => service.state === 'available')) ? (
        <p className="muted">{t('common.resourceHealth.checkDonation')}</p>
      ) : null}
    </div>
  );
}
