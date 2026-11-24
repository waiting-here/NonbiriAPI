import { StatusBadge } from '@shared/components/ui/StatusBadge';
import { reportStatusTone } from '../features/operations/reports';
import { QueryFeedback } from '@shared/components/QueryFeedback';
import { GatewayConnectorLabel } from '@shared/components/GatewayConnectorLabel';
import { Link, useLocation } from 'react-router';
import { useSearchState } from '@shared/operations/useSearchState';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import {
  DataTable,
  Field,
  FilterBar,
  Note,
  Panel,
  PanelBody,
  PanelHead,
} from '@shared/components/ui';
import { formatCount } from '@shared/utils/formatNumber';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { useAdminSession } from '../data';
import {
  getReportBadge,
  REPORT_STATUSES,
  type ReportCaseSummary,
  type ReportStatus,
} from '../features/operations/reports';
import { reportPageKeys, useReportsPage } from '../features/operations/reportPages';
import '@shared/operations/operations.css';
import './reports.css';

function selectedStatus(values: string[]): ReportStatus | '' {
  return values.length === 1 ? (REPORT_STATUSES.find((status) => status === values[0]) ?? '') : '';
}

export function ReportsPage() {
  const formatDateTime = useDateTimeFormatter();
  const { t } = useTranslation();
  const location = useLocation();
  const [searchParams, setSearchParams] = useSearchState();
  const session = useAdminSession();
  const accountId = session.data?.admin.username ?? '';
  const scopeReady = !session.isPending && !session.error && Boolean(accountId);
  const status = selectedStatus(searchParams.getAll('status'));
  const pager = useUrlPagePager({
    station: 'admin',
    listType: 'reports',
    scopeKey: accountId || 'anonymous',
    scopeReady,
    resetKey: status,
  });
  const badge = useQuery({
    queryKey: reportPageKeys.badge(accountId || 'anonymous'),
    queryFn: ({ signal }) => getReportBadge(signal),
    enabled: scopeReady,
    retry: false,
    refetchInterval: 30_000,
  });
  const reports = useReportsPage(accountId, status, pager.page, pager.pageSize, scopeReady);
  const badgeVisual = badge.data
    ? BigInt(badge.data.total) > 99n
      ? '99+'
      : formatCount(badge.data.total).display
    : '—';
  const statusLabels: Record<ReportStatus, string> = {
    pending_indexing: t('admin.reports.status.pendingIndexing'),
    pending_review: t('admin.reports.status.pendingReview'),
    approved_processing: t('admin.reports.status.approvedProcessing'),
    approved: t('admin.reports.status.approved'),
    rejected: t('admin.reports.status.rejected'),
    expired: t('admin.reports.status.expired'),
  };
  const progressLabels: Record<ReportCaseSummary['progress_state'], string> = {
    in_progress: t('admin.reports.progress.inProgress'),
    complete: t('admin.reports.progress.complete'),
  };
  const retryClassLabels: Record<
    NonNullable<ReportCaseSummary['retry']>['last_error_class'],
    string
  > = {
    db_busy: t('admin.reports.retryClass.databaseBusy'),
    internal_retryable: t('admin.reports.retryClass.internalRetryable'),
    invariant_violation: t('admin.reports.retryClass.invariantViolation'),
  };
  const listReturn = (actualPage: string, actualPageSize: number): string => {
    const params = new URLSearchParams(location.search);
    params.delete('page');
    params.set('page', actualPage);
    params.delete('page_size');
    params.set('page_size', String(actualPageSize));
    const query = params.toString();
    return query ? `${location.pathname}?${query}` : location.pathname;
  };
  const updateStatus = (nextStatus: string) => {
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      next.delete('status');
      if (nextStatus) next.set('status', nextStatus);
      next.delete('page');
      next.set('page', '1');
      next.delete('page_size');
      next.set('page_size', String(pager.pageSize));
      return next;
    });
  };
  const authorityError = [session.error, badge.error, reports.error].find(
    (error) => isForbidden(error) || isUnauthorized(error),
  );
  if (session.error || authorityError)
    return (
      <ErrorState error={session.error ?? authorityError} onRetry={() => void session.refetch()} />
    );
  if (!scopeReady) return <LoadingState />;
  return (
    <div className="page ops-page">
      <PageHeader title={t('admin.reports.title')} description={t('admin.reports.description')} />
      <Panel>
        <PanelHead title={t('admin.reports.badge.title')} />
        <PanelBody>
          {badge.isPending ? (
            <LoadingState />
          ) : badge.error ? (
            <ErrorState error={badge.error} onRetry={() => void badge.refetch()} />
          ) : (
            <div className="nb-stats reports-stats">
              <div className="nb-stat">
                <span>{t('admin.reports.badge.title')}</span>
                <strong
                  className="nb-amount"
                  aria-label={t('admin.reports.badge.exactCount', { count: badge.data?.total })}
                >
                  {badgeVisual}
                </strong>
              </div>
              <div className="nb-stat">
                <span>{t('admin.reports.badge.indexing')}</span>
                <strong className="nb-amount">
                  {formatCount(badge.data?.by_status.pending_indexing ?? 0).display}
                </strong>
              </div>
              <div className="nb-stat">
                <span>{t('admin.reports.badge.review')}</span>
                <strong className="nb-amount">
                  {formatCount(badge.data?.by_status.pending_review ?? 0).display}
                </strong>
              </div>
              <div className="nb-stat">
                <span>{t('admin.reports.badge.processing')}</span>
                <strong className="nb-amount">
                  {formatCount(badge.data?.by_status.approved_processing ?? 0).display}
                </strong>
              </div>
            </div>
          )}
          <Note tone="info" title={t('admin.reports.privacy.title')}>
            {t('admin.reports.privacy.body')}
          </Note>
        </PanelBody>
      </Panel>
      <Panel>
        <PanelBody>
          <FilterBar
            activeCount={status ? 1 : 0}
            search={
              <>
                <Field size="short" label={t('admin.reports.filter.status')}>
                  {(field) => (
                    <select
                      {...field}
                      value={status}
                      onChange={(event) => updateStatus(event.target.value)}
                    >
                      <option value="">{t('admin.reports.filter.all')}</option>
                      {REPORT_STATUSES.map((item) => (
                        <option key={item} value={item}>
                          {statusLabels[item]}
                        </option>
                      ))}
                    </select>
                  )}
                </Field>
                {status ? (
                  <button
                    type="button"
                    className="nb-btn nb-btn--ghost"
                    onClick={() => updateStatus('')}
                  >
                    {t('common.resetFilter')}
                  </button>
                ) : null}
              </>
            }
          />
          {reports.error && !reports.data ? (
            <ErrorState error={reports.error} onRetry={() => void reports.refetch()} />
          ) : reports.data ? (
            <div className="ops-stack" aria-busy={reports.isFetching}>
              <QueryFeedback query={reports} />
              {reports.error ? (
                <ErrorState error={reports.error} onRetry={() => void reports.refetch()} />
              ) : null}
              {reports.data.data.length === 0 ? (
                <EmptyState
                  title={t('admin.reports.empty.title')}
                  body={
                    status
                      ? t('admin.reports.empty.filtered', { status: statusLabels[status] })
                      : t('admin.reports.empty.body')
                  }
                />
              ) : (
                <DataTable
                  caption={t('admin.reports.title')}
                  rows={reports.data.data}
                  rowKey={(item) => item.id}
                  columns={[
                    {
                      key: 'status',
                      header: t('admin.reports.table.status'),
                      cell: 'status',
                      render: (item) => (
                        <StatusBadge tone={reportStatusTone[item.status]}>
                          {statusLabels[item.status]}
                        </StatusBadge>
                      ),
                    },
                    {
                      key: 'connectorUrl',
                      header: t('admin.reports.table.connectorUrl'),
                      cell: 'title',
                      render: (item) => (
                        <div className="nb-stack nb-stack--xs">
                          <strong>
                            <GatewayConnectorLabel value={item.connector_type} />
                          </strong>
                          <span className="mono nb-small nb-muted ops-wrap">
                            {item.canonical_base_url}
                          </span>
                        </div>
                      ),
                    },
                    {
                      key: 'progress',
                      header: t('admin.reports.table.progress'),
                      mobileLabel: t('admin.reports.table.progress'),
                      cell: 'meta',
                      render: (item) => (
                        <div>
                          <span>{progressLabels[item.progress_state]}</span>
                          {item.retry ? (
                            <div className="nb-small nb-muted">
                              {t('admin.reports.table.retry', {
                                count: item.retry.attempt_count,
                              })}{' '}
                              · {retryClassLabels[item.retry.last_error_class]}
                            </div>
                          ) : null}
                        </div>
                      ),
                    },
                    {
                      key: 'counts',
                      header: t('admin.reports.table.counts'),
                      mobileLabel: t('admin.reports.table.counts'),
                      cell: 'meta',
                      render: (item) => (
                        <div className="reports-counts nb-small">
                          <span>
                            {t('admin.reports.table.materials', {
                              count: formatCount(item.counts.materials).display,
                            })}
                          </span>
                          <span>
                            {t('admin.reports.table.targets', {
                              count: formatCount(item.counts.targets).display,
                            })}
                          </span>
                          <span>
                            {t('admin.reports.table.processed', {
                              count: formatCount(item.counts.processed).display,
                            })}
                          </span>
                        </div>
                      ),
                    },
                    {
                      key: 'deadline',
                      header: t('admin.reports.table.deadline'),
                      mobileLabel: t('admin.reports.table.deadline'),
                      cell: 'meta',
                      render: (item) => formatDateTime(item.deadline),
                    },
                    {
                      key: 'open',
                      header: t('admin.reports.table.open'),
                      cell: 'action',
                      align: 'action',
                      render: (item) => (
                        <Link
                          className="nb-btn nb-btn--secondary nb-btn--sm"
                          aria-disabled={reports.isFetching || Boolean(reports.error)}
                          onClick={(event) => {
                            if (reports.isFetching || reports.error) event.preventDefault();
                          }}
                          to={`/reports/${encodeURIComponent(item.id)}`}
                          state={{
                            returnTo: listReturn(
                              reports.data.pagination.page,
                              reports.data.pagination.page_size,
                            ),
                          }}
                        >
                          {item.status === 'pending_review'
                            ? t('admin.reports.table.review')
                            : t('admin.reports.table.view')}
                        </Link>
                      ),
                    },
                  ]}
                />
              )}
              <PagePagination
                metadata={reports.data.pagination}
                requestedPage={pager.page}
                busy={reports.isFetching}
                onPageChange={pager.setPage}
                onPageSizeChange={pager.setPageSize}
              />
            </div>
          ) : (
            <LoadingState />
          )}
        </PanelBody>
      </Panel>
    </div>
  );
}
