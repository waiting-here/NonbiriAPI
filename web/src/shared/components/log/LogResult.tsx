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

export function LogResultBadge({ row }: { row: RoleLogRow }) {
  const { t } = useTranslation();
  const result = logResult(row);
  const prefix =
    row.role === 'user' ? 'user.logs.result.' : 'common.operations.logs.presentation.result.';
  return (
    <span className="log-result">
      <span className={`nb-badge nb-badge--${result.tone}`}>
        {t(prefix + result.key, { status: row.caller_status ?? '—' })}
      </span>
      {row.role !== 'user' && row.caller_error_code ? (
        <span className="nb-sub mono">{row.caller_error_code}</span>
      ) : null}
    </span>
  );
}
