import { useTranslation } from 'react-i18next';
import { Button, Fold, Note } from '@shared/components/ui';
import { CopyValue } from '@shared/components/CopyValue';
import { FailureDetails } from '@shared/observability/FailureDiagnostics';
import { normalizeFailure, type FailureDiagnostics } from '@shared/observability/failure';
import { useDateTimeFormatter } from '@shared/utils/datetime';

export interface OperationFailure {
  id: string;
  operation: string;
  source_name: string;
  site_status: number;
  upstream_code?: string;
  saved: boolean;
  details: FailureDiagnostics;
}
export function decodeOperationFailure(value: unknown): OperationFailure | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const row = value as Record<string, unknown>;
  const details = normalizeFailure(row.details);
  if (!details || typeof row.id !== 'string' || typeof row.operation !== 'string') return null;
  return {
    id: row.id,
    operation: row.operation,
    source_name: typeof row.source_name === 'string' ? row.source_name : '',
    site_status: typeof row.site_status === 'number' ? row.site_status : 0,
    upstream_code: typeof row.upstream_code === 'string' ? row.upstream_code : undefined,
    saved: row.saved === true,
    details,
  };
}
const reasons = new Set([
  'upstream_authentication',
  'upstream_account_restricted',
  'upstream_rate_limited',
  'connection_changed',
  'connection_disconnected',
  'credential_expired',
  'credential_unreadable',
  'response_limit',
  'invalid_json',
  'json_type_mismatch',
  'credential_reflected',
  'driver_unconfigured',
  'endpoint_invalid',
]);
export function DriverFailure({
  failure,
  onRetry,
}: {
  failure: OperationFailure;
  onRetry?: () => void;
}) {
  const { t } = useTranslation();
  const format = useDateTimeFormatter();
  const code = failure.details.code ?? '';
  const reason = reasons.has(code)
    ? code
    : failure.details.category === 'timeout'
      ? 'timeout'
      : failure.details.category === 'network' || failure.details.category === 'tls'
        ? 'network'
        : 'unknown';
  return (
    <div className="nb-stack nb-stack--sm">
      <Note tone="warn">{t(`common.agent.diagnostic.reasons.${reason}`)}</Note>
      {onRetry ? (
        <Button type="button" onClick={onRetry}>
          {t('common.retry')}
        </Button>
      ) : null}
      <Fold title={t('common.agent.diagnostic.details')} lazy>
        <div className="nb-stack nb-stack--sm">
          <p>
            {failure.source_name} ·{' '}
            {t(`common.agent.diagnostic.operations.${failure.operation}`, {
              defaultValue: failure.operation,
            })}
          </p>
          <p>
            {format(failure.details.started_at_ms)} ·{' '}
            {t('common.agent.diagnostic.siteStatus', { status: failure.site_status })}
          </p>
          <p>
            {failure.details.headers_received
              ? t('common.agent.diagnostic.upstreamStatus', {
                  status: failure.details.upstream_status,
                })
              : t('common.agent.diagnostic.noResponse')}
          </p>
          <p className="mono">{failure.id}</p>
          {failure.upstream_code ? <p className="mono">{failure.upstream_code}</p> : null}
          {!failure.saved ? <p>{t('common.agent.diagnostic.notSaved')}</p> : null}
          <FailureDetails failure={failure.details} />
          <CopyValue
            value={JSON.stringify(failure, null, 2)}
            label={t('common.agent.diagnostic.copy')}
            showValue={false}
          />
        </div>
      </Fold>
    </div>
  );
}
