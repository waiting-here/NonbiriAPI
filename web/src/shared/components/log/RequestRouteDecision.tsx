import { rejectionFieldLabel } from './rejectionField';
import { useTranslation } from 'react-i18next';
import { NATIVE_SERVICE_LABEL_KEYS, type NativeService } from '@shared/nativeServices';
import { failureReasonKey, isGoogleModelQuotaReason } from '@shared/observability/failure';
import { type RouteDecision } from './routeDecision';

const reasonLabels: Record<string, string> = {
  failure_disabled: 'logs.routing.failureDisabled',
  cooldown: 'logs.routing.cooldown',
  unavailable: 'logs.routing.unavailable',
  expired: 'logs.routing.expired',
  capacity: 'logs.routing.capacity',
  quota: 'logs.routing.quota',
  key_rate_limit: 'logs.routing.keyRateLimit',
  quota_storage_full: 'logs.routing.quotaStorageFull',
  routing_busy: 'logs.routing.routingBusy',
  caller_level: 'logs.routing.callerLevel',
  credential_unavailable: 'logs.routing.credentialUnavailable',
  dependency_unavailable: 'logs.routing.dependencyUnavailable',
  operation_unsupported: 'logs.routing.operationUnsupported',
  features_unsupported: 'logs.routing.featuresUnsupported',
  preparation_rejected: 'logs.routing.preparationRejected',
  continuation_mismatch: 'logs.routing.continuationMismatch',
  protocol_rejected: 'logs.routing.protocolRejected',
  eligible: 'logs.routing.eligible',
  no_bindings: 'logs.routing.noBindings',
};

export function RequestRouteDecision({ decision }: { decision: RouteDecision }) {
  const { t } = useTranslation();
  if (!decision.observations.length && !decision.truncated) return null;
  return (
    <section className="log-route" aria-label={t('logs.routing.title')}>
      <h3>{t('logs.routing.title')}</h3>
      <ol className="log-route__observations">
        {decision.observations.map((observation, index) => {
          const {
            native_service: service,
            reason,
            binding_id: binding,
            match,
            field,
          } = observation;
          const label =
            service && Object.hasOwn(NATIVE_SERVICE_LABEL_KEYS, service)
              ? t(NATIVE_SERVICE_LABEL_KEYS[service as NativeService])
              : service;
          return (
            <li key={index}>
              <div className="log-route__resource">
                {label && <strong>{label}</strong>}
                {match && (
                  <span>
                    {t(match === 'native' ? 'logs.routing.native' : 'logs.routing.conversion')}
                  </span>
                )}
                {binding && <span>{t('logs.routing.binding', { id: binding })}</span>}
                {observation.stage && (
                  <span>
                    {t(
                      observation.stage === 'claim'
                        ? 'logs.routing.claim'
                        : 'logs.routing.dispatch',
                    )}
                  </span>
                )}
              </div>
              <p>
                {isGoogleModelQuotaReason(reason)
                  ? t('logs.routing.googleQuotaCooldown', {
                      reason: t(
                        failureReasonKey(reason) ?? 'common.failure.reason.google_model_quota',
                      ),
                    })
                  : Object.hasOwn(reasonLabels, reason)
                    ? t(reasonLabels[reason])
                    : reason}
                {field && (
                  <>
                    {' '}
                    ·{' '}
                    <code>
                      {field.startsWith('messages[') ? rejectionFieldLabel(field, t) : field}
                    </code>
                  </>
                )}
              </p>
            </li>
          );
        })}
      </ol>
      {decision.truncated && <p className="nb-muted">{t('logs.routing.partial')}</p>}
    </section>
  );
}
