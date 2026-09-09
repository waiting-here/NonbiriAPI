import { Link, useLocation } from 'react-router';
import { useSearchState } from '@shared/operations/useSearchState';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge,
} from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import { useAdminSession } from '../data';
import {
  getReportBadge,
  REPORT_STATUSES,
  type ReportCaseSummary,
  type ReportStatus,
} from '../features/operations/reports';
import { reportPageKeys, useReportsPage } from '../features/operations/reportPages';
import '@shared/operations/operations.css';

function selectedStatus(values: string[]): string {
  return values.length === 1 && REPORT_STATUSES.includes(values[0] as ReportStatus)
    ? values[0]
    : '';
}

export function ReportsPage() {
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
      : badge.data.total
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
      <div className="ops-grid">
        <Card>
          <h2>{t('admin.reports.badge.title')}</h2>
          {badge.isPending ? (
            <LoadingState />
          ) : badge.error ? (
            <ErrorState error={badge.error} onRetry={() => void badge.refetch()} />
          ) : (
            <>
              <strong
                className="metric-value"
                aria-label={t('admin.reports.badge.exactCount', { count: badge.data?.total })}
              >
                {badgeVisual}
              </strong>
              <dl className="ops-kv">
                <dt>{t('admin.reports.badge.indexing')}</dt>
                <dd>{badge.data?.by_status.pending_indexing}</dd>
                <dt>{t('admin.reports.badge.review')}</dt>
                <dd>{badge.data?.by_status.pending_review}</dd>
                <dt>{t('admin.reports.badge.processing')}</dt>
                <dd>{badge.data?.by_status.approved_processing}</dd>
              </dl>
            </>
          )}
        </Card>
        <Card>
          <h2>{t('admin.reports.privacy.title')}</h2>
          <p>{t('admin.reports.privacy.body')}</p>
        </Card>
      </div>
      <Card>
        <label className="ops-form-field">
          <span>{t('admin.reports.filter.status')}</span>
          <select
            value={status}
            onChange={(event) => {
              const nextStatus = event.target.value;
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
            }}
          >
            <option value="">{t('admin.reports.filter.all')}</option>
            {REPORT_STATUSES.map((item) => (
              <option key={item} value={item}>
                {statusLabels[item]}
              </option>
            ))}
          </select>
        </label>
        {reports.error && !reports.data ? (
          <ErrorState error={reports.error} onRetry={() => void reports.refetch()} />
        ) : reports.data ? (
          <div className="ops-stack" aria-busy={reports.isFetching}>
            {reports.error ? (
              <ErrorState error={reports.error} onRetry={() => void reports.refetch()} />
            ) : null}
            {reports.data.data.length === 0 ? (
              <EmptyState
                title={t('admin.reports.empty.title')}
                body={t('admin.reports.empty.body')}
              />
            ) : (
              <div className="ops-table-scroll">
                <table className="ops-table ops-table--responsive">
                  <thead>
                    <tr>
                      <th>{t('admin.reports.table.status')}</th>
                      <th>{t('admin.reports.table.connectorUrl')}</th>
                      <th>{t('admin.reports.table.progress')}</th>
                      <th>{t('admin.reports.table.counts')}</th>
                      <th>{t('admin.reports.table.deadline')}</th>
                      <th>{t('admin.reports.table.open')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {reports.data.data.map((item) => (
                      <tr key={item.id}>
                        <td data-label={t('admin.reports.table.status')}>
                          <StatusBadge
                            active={item.status === 'pending_review'}
                            danger={item.status === 'approved_processing'}
                            label={statusLabels[item.status]}
                          />
                        </td>
                        <td
                          className="ops-cell-wide"
                          data-label={t('admin.reports.table.connectorUrl')}
                        >
                          {item.connector_type}
                          <br />
                          <span className="ops-wrap">{item.canonical_base_url}</span>
                        </td>
                        <td data-label={t('admin.reports.table.progress')}>
                          {progressLabels[item.progress_state]}
                          {item.retry ? (
                            <>
                              <br />
                              {t('admin.reports.table.retry', {
                                count: item.retry.attempt_count,
                              })}{' '}
                              · {retryClassLabels[item.retry.last_error_class]}
                            </>
                          ) : null}
                        </td>
                        <td data-label={t('admin.reports.table.counts')}>
                          {t('admin.reports.table.materials', { count: item.counts.materials })}
                          <br />
                          {t('admin.reports.table.targets', { count: item.counts.targets })}
                          <br />
                          {t('admin.reports.table.processed', { count: item.counts.processed })}
                        </td>
                        <td data-label={t('admin.reports.table.deadline')}>
                          {formatDateTime(item.deadline)}
                        </td>
                        <td data-label={t('admin.reports.table.open')}>
                          <Link
                            className="btn btn-secondary"
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
                            {t('admin.reports.table.review')}
                          </Link>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            <PagePagination
              metadata={reports.data.pagination}
              requestedPage={pager.page}
              busy={reports.isFetching}
              onPageChange={pager.setPage}
              onPageSizeChange={pager.setPageSize}
            />
          </div>
        ) : reports.isPending ? (
          <LoadingState />
        ) : (
          <LoadingState />
        )}
      </Card>
    </div>
  );
}
