import { useTranslation } from 'react-i18next';
import type { RoleLogRow } from './data';

export function logResult(
  row: Pick<
    RoleLogRow,
    'caller_result_class' | 'caller_status' | 'caller_error_code' | 'phase' | 'rejection_reason'
  >,
) {
  if (row.caller_result_class === 'success') return { key: 'success', tone: 'ok' } as const;
  if (row.caller_result_class === null) return { key: 'pending', tone: 'info' } as const;
  if (row.caller_result_class === 'cancelled') return { key: 'cancelled', tone: 'warn' } as const;
  const code = row.caller_error_code ?? row.rejection_reason;
  if (code === 'insufficient_credits')
    return { key: 'credits', tone: 'bad', hint: 'creditsHint' } as const;
  if (code === 'not_found' || code === 'unbound_model')
    return { key: 'missingModel', tone: 'bad' } as const;
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
  success: 'user.logs.result.success',
  pending: 'user.logs.result.pending',
  cancelled: 'user.logs.result.cancelled',
  credits: 'user.logs.result.credits',
  missingModel: 'user.logs.result.missingModel',
  rejected: 'user.logs.result.rejected',
  keyRejected: 'user.logs.result.keyRejected',
  rateLimited: 'user.logs.result.rateLimited',
  timeout: 'user.logs.result.timeout',
  upstream: 'user.logs.result.upstream',
  failed: 'user.logs.result.failed',
} as const;

const resultKeys = {
  success: 'common.operations.logs.presentation.result.success',
  pending: 'common.operations.logs.presentation.result.pending',
  cancelled: 'common.operations.logs.presentation.result.cancelled',
  credits: 'common.operations.logs.presentation.result.credits',
  missingModel: 'common.operations.logs.presentation.result.missingModel',
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
) {
  return (role === 'user' ? userResultKeys : resultKeys)[key];
}

export function LogResultBadge({ row }: { row: RoleLogRow }) {
  const { t } = useTranslation();
  const result = logResult(row);
  return (
    <span className="log-result">
      <span className={`nb-badge nb-badge--${result.tone}`}>
        {t(logResultCopyKey(row.role, result.key), { status: row.caller_status ?? '—' })}
      </span>
    </span>
  );
}
