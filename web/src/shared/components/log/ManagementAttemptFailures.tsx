import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { GatewayConnectorLabel } from '@shared/components/GatewayConnectorLabel';
import { NATIVE_SERVICE_LABEL_KEYS } from '@shared/nativeServices';
import { failureReasonKey } from '@shared/observability/failure';
import { formatCount } from '@shared/utils/formatNumber';
import type { AttemptFailureReason, FailureDisablement, FailureGroup } from './managementFailures';

export function AttemptReason({ failure }: { failure: AttemptFailureReason }) {
  const { t } = useTranslation();
  return (
    <>
      {t(failureReasonKey(failure.reason) ?? 'common.failure.reason.attempt_failed')}
      {failure.status_code !== undefined && (
        <span className="nb-muted"> · HTTP {failure.status_code}</span>
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
    before: formatCount(observation.streak_before).display,
    after: formatCount(observation.streak_after).display,
    threshold: formatCount(observation.threshold).display,
  });
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
            <dd>{formatCount(group.count).display}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}
