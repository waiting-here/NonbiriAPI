import { useCallback, useEffect, useMemo, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { CharityManagement } from '@shared/components/CharityManagement';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader, Pagination } from '@shared/components/States';
import { LogDetailDrawer, LogFilters, LogTable, TokenBuckets, useLogUrlState, type LogColumn, type LogDetailField, type LogFilterField } from '@shared/components/log';
import { CompactNumber } from '@shared/components/CompactNumber';
import { formatCount } from '@shared/utils/formatNumber';
import { formatDateTime } from '@shared/utils/datetime';
import { ApiError } from '@shared/query/http';
import { charityManagementKeys } from '@shared/charityManagement';
import { useStewardLogs, useUserSession, userKeys, type StewardLogFilter, type StewardRequestLog } from '../data';

const PAGE_SIZE_OPTIONS = [10, 20, 50] as const;
const DEFAULT_PAGE_SIZE = 20;
const TEXT_PARAMS = ['user_id', 'endpoint_base_url', 'upstream_model', 'error_code', 'status'] as const;

function buildFilter(filters: Record<string, string>): StewardLogFilter {
  const next: StewardLogFilter = {};
  const userId = filters.user_id?.trim();
  if (userId && /^\d+$/.test(userId)) next.userId = userId;
  const baseURL = filters.endpoint_base_url?.trim();
  if (baseURL) next.endpointBaseURL = baseURL.slice(0, 512);
  const upstream = filters.upstream_model?.trim();
  if (upstream) next.upstreamModel = upstream.slice(0, 256);
  const errorCode = filters.error_code?.trim();
  if (errorCode) next.errorCode = errorCode.slice(0, 96);
  const status = filters.status?.trim();
  if (status && /^\d{3}$/.test(status) && Number(status) >= 100 && Number(status) <= 599) next.status = status;
  return next;
}

export function StewardPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const session = useUserSession();
  const [section, setSection] = useState<'logs' | 'charity'>('logs');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [revokedSession, setRevokedSession] = useState<string | null>(null);
  const { state, patch } = useLogUrlState(TEXT_PARAMS, DEFAULT_PAGE_SIZE);
  const filter = useMemo(() => ({ ...buildFilter(state.filters), fromUnix: state.fromUnix, toUnix: state.toUnix }), [state.filters, state.fromUnix, state.toUnix]);
  const sessionKey = session.data ? `${session.data.user.id}:${session.data.user.effective_level}` : '';
  const logs = useStewardLogs(state.page, filter, state.pageSize, section === 'logs' && session.data?.user.effective_level === 5 && revokedSession !== sessionKey);

  const clearSensitive = useCallback((refreshSession = false) => {
    queryClient.removeQueries({ queryKey: userKeys.stewardLogsRoot });
    queryClient.removeQueries({ queryKey: charityManagementKeys.root('steward') });
    queryClient.removeQueries({ queryKey: userKeys.charityModels });
    queryClient.removeQueries({ queryKey: userKeys.donations });
    if (refreshSession) void queryClient.invalidateQueries({ queryKey: userKeys.session });
  }, [queryClient]);

  useEffect(() => {
    if (session.data?.user.effective_level !== 5) {
      setSelectedId(null); // eslint-disable-line react-hooks/set-state-in-effect
      setRevokedSession(null);
      clearSensitive();
    } else if (revokedSession && revokedSession !== sessionKey) {
      setRevokedSession(null);
    }
  }, [clearSensitive, revokedSession, session.data?.user.effective_level, sessionKey]);

  useEffect(() => {
    if (logs.error instanceof ApiError && (logs.error.status === 401 || logs.error.status === 403)) {
      setSelectedId(null); // eslint-disable-line react-hooks/set-state-in-effect
      setRevokedSession(sessionKey);
      clearSensitive(true);
    }
  }, [clearSensitive, logs.error, sessionKey]);

  if (session.isPending) return <LoadingState />;
  const authError = logs.error instanceof ApiError && (logs.error.status === 401 || logs.error.status === 403);
  const revoked = authError || (session.data?.user.effective_level === 5 && revokedSession === sessionKey);
  if (session.data?.user.effective_level !== 5 || revoked) {
    return <div className="page"><PageHeader eyebrow={t('app.name')} title={t('user.steward.title')} description={t('user.steward.description')} /><Card><p className="field-error" role="alert">{t('user.steward.accessDenied')}</p></Card></div>;
  }

  const applyFilters = (next: { filters: Record<string, string>; fromUnix?: number; toUnix?: number }) => patch({ ...next, page: 1 });
  const fields: LogFilterField[] = [
    { name: 'user_id', label: t('common.userId'), ariaLabel: t('common.filterUserIdAria'), inputType: 'number', maxLength: 19 },
    { name: 'endpoint_base_url', label: t('logs.endpointBaseUrl'), ariaLabel: t('logs.endpointBaseUrlAria'), maxLength: 512 },
    { name: 'upstream_model', label: t('logs.upstreamModel'), ariaLabel: t('logs.upstreamModelAria'), maxLength: 256 },
    { name: 'error_code', label: t('logs.errorCode'), ariaLabel: t('logs.errorCodeAria'), maxLength: 96 },
    { name: 'status', label: t('common.status'), ariaLabel: t('common.filterStatusAria'), inputType: 'number', maxLength: 3 },
  ];
  const displayResource = (row: StewardRequestLog, value: string) => row.route_kind === 'personal' ? (value || '—') : '—';
  const columns: LogColumn<StewardRequestLog>[] = [
    { key: 'started_at', header: t('logs.time'), render: (row) => <span title={formatDateTime(row.completed_at)}>{formatDateTime(row.started_at)}</span> },
    { key: 'user_id', header: t('common.userId'), render: (row) => <span className="mono">{row.user_id}</span> },
    { key: 'route_kind', header: t('logs.routeKind'), render: (row) => row.route_kind },
    { key: 'endpoint_base_url', header: t('logs.endpointBaseUrl'), render: (row) => <span className="mono read-only-value">{displayResource(row, row.endpoint_base_url)}</span> },
    { key: 'upstream_model_id', header: t('logs.upstreamModel'), render: (row) => <span className="mono">{displayResource(row, row.upstream_model_id)}</span> },
    { key: 'duration_ms', header: t('logs.duration'), render: (row) => `${formatCount(row.duration_ms).display} ms` },
    { key: 'tokens', header: t('logs.tokens'), render: (row) => <TokenBuckets row={row} /> },
    { key: 'status_code', header: t('logs.statusCode'), render: (row) => row.status_code > 0 ? row.status_code : '—' },
    { key: 'error', header: t('logs.error'), render: (row) => row.error_code || row.error_source ? <><span className="mono">{row.error_code || '—'}</span><br /><span className="table-note">{row.error_source || '—'}</span></> : '—' },
  ];
  const selected = selectedId ? logs.data?.items.find((row) => row.id === selectedId) : undefined;
  const detailFields = (row: StewardRequestLog): LogDetailField[] => [
    { label: t('logs.logId'), value: <span className="mono">{row.id}</span> },
    { label: t('common.userId'), value: <span className="mono">{row.user_id}</span> },
    { label: t('logs.routeKind'), value: row.route_kind },
    { label: t('logs.endpointBaseUrl'), value: <span className="mono read-only-value">{displayResource(row, row.endpoint_base_url)}</span> },
    { label: t('logs.upstreamModel'), value: <span className="mono">{displayResource(row, row.upstream_model_id)}</span> },
    { label: t('logs.endpointKeyId'), value: <span className="mono">{displayResource(row, row.endpoint_key_id)}</span> },
    { label: t('logs.statusCode'), value: row.status_code > 0 ? row.status_code : '—' },
    { label: t('logs.duration'), value: `${row.duration_ms} ms` },
    { label: t('logs.startedAt'), value: formatDateTime(row.started_at) },
    { label: t('logs.completedAt'), value: formatDateTime(row.completed_at) },
    { label: t('logs.bucketUncachedInput'), value: <CompactNumber value={formatCount(row.uncached_input_tokens)} /> },
    { label: t('logs.bucketCacheWrite'), value: <CompactNumber value={formatCount(row.cache_write_input_tokens)} /> },
    { label: t('logs.bucketCacheRead'), value: <CompactNumber value={formatCount(row.cache_read_input_tokens)} /> },
    { label: t('logs.bucketOutput'), value: <CompactNumber value={formatCount(row.output_tokens)} /> },
    { label: t('logs.usageUnknown'), value: row.usage_unknown ? t('common.yes') : t('common.no') },
    { label: t('logs.errorCode'), value: row.error_code || '—' },
    { label: t('logs.errorSource'), value: row.error_source || '—' },
    { label: t('logs.attemptId'), value: <span className="mono">{row.attempt_id || '—'}</span> },
  ];

  return <div className="page">
    <PageHeader eyebrow={t('user.steward.eyebrow')} title={t('user.steward.title')} description={t('user.steward.description')} />
    <div className="form-actions" role="tablist" aria-label={t('user.steward.sectionsLabel')}>
      <button id="steward-logs-tab" type="button" role="tab" aria-controls="steward-logs-panel" aria-selected={section === 'logs'} className={`btn ${section === 'logs' ? 'btn-primary' : 'btn-secondary'}`} onClick={() => setSection('logs')}>{t('user.steward.logsTab')}</button>
      <button id="steward-charity-tab" type="button" role="tab" aria-controls="steward-charity-panel" aria-selected={section === 'charity'} className={`btn ${section === 'charity' ? 'btn-primary' : 'btn-secondary'}`} onClick={() => setSection('charity')}>{t('user.steward.charityTab')}</button>
    </div>
    {section === 'charity' ? <div id="steward-charity-panel" role="tabpanel" aria-labelledby="steward-charity-tab"><CharityManagement frame="steward" /></div> : <div id="steward-logs-panel" role="tabpanel" aria-labelledby="steward-logs-tab"><Card>
      <h2>{t('user.steward.logsTitle')}</h2>
      <LogFilters fields={fields} state={state} onApply={applyFilters} />
      {logs.isPending ? <LoadingState /> : logs.error ? <ErrorState error={logs.error} onRetry={() => void logs.refetch()} /> : logs.data.items.length === 0 ? <EmptyState title={t('user.steward.logsEmpty')} body={t('user.steward.logsEmptyBody')} /> : <>
        <LogTable caption={t('user.steward.logsTitle')} columns={columns} rows={logs.data.items} rowKey={(row) => row.id} actions={(row) => <button type="button" className="btn btn-secondary" onClick={() => setSelectedId(row.id)} aria-haspopup="dialog">{t('logs.details')}</button>} />
        <Pagination page={state.page} hasNext={logs.data.hasNext} onChange={(page) => patch({ page })} pageSize={state.pageSize} pageSizeOptions={PAGE_SIZE_OPTIONS} onPageSizeChange={(pageSize) => patch({ pageSize, page: 1 })} onJumpToPage={(page) => patch({ page })} />
      </>}
    </Card></div>}
    <LogDetailDrawer open={Boolean(selected)} onClose={() => setSelectedId(null)} title={selected ? `${t('logs.drawerTitle')} #${selected.id}` : ''} fields={selected ? detailFields(selected) : []} diagnostics={selected?.error_diag ? { label: t('logs.diag'), text: selected.error_diag } : undefined} />
  </div>;
}
