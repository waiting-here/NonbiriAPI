import { useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { clearStationSession } from '@shared/charityManagement';
import { isApiError } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import { Card, EmptyState, ErrorState, LoadingState } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { LogDetailDrawer } from './LogDetailDrawer';
import { LogTable, type LogColumn } from './LogTable';
import { TokenBuckets } from './TokenBuckets';
import {
  adminLogExportPath,
  useRoleLogDetail,
  useRoleLogs,
  validateLogFilter,
  type LogFiltersValue,
  type LogRole,
  type RoleLogAttempt,
  type RoleLogDetail,
  type RoleLogRow,
} from './data';

interface Copy {
  title: string;
  empty: string;
  emptyBody: string;
  apply: string;
  clear: string;
  details: string;
  close: string;
  previous: string;
  next: string;
  page: string;
  filterInvalid: string;
  loadingDetail: string;
  attempts: string;
  noAttempts: string;
  exportCsv: string;
  exportJson: string;
}

const COPY: Record<'zh' | 'en', Copy> = {
  zh: {
    title: '请求日志', empty: '没有日志', emptyBody: '当前筛选范围内没有可见记录。', apply: '应用筛选', clear: '清除',
    details: '详情', close: '关闭', previous: '上一页', next: '下一页', page: '页', filterInvalid: '状态码或时间范围无效。',
    loadingDetail: '正在读取详情…', attempts: '上游尝试', noAttempts: '该投影不公开上游尝试。', exportCsv: '导出 CSV', exportJson: '导出 JSON',
  },
  en: {
    title: 'Request logs', empty: 'No logs', emptyBody: 'No visible records match the current filters.', apply: 'Apply filters', clear: 'Clear',
    details: 'Details', close: 'Close', previous: 'Previous', next: 'Next', page: 'Page', filterInvalid: 'The status code or time range is invalid.',
    loadingDetail: 'Loading details…', attempts: 'Upstream attempts', noAttempts: 'This projection does not expose upstream attempts.', exportCsv: 'Export CSV', exportJson: 'Export JSON',
  },
};

function datetimeUnix(value: string): number | undefined {
  if (!value) return undefined;
  const milliseconds = Date.parse(value);
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return undefined;
  return Math.floor(milliseconds / 1_000);
}

function resultLabel(row: RoleLogRow): string {
  return row.caller_result_class ?? 'pending';
}

function requestFrom(detail: RoleLogDetail): RoleLogRow {
  return detail.request;
}

function isFinalAuthorityError(error: unknown): boolean {
  return isApiError(error) && (error.code === 'unauthorized' || error.code === 'forbidden');
}

function AttemptTable({
  detail,
  copy,
  page,
  onPrevious,
  onNext,
}: {
  detail: RoleLogDetail;
  copy: Copy;
  page: number;
  onPrevious: () => void;
  onNext: (cursor: string) => void;
}) {
  if (!('attempts' in detail)) return <p>{copy.noAttempts}</p>;
  const attempts = detail.attempts;
  return (
    <div className="ops-stack">
      {attempts.data.length === 0 ? <p>{copy.noAttempts}</p> : (
        <div className="ops-table-scroll">
          <table className="ops-table">
            <caption>{copy.attempts}</caption>
            <thead><tr><th>#</th><th>Endpoint</th><th>Connector / model</th><th>Status</th><th>Usage</th><th>Diagnostic</th></tr></thead>
            <tbody>
              {attempts.data.map((attempt: RoleLogAttempt) => (
                <tr key={attempt.attempt_seq}>
                  <td className="mono">{attempt.attempt_seq}</td>
                  <td>
                    <span className="mono">{attempt.endpoint_base_url}</span>
                    {'endpoint_note' in attempt && (attempt.endpoint_note || attempt.key_note) ? (
                      <div className="table-note">{attempt.endpoint_note || '—'} / {attempt.key_note || '—'}</div>
                    ) : null}
                  </td>
                  <td>{attempt.connector_type}<br /><span className="mono">{attempt.upstream_model_id}</span></td>
                  <td>{attempt.status_code ?? '—'}<br /><span className="mono">{attempt.upstream_code ?? '—'}</span></td>
                  <td><TokenBuckets row={attempt.usage} /><span className="table-note">{attempt.usage.charge}</span></td>
                  <td className="mono">{attempt.diag ?? '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <CursorPagination
        page={page}
        nextCursor={attempts.next_cursor}
        onPrevious={onPrevious}
        onNext={onNext}
        labels={{ previous: copy.previous, next: copy.next, page: copy.page }}
      />
    </div>
  );
}

export function RoleLogPanel({
  role,
  language = 'en',
  enabled = true,
  onAuthorityLoss,
}: {
  role: LogRole;
  language?: string;
  enabled?: boolean;
  onAuthorityLoss?: () => void;
}) {
  const copy = COPY[language.toLowerCase().startsWith('zh') ? 'zh' : 'en'];
  const queryClient = useQueryClient();
  const pager = useCursorPager();
  const attemptPager = useCursorPager();
  const resetPager = pager.reset;
  const resetAttemptPager = attemptPager.reset;
  const [raw, setRaw] = useState<Record<string, string>>({});
  const [fromDraft, setFromDraft] = useState('');
  const [toDraft, setToDraft] = useState('');
  const [filter, setFilter] = useState<LogFiltersValue>({});
  const [validation, setValidation] = useState('');
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [revoked, setRevoked] = useState(false);
  const [revokedError, setRevokedError] = useState<unknown>(null);
  const authorityClosedRef = useRef(false);
  const observerEnabled = enabled && !revoked;
  const logs = useRoleLogs(role, pager.cursor, filter, 20, observerEnabled);
  const detail = useRoleLogDetail(role, selectedID, attemptPager.cursor, 50, observerEnabled);
  const authorityError = isFinalAuthorityError(logs.error)
    ? logs.error
    : isFinalAuthorityError(detail.error) ? detail.error : null;
  const authorityClosed = revoked || authorityError !== null;

  /* eslint-disable react-hooks/set-state-in-effect */
  useEffect(() => {
    if (!authorityError || authorityClosedRef.current) return;
    authorityClosedRef.current = true;
    setRevoked(true);
    setRevokedError(authorityError);
    setRaw({});
    setFromDraft('');
    setToDraft('');
    setFilter({});
    setValidation('');
    setSelectedID(null);
    resetPager();
    resetAttemptPager();
    clearStationSession(queryClient, role === 'admin' ? 'admin' : 'steward');
    onAuthorityLoss?.();
  }, [authorityError, onAuthorityLoss, queryClient, resetAttemptPager, resetPager, role]);

  useEffect(() => {
    setSelectedID(null);
    resetPager();
    resetAttemptPager();
  }, [resetAttemptPager, resetPager, role]);
  /* eslint-enable react-hooks/set-state-in-effect */

  const fields = useMemo(() => {
    const values: Array<{ key: string; label: string; max: number }> = [];
    if (role === 'user') values.push({ key: 'model', label: 'Model', max: 133 });
    if (role === 'admin') values.push({ key: 'user_id', label: 'User ID', max: 39 });
    if (role !== 'user') {
      values.push({ key: 'endpoint_base_url', label: 'Endpoint base URL', max: 512 });
      values.push({ key: 'upstream_model', label: 'Upstream model', max: 512 });
    }
    values.push({ key: 'error_code', label: 'Error code', max: 96 });
    values.push({ key: 'status', label: 'Caller status', max: 3 });
    return values;
  }, [role]);

  const apply = (event: FormEvent) => {
    event.preventDefault();
    setValidation('');
    const from = datetimeUnix(fromDraft);
    const to = datetimeUnix(toDraft);
    if ((fromDraft && from === undefined) || (toDraft && to === undefined) || (from !== undefined && to !== undefined && from >= to)
      || (raw.status?.trim() && !/^[1-5][0-9]{2}$/.test(raw.status.trim()))) {
      setValidation(copy.filterInvalid);
      return;
    }
    setFilter({ ...validateLogFilter(role, raw), ...(from === undefined ? {} : { from }), ...(to === undefined ? {} : { to }) });
    setSelectedID(null);
    pager.reset();
    attemptPager.reset();
  };

  const columns: LogColumn<RoleLogRow>[] = [
    { key: 'time', header: 'Time', render: (row) => formatDateTime(row.started_at) },
    { key: 'route', header: 'Route', render: (row) => <span className="mono">{row.route_kind}</span> },
    ...(role === 'user' ? [{ key: 'model', header: 'Model', render: (row: RoleLogRow) => 'model' in row ? row.model : '—' }] : []),
    ...(role === 'admin' ? [{ key: 'user', header: 'User', render: (row: RoleLogRow) => 'user_id' in row ? row.user_id ?? '—' : '—' }] : []),
    { key: 'result', header: 'Result', render: resultLabel },
    { key: 'status', header: 'Status', render: (row) => row.caller_status ?? '—' },
    { key: 'error', header: 'Error', render: (row) => <span className="mono">{row.caller_error_code ?? '—'}</span> },
    { key: 'usage', header: 'Usage', render: (row) => <TokenBuckets row={row.usage} /> },
    { key: 'charge', header: 'Charge', render: (row) => <span className="mono">{row.usage.charge}</span> },
  ];

  let detailBody: ReactNode = null;
  if (selectedID && detail.isPending) detailBody = copy.loadingDetail;
  else if (selectedID && detail.error) detailBody = <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />;

  const detailFields = detail.data ? [
    { label: 'Request', value: <span className="mono">{requestFrom(detail.data).id}</span> },
    { label: 'Route', value: <span className="mono">{requestFrom(detail.data).route_kind}</span> },
    { label: 'Caller result', value: `${resultLabel(requestFrom(detail.data))} / ${requestFrom(detail.data).caller_status ?? '—'}` },
    { label: 'Caller error', value: <span className="mono">{requestFrom(detail.data).caller_error_code ?? '—'}</span> },
    { label: 'Usage', value: <TokenBuckets row={requestFrom(detail.data).usage} /> },
    {
      label: copy.attempts,
      value: (
        <AttemptTable
          detail={detail.data}
          copy={copy}
          page={attemptPager.page}
          onPrevious={attemptPager.previous}
          onNext={attemptPager.next}
        />
      ),
    },
  ] : detailBody ? [{ label: copy.details, value: detailBody }] : [];

  if (authorityClosed) {
    return <Card><ErrorState error={authorityError ?? revokedError} /></Card>;
  }

  return (
    <Card className="ops-stack">
      <div className="card-title-row">
        <h2>{copy.title}</h2>
        {role === 'admin' ? (
          <div className="ops-actions">
            <a className="btn btn-secondary" href={adminLogExportPath(filter, 'csv')} download>{copy.exportCsv}</a>
            <a className="btn btn-secondary" href={adminLogExportPath(filter, 'json')} download>{copy.exportJson}</a>
          </div>
        ) : null}
      </div>
      <form className="ops-stack" onSubmit={apply}>
        <div className="ops-field-grid">
          {fields.map((field) => (
            <label key={field.key}>{field.label}
              <input
                value={raw[field.key] ?? ''}
                maxLength={field.max}
                onChange={(event) => setRaw((current) => ({ ...current, [field.key]: event.target.value }))}
              />
            </label>
          ))}
          <label>From<input type="datetime-local" value={fromDraft} onChange={(event) => setFromDraft(event.target.value)} /></label>
          <label>To<input type="datetime-local" value={toDraft} onChange={(event) => setToDraft(event.target.value)} /></label>
        </div>
        {validation ? <p className="field-error" role="alert">{validation}</p> : null}
        <div className="ops-actions">
          <button className="btn btn-primary" type="submit">{copy.apply}</button>
          <button className="btn btn-secondary" type="button" onClick={() => {
            setRaw({}); setFromDraft(''); setToDraft(''); setFilter({}); setValidation(''); setSelectedID(null); pager.reset(); attemptPager.reset();
          }}>{copy.clear}</button>
        </div>
      </form>
      {logs.isPending ? <LoadingState /> : logs.error ? <ErrorState error={logs.error} onRetry={() => void logs.refetch()} />
        : logs.data.data.length === 0 ? <EmptyState title={copy.empty} body={copy.emptyBody} /> : (
          <>
            <LogTable
              caption={copy.title}
              columns={columns}
              rows={logs.data.data}
              rowKey={(row) => row.id}
              actions={(row) => <button type="button" className="btn btn-secondary" onClick={() => {
                attemptPager.reset();
                setSelectedID(row.id);
              }}>{copy.details}</button>}
            />
            <CursorPagination page={pager.page} nextCursor={logs.data.next_cursor} onPrevious={pager.previous} onNext={pager.next}
              labels={{ previous: copy.previous, next: copy.next, page: copy.page }} />
          </>
        )}
      <LogDetailDrawer
        open={Boolean(selectedID)}
        onClose={() => {
          setSelectedID(null);
          attemptPager.reset();
        }}
        title={selectedID ? `${copy.details} ${selectedID}` : copy.details}
        fields={detailFields}
      />
    </Card>
  );
}
