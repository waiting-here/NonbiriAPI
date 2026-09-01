import { useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
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
  type LogResultClass,
  type LogRole,
  type LogRouteKind,
  type RoleLogAttempt,
  type RoleLogDetail,
  type RoleLogRow,
} from './data';

const ROUTE_LABEL_KEYS: Record<LogRouteKind, string> = {
  openai_chat_completions: 'common.operations.logs.route.openaiChatCompletions',
  charity_chat_completions: 'common.operations.logs.route.charityChatCompletions',
  model_discovery: 'common.operations.logs.route.modelDiscovery',
};

const RESULT_LABEL_KEYS: Record<LogResultClass, string> = {
  success: 'common.operations.logs.resultValue.success',
  failed: 'common.operations.logs.resultValue.failed',
  cancelled: 'common.operations.logs.resultValue.cancelled',
};

function datetimeUnix(value: string): number | undefined {
  if (!value) return undefined;
  const milliseconds = Date.parse(value);
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return undefined;
  return Math.floor(milliseconds / 1_000);
}

function requestFrom(detail: RoleLogDetail): RoleLogRow {
  return detail.request;
}

function isFinalAuthorityError(error: unknown): boolean {
  return isApiError(error) && (error.code === 'unauthorized' || error.code === 'forbidden');
}

function AttemptTable({
  detail,
  page,
  onPrevious,
  onNext,
}: {
  detail: RoleLogDetail;
  page: number;
  onPrevious: () => void;
  onNext: (cursor: string) => void;
}) {
  const { t } = useTranslation();
  if (!('attempts' in detail)) return <p>{t('common.operations.logs.noAttempts')}</p>;
  const attempts = detail.attempts;
  return (
    <div className="ops-stack">
      {attempts.data.length === 0 ? <p>{t('common.operations.logs.noAttempts')}</p> : (
        <div className="ops-table-scroll">
          <table className="ops-table">
            <caption>{t('common.operations.logs.attempts')}</caption>
            <thead><tr><th>#</th><th>{t('logs.endpointBaseUrl')}</th><th>{t('common.operations.logs.connectorModel')}</th><th>{t('common.status')}</th><th>{t('logs.tokens')}</th><th>{t('logs.diag')}</th></tr></thead>
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
        labels={{ previous: t('common.previous'), next: t('common.next'), page: t('common.operations.logs.pageLabel') }}
      />
    </div>
  );
}

export function RoleLogPanel({
  role,
  enabled = true,
  onAuthorityLoss,
}: {
  role: LogRole;
  language?: string;
  enabled?: boolean;
  onAuthorityLoss?: () => void;
}) {
  const { t } = useTranslation();
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
    if (role === 'user') values.push({ key: 'model', label: t('common.model'), max: 133 });
    if (role === 'admin') values.push({ key: 'user_id', label: t('common.userId'), max: 39 });
    if (role !== 'user') {
      values.push({ key: 'endpoint_base_url', label: t('logs.endpointBaseUrl'), max: 512 });
      values.push({ key: 'upstream_model', label: t('logs.upstreamModel'), max: 512 });
    }
    values.push({ key: 'error_code', label: t('logs.errorCode'), max: 96 });
    values.push({ key: 'status', label: t('common.status'), max: 3 });
    return values;
  }, [role, t]);
  const routeLabel = (route: LogRouteKind) => t(ROUTE_LABEL_KEYS[route]);
  const resultLabel = (row: RoleLogRow) => row.caller_result_class
    ? t(RESULT_LABEL_KEYS[row.caller_result_class])
    : t('common.operations.logs.resultValue.pending');

  const apply = (event: FormEvent) => {
    event.preventDefault();
    setValidation('');
    const from = datetimeUnix(fromDraft);
    const to = datetimeUnix(toDraft);
    if ((fromDraft && from === undefined) || (toDraft && to === undefined) || (from !== undefined && to !== undefined && from >= to)
      || (raw.status?.trim() && !/^[1-5][0-9]{2}$/.test(raw.status.trim()))) {
      setValidation(t('common.operations.logs.filterInvalid'));
      return;
    }
    setFilter({ ...validateLogFilter(role, raw), ...(from === undefined ? {} : { from }), ...(to === undefined ? {} : { to }) });
    setSelectedID(null);
    pager.reset();
    attemptPager.reset();
  };

  const columns: LogColumn<RoleLogRow>[] = [
    { key: 'time', header: t('logs.time'), render: (row) => formatDateTime(row.started_at) },
    { key: 'route', header: t('logs.routeKind'), render: (row) => routeLabel(row.route_kind) },
    ...(role === 'user' ? [{ key: 'model', header: t('common.model'), render: (row: RoleLogRow) => 'model' in row ? row.model : '—' }] : []),
    ...(role === 'admin' ? [{ key: 'user', header: t('common.userId'), render: (row: RoleLogRow) => 'user_id' in row ? row.user_id ?? '—' : '—' }] : []),
    { key: 'result', header: t('common.operations.logs.result'), render: resultLabel },
    { key: 'status', header: t('common.status'), render: (row) => row.caller_status ?? '—' },
    { key: 'error', header: t('logs.error'), render: (row) => <span className="mono">{row.caller_error_code ?? '—'}</span> },
    { key: 'usage', header: t('logs.tokens'), render: (row) => <TokenBuckets row={row.usage} /> },
    { key: 'charge', header: t('common.operations.logs.charge'), render: (row) => <span className="mono">{row.usage.charge}</span> },
  ];

  let detailBody: ReactNode = null;
  if (selectedID && detail.isPending) detailBody = t('common.loading');
  else if (selectedID && detail.error) detailBody = <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />;

  const detailFields = detail.data ? [
    { label: t('common.operations.logs.request'), value: <span className="mono">{requestFrom(detail.data).id}</span> },
    { label: t('logs.routeKind'), value: routeLabel(requestFrom(detail.data).route_kind) },
    { label: t('common.operations.logs.callerResult'), value: `${resultLabel(requestFrom(detail.data))} / ${requestFrom(detail.data).caller_status ?? '—'}` },
    { label: t('common.operations.logs.callerError'), value: <span className="mono">{requestFrom(detail.data).caller_error_code ?? '—'}</span> },
    { label: t('logs.tokens'), value: <TokenBuckets row={requestFrom(detail.data).usage} /> },
    {
      label: t('common.operations.logs.attempts'),
      value: (
        <AttemptTable
          detail={detail.data}
          page={attemptPager.page}
          onPrevious={attemptPager.previous}
          onNext={attemptPager.next}
        />
      ),
    },
  ] : detailBody ? [{ label: t('logs.details'), value: detailBody }] : [];

  const title = role === 'admin'
    ? t('admin.logs.logsTitle')
    : role === 'user' ? t('user.logs.title') : t('common.operations.logs.stewardTitle');
  const empty = role === 'admin'
    ? t('admin.logs.noLogs')
    : role === 'user' ? t('user.logs.empty') : t('common.operations.logs.stewardEmpty');
  const emptyBody = role === 'admin'
    ? t('admin.logs.noLogsBody')
    : role === 'user' ? t('user.logs.emptyBody') : t('common.operations.logs.stewardEmptyBody');

  if (authorityClosed) {
    return <Card><ErrorState error={authorityError ?? revokedError} /></Card>;
  }

  return (
    <Card className="ops-stack">
      <div className="card-title-row">
        <h2>{title}</h2>
        {role === 'admin' ? (
          <div className="ops-actions">
            <a className="btn btn-secondary" href={adminLogExportPath(filter, 'csv')} download>{t('admin.logs.exportCsv')}</a>
            <a className="btn btn-secondary" href={adminLogExportPath(filter, 'json')} download>{t('admin.logs.exportJson')}</a>
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
          <label>{t('common.from')}<input type="datetime-local" value={fromDraft} onChange={(event) => setFromDraft(event.target.value)} /></label>
          <label>{t('common.to')}<input type="datetime-local" value={toDraft} onChange={(event) => setToDraft(event.target.value)} /></label>
        </div>
        {validation ? <p className="field-error" role="alert">{validation}</p> : null}
        <div className="ops-actions">
          <button className="btn btn-primary" type="submit">{t('common.applyFilter')}</button>
          <button className="btn btn-secondary" type="button" onClick={() => {
            setRaw({}); setFromDraft(''); setToDraft(''); setFilter({}); setValidation(''); setSelectedID(null); pager.reset(); attemptPager.reset();
          }}>{t('common.resetFilter')}</button>
        </div>
      </form>
      {logs.isPending ? <LoadingState /> : logs.error ? <ErrorState error={logs.error} onRetry={() => void logs.refetch()} />
        : logs.data.data.length === 0 ? <EmptyState title={empty} body={emptyBody} /> : (
          <>
            <LogTable
              caption={title}
              columns={columns}
              rows={logs.data.data}
              rowKey={(row) => row.id}
              actions={(row) => <button type="button" className="btn btn-secondary" onClick={() => {
                attemptPager.reset();
                setSelectedID(row.id);
              }}>{t('logs.details')}</button>}
            />
            <CursorPagination page={pager.page} nextCursor={logs.data.next_cursor} onPrevious={pager.previous} onNext={pager.next}
              labels={{ previous: t('common.previous'), next: t('common.next'), page: t('common.operations.logs.pageLabel') }} />
          </>
        )}
      <LogDetailDrawer
        open={Boolean(selectedID)}
        onClose={() => {
          setSelectedID(null);
          attemptPager.reset();
        }}
        title={selectedID ? `${t('logs.drawerTitle')} ${selectedID}` : t('logs.drawerTitle')}
        fields={detailFields}
      />
    </Card>
  );
}
