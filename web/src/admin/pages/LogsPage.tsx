import { useMemo, useState } from 'react';
import { formatDateTime } from '@shared/utils/datetime';
import { useTranslation } from 'react-i18next';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  Pagination,
} from '@shared/components/States';
import {
  LogDetailDrawer,
  LogFilters,
  LogTable,
  TokenBuckets,
  useLogUrlState,
  type LogColumn,
  type LogDetailField,
  type LogFilterField,
} from '@shared/components/log';
import { CompactNumber } from '@shared/components/CompactNumber';
import { formatCompact, formatCount } from '@shared/utils/formatNumber';
import {
  adminLogExportPath,
  useAdminLogs,
  useAdminUsage,
  type AdminLogFilter,
  type AdminRequestLog,
} from '../data';

const PAGE_SIZE_OPTIONS = [10, 20, 50] as const;
const DEFAULT_PAGE_SIZE = 20;

// Frozen administrator filter set, mirrored in the URL query string.
const TEXT_PARAMS = ['user_id', 'endpoint_base_url', 'upstream_model', 'error_code', 'status'] as const;

function buildApiFilter(filters: Record<string, string>): AdminLogFilter {
  const next: AdminLogFilter = {};
  const userId = filters.user_id?.trim();
  if (userId && /^\d+$/.test(userId)) next.userId = userId;
  const baseURL = filters.endpoint_base_url?.trim();
  if (baseURL) next.endpointBaseURL = baseURL.slice(0, 512);
  const upstream = filters.upstream_model?.trim();
  if (upstream) next.upstreamModel = upstream.slice(0, 512);
  const errorCode = filters.error_code?.trim();
  if (errorCode) next.errorCode = errorCode.slice(0, 512);
  // The server status filter is one exact 100..599 code, not a band.
  const status = filters.status?.trim();
  if (status && /^\d{3}$/.test(status)) {
    const code = Number(status);
    if (code >= 100 && code <= 599) next.status = status;
  }
  return next;
}

export function LogsPage() {
  const { t } = useTranslation();
  const { state, patch } = useLogUrlState(TEXT_PARAMS, DEFAULT_PAGE_SIZE);
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const usage = useAdminUsage();
  const filter = useMemo(
    () => ({
      ...buildApiFilter(state.filters),
      fromUnix: state.fromUnix,
      toUnix: state.toUnix,
    }),
    [state.filters, state.fromUnix, state.toUnix],
  );
  const logs = useAdminLogs(state.page, filter, state.pageSize);

  const applyFilters = (next: {
    filters: Record<string, string>;
    fromUnix?: number;
    toUnix?: number;
  }) => {
    // A changed filter always restarts paging at page 1.
    patch({ ...next, page: 1 });
  };

  const changePageSize = (size: number) => {
    patch({ pageSize: size, page: 1 });
  };

  const fields: LogFilterField[] = [
    {
      name: 'user_id',
      label: t('common.userId'),
      ariaLabel: t('common.filterUserIdAria'),
      inputType: 'number',
      maxLength: 19,
    },
    {
      name: 'endpoint_base_url',
      label: t('logs.endpointBaseUrl'),
      ariaLabel: t('logs.endpointBaseUrlAria'),
      maxLength: 512,
    },
    {
      name: 'upstream_model',
      label: t('logs.upstreamModel'),
      ariaLabel: t('logs.upstreamModelAria'),
      maxLength: 256,
    },
    {
      name: 'error_code',
      label: t('logs.errorCode'),
      ariaLabel: t('logs.errorCodeAria'),
      maxLength: 96,
    },
    {
      name: 'status',
      label: t('common.status'),
      ariaLabel: t('common.filterStatusAria'),
      inputType: 'number',
      maxLength: 3,
    },
  ];

  const columns: LogColumn<AdminRequestLog>[] = [
    {
      key: 'started_at',
      header: t('logs.time'),
      render: (row) => <span title={formatDateTime(row.completed_at)}>{formatDateTime(row.started_at)}</span>,
    },
    { key: 'user_id', header: t('common.userId'), render: (row) => <span className="mono">{row.user_id}</span> },
    { key: 'route_kind', header: t('logs.routeKind'), render: (row) => row.route_kind },
    {
      key: 'endpoint_base_url',
      header: t('logs.endpointBaseUrl'),
      render: (row) => <span className="mono read-only-value">{row.endpoint_base_url}</span>,
    },
    {
      key: 'upstream_model_id',
      header: t('logs.upstreamModel'),
      render: (row) => <span className="mono">{row.upstream_model_id}</span>,
    },
    {
      key: 'status_code',
      header: t('logs.statusCode'),
      render: (row) => (row.status_code > 0 ? row.status_code : '—'),
    },
    {
      key: 'duration_ms',
      header: t('logs.duration'),
      render: (row) => `${formatCount(row.duration_ms).display} ms`,
    },
    { key: 'tokens', header: t('logs.tokens'), render: (row) => <TokenBuckets row={row} /> },
    {
      key: 'error',
      header: t('logs.error'),
      render: (row) =>
        row.error_code || row.error_source ? (
          <>
            <span className="mono">{row.error_code || '—'}</span>
            <br />
            <span className="table-note">{t(`logs.errorSource${row.error_source === 'upstream' ? 'Upstream' : 'Platform'}`)}</span>
          </>
        ) : (
          '—'
        ),
    },
  ];

  const selected = selectedId ? logs.data?.items.find((row) => row.id === selectedId) : undefined;

  const detailFields = (row: AdminRequestLog): LogDetailField[] => [
    { label: t('logs.logId'), value: <span className="mono">{row.id}</span> },
    { label: t('common.userId'), value: <span className="mono">{row.user_id}</span> },
    { label: t('logs.routeKind'), value: row.route_kind },
    {
      label: t('logs.endpointBaseUrl'),
      value: <span className="mono read-only-value">{row.endpoint_base_url}</span>,
    },
    { label: t('logs.upstreamModel'), value: <span className="mono">{row.upstream_model_id}</span> },
    { label: t('logs.endpointKeyId'), value: <span className="mono">{row.endpoint_key_id}</span> },
    { label: t('logs.statusCode'), value: row.status_code > 0 ? row.status_code : '—' },
    { label: t('logs.duration'), value: `${formatCount(row.duration_ms).display} ms` },
    { label: t('logs.startedAt'), value: formatDateTime(row.started_at) },
    { label: t('logs.completedAt'), value: formatDateTime(row.completed_at) },
    {
      label: t('logs.bucketUncachedInput'),
      value: <CompactNumber value={formatCount(row.uncached_input_tokens)} />,
    },
    {
      label: t('logs.bucketCacheWrite'),
      value: <CompactNumber value={formatCount(row.cache_write_input_tokens)} />,
    },
    {
      label: t('logs.bucketCacheRead'),
      value: <CompactNumber value={formatCount(row.cache_read_input_tokens)} />,
    },
    { label: t('logs.bucketOutput'), value: <CompactNumber value={formatCount(row.output_tokens)} /> },
    {
      label: t('logs.usageUnknown'),
      value: row.usage_unknown ? t('common.yes') : t('common.no'),
    },
    { label: t('logs.errorCode'), value: row.error_code || '—' },
    {
      label: t('logs.errorSource'),
      value: row.error_source
        ? t(`logs.errorSource${row.error_source === 'upstream' ? 'Upstream' : 'Platform'}`)
        : '—',
    },
    { label: t('logs.attemptId'), value: <span className="mono">{row.attempt_id || '—'}</span> },
  ];

  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('admin.logs.title')}
        description={t('admin.logs.description')}
      />
      <Card>
        <div className="card-title-row">
          <h2>{t('admin.logs.usageTitle')}</h2>
        </div>
        {usage.isPending ? (
          <LoadingState />
        ) : usage.error ? (
          <ErrorState error={usage.error} onRetry={() => void usage.refetch()} />
        ) : (
          <div className="metric-grid">
            <div className="metric-card">
              <p>{t('admin.dashboard.requests')}</p>
              <strong className="metric-value">{formatCount(usage.data.total_requests).display}</strong>
            </div>
            <div className="metric-card">
              <p>{t('common.tokens.input')}</p>
              <strong className="metric-value">
                <CompactNumber value={formatCompact(usage.data.total_prompt_tokens)} />
              </strong>
            </div>
            <div className="metric-card">
              <p>{t('common.tokens.output')}</p>
              <strong className="metric-value">
                <CompactNumber value={formatCompact(usage.data.total_completion_tokens)} />
              </strong>
            </div>
            <div className="metric-card">
              <p>{t('admin.dashboard.unknownUsage')}</p>
              <strong className="metric-value">
                {formatCount(usage.data.total_unknown_usage_requests).display}
              </strong>
            </div>
          </div>
        )}
      </Card>
      <Card>
        <div className="card-title-row">
          <h2>{t('admin.logs.logsTitle')}</h2>
          <div className="card-actions">
            <a
              className="btn btn-secondary"
              href={adminLogExportPath(filter, 'csv')}
              download
              aria-label={t('admin.logs.exportCsv')}
            >
              {t('admin.logs.exportCsv')}
            </a>
            <a
              className="btn btn-secondary"
              href={adminLogExportPath(filter, 'json')}
              download
              aria-label={t('admin.logs.exportJson')}
            >
              {t('admin.logs.exportJson')}
            </a>
          </div>
        </div>
        <LogFilters fields={fields} state={state} onApply={applyFilters} />
        {logs.isPending ? (
          <LoadingState />
        ) : logs.error ? (
          <ErrorState error={logs.error} onRetry={() => void logs.refetch()} />
        ) : logs.data.items.length === 0 ? (
          <EmptyState title={t('admin.logs.noLogs')} body={t('admin.logs.noLogsBody')} />
        ) : (
          <>
            <LogTable
              caption={t('admin.logs.logsTitle')}
              columns={columns}
              rows={logs.data.items}
              rowKey={(row) => row.id}
              actions={(row) => (
                <button
                  type="button"
                  className="btn btn-secondary"
                  onClick={() => setSelectedId(row.id)}
                  aria-haspopup="dialog"
                >
                  {t('logs.details')}
                </button>
              )}
            />
            <Pagination
              page={state.page}
              hasNext={logs.data.hasNext}
              onChange={(page) => patch({ page })}
              pageSize={state.pageSize}
              pageSizeOptions={PAGE_SIZE_OPTIONS}
              onPageSizeChange={changePageSize}
              onJumpToPage={(page) => patch({ page })}
            />
          </>
        )}
      </Card>
      <LogDetailDrawer
        open={Boolean(selected)}
        onClose={() => setSelectedId(null)}
        title={selected ? `${t('logs.drawerTitle')} #${selected.id}` : ''}
        fields={selected ? detailFields(selected) : []}
        diagnostics={
          selected
            ? {
                label: t('logs.diag'),
                text: [
                  `id=${selected.id}`,
                  `user_id=${selected.user_id}`,
                  `route_kind=${selected.route_kind}`,
                  `status_code=${selected.status_code}`,
                  `error_code=${selected.error_code}`,
                  `error_source=${selected.error_source}`,
                  `attempt_id=${selected.attempt_id}`,
                  selected.error_diag,
                ]
                  .filter(Boolean)
                  .join('\n'),
              }
            : undefined
        }
      />
    </div>
  );
}
