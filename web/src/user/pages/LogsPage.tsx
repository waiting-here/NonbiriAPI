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
import { formatCount } from '@shared/utils/formatNumber';
import { UserPageGate } from '../components/UserPageGate';
import {
  useUserLogOptions,
  useUserLogs,
  type UserRequestLog,
} from '../data';

const PAGE_SIZE_OPTIONS = [10, 20, 50] as const;
const DEFAULT_PAGE_SIZE = 20;

// Frozen user filter set, mirrored in the URL query string. Ownership comes
// exclusively from the session; there is no user_id parameter and no export.
const TEXT_PARAMS = ['model', 'error_code', 'status'] as const;

function buildApiFilter(filters: Record<string, string>) {
  const next: { model?: string; errorCode?: string; status?: string } = {};
  const model = filters.model?.trim();
  if (model) next.model = model.slice(0, 512);
  const errorCode = filters.error_code?.trim();
  if (errorCode) next.errorCode = errorCode.slice(0, 512);
  const status = filters.status?.trim();
  if (status && /^\d{3}$/.test(status)) {
    const code = Number(status);
    if (code >= 100 && code <= 599) next.status = status;
  }
  return next;
}

function LogsContent() {
  const { t } = useTranslation();
  const { state, patch } = useLogUrlState(TEXT_PARAMS, DEFAULT_PAGE_SIZE);
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const options = useUserLogOptions();
  const filter = useMemo(
    () => ({
      ...buildApiFilter(state.filters),
      fromUnix: state.fromUnix,
      toUnix: state.toUnix,
    }),
    [state.filters, state.fromUnix, state.toUnix],
  );
  const logs = useUserLogs(state.page, filter, state.pageSize);

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
      name: 'model',
      label: t('common.model'),
      ariaLabel: t('common.filterModelAria'),
      maxLength: 256,
      suggestions: options.data?.models,
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

  const columns: LogColumn<UserRequestLog>[] = [
    {
      key: 'started_at',
      header: t('logs.time'),
      render: (row) => <span title={formatDateTime(row.completed_at)}>{formatDateTime(row.started_at)}</span>,
    },
    {
      key: 'model',
      header: t('common.model'),
      render: (row) => (
        <>
          <span className="mono">{row.model}</span>
          <br />
          <span className="table-note">{row.upstream_model_id}</span>
        </>
      ),
    },
    {
      key: 'note',
      header: t('logs.note'),
      // Notes are the current values of the caller's own resources; both are
      // empty once a resource has been deleted, which renders as a dash.
      render: (row) =>
        row.endpoint_note || row.key_note ? (
          <>
            {row.endpoint_note || '—'}
            {row.key_note ? (
              <>
                <br />
                <span className="table-note">{row.key_note}</span>
              </>
            ) : null}
          </>
        ) : (
          '—'
        ),
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
            <span className="table-note">
              {t(`logs.errorSource${row.error_source === 'upstream' ? 'Upstream' : 'Platform'}`)}
            </span>
          </>
        ) : (
          '—'
        ),
    },
  ];

  const selected = selectedId ? logs.data?.items.find((row) => row.id === selectedId) : undefined;

  const detailFields = (row: UserRequestLog): LogDetailField[] => [
    { label: t('logs.logId'), value: <span className="mono">{row.id}</span> },
    { label: t('common.model'), value: <span className="mono">{row.model}</span> },
    { label: t('logs.upstreamModel'), value: <span className="mono">{row.upstream_model_id}</span> },
    { label: t('logs.routeKind'), value: row.route_kind },
    {
      label: t('logs.endpointBaseUrl'),
      value: <span className="mono read-only-value">{row.endpoint_base_url}</span>,
    },
    { label: t('logs.endpointKeyId'), value: <span className="mono">{row.endpoint_key_id}</span> },
    { label: t('logs.endpointNote'), value: row.endpoint_note || '—' },
    { label: t('logs.keyNote'), value: row.key_note || '—' },
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
    { label: t('logs.usageUnknown'), value: row.usage_unknown ? t('common.yes') : t('common.no') },
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
        title={t('user.logs.title')}
        description={t('user.logs.description')}
      />
      <Card>
        <div className="card-title-row">
          <h2>{t('user.logs.listTitle')}</h2>
          <span className="muted">{t('common.page', { page: state.page })}</span>
        </div>
        <LogFilters fields={fields} state={state} onApply={applyFilters} />
        {logs.isPending ? (
          <LoadingState />
        ) : logs.error ? (
          <ErrorState error={logs.error} onRetry={() => void logs.refetch()} />
        ) : logs.data.items.length === 0 ? (
          <EmptyState
            title={state.page > 1 ? t('common.noResults') : t('user.logs.empty')}
            body={state.page > 1 ? t('common.noResultsBody') : t('user.logs.emptyBody')}
          />
        ) : (
          <>
            <LogTable
              caption={t('user.logs.listTitle')}
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
              hasNext={logs.data.hasMore}
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
                  `model=${selected.model}`,
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

export function LogsPage() {
  return (
    <UserPageGate>
      <LogsContent />
    </UserPageGate>
  );
}
