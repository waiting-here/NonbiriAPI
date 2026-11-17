import { useTranslation } from 'react-i18next';
import type { RoleLogRow } from './data';

export function logResult(
  row: Pick<
    RoleLogRow,
    | 'route_kind'
    | 'caller_result_class'
    | 'caller_status'
    | 'caller_error_code'
    | 'phase'
    | 'rejection_reason'
  >,
) {
  if (row.caller_result_class === 'success') {
    if (row.route_kind === 'model_discovery' && row.caller_status === 202)
      return { key: 'discoveryAccepted', tone: 'info', hint: 'discoveryAcceptedHint' } as const;
    return { key: 'success', tone: 'ok' } as const;
  }
  if (row.caller_result_class === null) return { key: 'pending', tone: 'info' } as const;
  if (row.caller_result_class === 'cancelled') return { key: 'cancelled', tone: 'warn' } as const;
  const code = row.caller_error_code ?? row.rejection_reason;
  if (code === 'insufficient_credits')
    return { key: 'credits', tone: 'bad', hint: 'creditsHint' } as const;
  if (code === 'unbound_model') return { key: 'noUsableKey', tone: 'bad' } as const;
  if (code === 'not_found') return { key: 'missingModel', tone: 'bad' } as const;
  if (row.phase === 'pre_handler') return { key: 'rejected', tone: 'bad' } as const;
  if (code === 'upstream') {
    if (row.caller_status === 401 || row.caller_status === 403)
      return { key: 'keyRejected', tone: 'bad', hint: 'keyHint' } as const;
    if (row.caller_status === 429)
      return { key: 'rateLimited', tone: 'bad', hint: 'rateHint' } as const;
    if (row.caller_status === 408 || row.caller_status === 504)
      return { key: 'timeout', tone: 'bad', hint: 'retryHint' } as const;
    return { key: 'upstream', tone: 'bad', hint: 'retryHint' } as const;
  }
  return { key: 'failed', tone: 'bad' } as const;
}

const userResultKeys = {
  discoveryAccepted: 'common.operations.logs.discoveryAccepted',
  success: 'user.logs.result.success',
  pending: 'user.logs.result.pending',
  cancelled: 'user.logs.result.cancelled',
  credits: 'user.logs.result.credits',
  missingModel: 'user.logs.result.missingModel',
  noUsableKey: 'user.logs.result.noUsableKey',
  rejected: 'user.logs.result.rejected',
  keyRejected: 'user.logs.result.keyRejected',
  rateLimited: 'user.logs.result.rateLimited',
  timeout: 'user.logs.result.timeout',
  upstream: 'user.logs.result.upstream',
  failed: 'user.logs.result.failed',
} as const;

const resultKeys = {
  discoveryAccepted: 'common.operations.logs.discoveryAccepted',
  success: 'common.operations.logs.presentation.result.success',
  pending: 'common.operations.logs.presentation.result.pending',
  cancelled: 'common.operations.logs.presentation.result.cancelled',
  credits: 'common.operations.logs.presentation.result.credits',
  missingModel: 'common.operations.logs.presentation.result.missingModel',
  noUsableKey: 'common.operations.logs.presentation.result.noUsableKey',
  rejected: 'common.operations.logs.presentation.result.rejected',
  keyRejected: 'common.operations.logs.presentation.result.keyRejected',
  rateLimited: 'common.operations.logs.presentation.result.rateLimited',
  timeout: 'common.operations.logs.presentation.result.timeout',
  upstream: 'common.operations.logs.presentation.result.upstream',
  failed: 'common.operations.logs.presentation.result.failed',
} as const;

export function logResultCopyKey(
  role: RoleLogRow['role'],
  key: ReturnType<typeof logResult>['key'],
  reason?: string | null,
) {
  if ((key === 'rejected' || key === 'failed') && reason && Object.hasOwn(platformResults, reason))
    return platformResults[reason];
  return (role === 'user' ? userResultKeys : resultKeys)[key];
}

const platformResults: Record<string, string> = {
  unauthorized: 'logs.platformResult.unauthorized',
  forbidden: 'logs.platformResult.forbidden',
  charity_suspended: 'logs.platformResult.charitySuspended',
  feature_disabled: 'logs.platformResult.featureDisabled',
  maintenance: 'logs.platformResult.maintenance',
  invalid_request: 'logs.platformResult.invalidRequest',
  user_rpm: 'logs.platformResult.userRate',
  global_rpm: 'logs.platformResult.siteRate',
  shared_rpm: 'logs.platformResult.sharedRate',
  concurrency: 'logs.platformResult.concurrency',
  content_too_short: 'logs.platformResult.contentTooShort',
  payload_too_large: 'logs.platformResult.payloadTooLarge',
  resource_limit_exceeded: 'logs.platformResult.resourceLimit',
  service_unavailable: 'logs.platformResult.serviceUnavailable',
};

export function LogResultBadge({ row }: { row: RoleLogRow }) {
  const { t } = useTranslation();
  const result = logResult(row);
  const field = row.rejection_detail?.field;
  const position = field && /^request\.fields\[(\d+)\]$/.exec(field);
  return (
    <span className="log-result">
      <span className={`nb-badge nb-badge--${result.tone}`}>
        {t(logResultCopyKey(row.role, result.key, row.rejection_reason ?? row.caller_error_code), {
          status: row.caller_status ?? '—',
        })}
      </span>
      {field && field !== 'request' && (
        <small className="log-result__field">
          {position ? t('logs.rejectionTopLevelField', { position: position[1] }) : field}
        </small>
      )}
    </span>
  );
}
